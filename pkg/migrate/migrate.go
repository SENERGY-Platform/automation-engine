/*
 * Copyright 2026 InfAI (CC SES)
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *    http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

// Package migrate moves the data of the Camunda based services into the engine (MIG-01 to
// MIG-09). It reads the databases of the old services and the REST API of Camunda directly
// (decided for M7) and writes through the controller and the store of the engine.
//
// A run is a dry run unless Apply is set. It only creates what is missing, so it can run again
// after a partial failure; ids are kept, and where a new id is needed it is derived from the
// old one, so that a second run finds what the first one created. The report lists per step
// what was migrated, what existed already, what needs a decision and what failed.
package migrate

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
)

// Options steer a run.
type Options struct {
	// without Apply nothing is written
	Apply bool
	// deployments that exist only in Camunda and are to be migrated, by Camunda deployment id;
	// all others are listed for a decision (MIG-03)
	CamundaOnly map[string]bool
	// owners for resources whose owner is empty or invalid, e.g. releases with the creator
	// placeholder-for-invalid-data (MIG-09)
	Owners map[string]string
	// running instances are not restarted, only listed (MIG-02)
	NoRestart bool
}

// owner maps an owner; empty means the resource has none and needs a decision
func (this Options) owner(original string) string {
	if mapped, ok := this.Owners[original]; ok {
		return mapped
	}
	if original == "" || original == "placeholder-for-invalid-data" {
		return ""
	}
	return original
}

type Status string

const (
	Migrated Status = "migrated"
	// would be migrated in a dry run
	Planned  Status = "planned"
	Existing Status = "existing"
	// needs a decision or a manual step
	Manual Status = "manual"
	// left out on purpose, e.g. a deleted resource
	Skipped Status = "skipped"
	Failed  Status = "failed"
)

// Findings of the model rewrite of a deployment (MIG-04), as items of kind model; the item of
// the deployment carries its outcome.
const (
	Rewritten   Status = "rewritten"
	Check       Status = "check"
	Unsupported Status = "unsupported"
)

type Item struct {
	Step    string `json:"step"`
	Kind    string `json:"kind"`
	Id      string `json:"id"`
	Owner   string `json:"owner,omitempty"`
	Status  Status `json:"status"`
	Message string `json:"message,omitempty"`
}

type Report struct {
	Items []Item `json:"items"`
}

func (this *Report) add(item Item) {
	this.Items = append(this.Items, item)
}

func (this *Report) Count(status Status) int {
	n := 0
	for _, i := range this.Items {
		if i.Status == status {
			n++
		}
	}
	return n
}

// ExitCode is 0 for a complete run, 1 if anything needs a decision or failed; findings of the
// model rewrite do not count, an unsupported part makes its deployment manual
func (this *Report) ExitCode() int {
	if this.Count(Failed) > 0 || this.Count(Manual) > 0 {
		return 1
	}
	return 0
}

// Write prints the report: a summary per step and kind, then every item that is not done and
// the rewritten parts and those to check of the models; the unsupported ones are named by the
// item of their deployment
func (this *Report) Write(w io.Writer) {
	type key struct{ step, kind string }
	counts := map[key]map[Status]int{}
	var keys []key
	for _, i := range this.Items {
		k := key{i.Step, i.Kind}
		if counts[k] == nil {
			counts[k] = map[Status]int{}
			keys = append(keys, k)
		}
		counts[k][i.Status]++
	}
	sort.SliceStable(keys, func(a, b int) bool { return keys[a].step < keys[b].step })
	for _, k := range keys {
		var parts []string
		for _, s := range []Status{Migrated, Planned, Existing, Skipped, Manual, Failed, Rewritten, Check, Unsupported} {
			if n := counts[k][s]; n > 0 {
				parts = append(parts, fmt.Sprintf("%v %v", n, s))
			}
		}
		_, _ = fmt.Fprintf(w, "%-24v %-20v %v\n", k.step, k.kind, strings.Join(parts, ", "))
	}
	for _, i := range this.Items {
		if i.Status == Manual || i.Status == Failed || i.Status == Rewritten || i.Status == Check || (i.Status == Skipped && i.Message != "") {
			_, _ = fmt.Fprintf(w, "%v\t%v\t%v\t%v\towner=%v\t%v\n", i.Status, i.Step, i.Kind, i.Id, i.Owner, i.Message)
		}
	}
}

// Run migrates step by step; a step that cannot read its source fails as a whole, the next
// steps run nevertheless.
func Run(ctx context.Context, options Options, sources Sources, target *Target) *Report {
	m := &migration{options: options, sources: sources, target: target, report: &Report{}, instanceIds: map[string]string{}, claimed: map[string]string{}, keyIds: map[keyRef][]string{}, planned: map[string]string{}, networkOwners: map[string]string{}}
	steps := []struct {
		name string
		run  func(context.Context) error
	}{
		{"process-models", m.processModels},
		{"process-deployments", m.processDeployments},
		{"camunda-deployments", m.camundaDeployments},
		{"running-instances", m.runningInstances},
		// before the smart services, whose modules refer to these instances by business key
		{"fog-deployments", m.fogDeployments},
		{"fog-instances", m.fogStarts},
		{"schedules", m.schedules},
		{"process-io", m.ioValues},
		{"smart-service-designs", m.designs},
		{"smart-service-releases", m.releases},
		{"smart-service-instances", m.smartServiceInstances},
		{"smart-service-modules", m.modules},
		{"smart-service-variables", m.smartServiceVariables},
	}
	for _, step := range steps {
		if err := step.run(ctx); err != nil {
			m.report.add(Item{Step: step.name, Kind: "source", Status: Failed, Message: err.Error()})
		}
	}
	return m.report
}

type migration struct {
	options Options
	sources Sources
	target  *Target
	report  *Report
	// models by owner and BPMN, to find the model of a deployment (MIG-01)
	modelsByContent map[string]string
	// deployments of process-deployment, by id
	deployments map[string]ProcessDeployment
	// deployments of the Camunda engines
	camunda []CamundaDeployment
	// BPMN of the deployments a dry run would create, so that later steps judge as with apply
	planned map[string]string
	// designs that became models
	migratedDesigns map[string]bool
	// release id -> deployment and version
	releaseRefs map[string]releaseRef
	// migrated smart-service instances and their owners
	smartInstances map[string]string
	// modules written together with their instance
	insertedModules map[string]bool
	// ids of fog deployments, platform and Camunda id -> deployment id
	fogIds map[string]string
	// owners of networks
	networkOwners map[string]string
	// Camunda process instance id -> instance id in the engine, for IO values
	instanceIds map[string]string
	// instance ids assigned in this run -> what they were assigned to
	claimed map[string]string
	// business keys -> the ids of the instances that had them, for references by key
	keyIds map[keyRef][]string
}

func (this *migration) result(step string, kind string, id string, owner string, status Status, format string, args ...any) {
	if status == Migrated && !this.options.Apply {
		status = Planned
	}
	this.report.add(Item{Step: step, Kind: kind, Id: id, Owner: owner, Status: status, Message: fmt.Sprintf(format, args...)})
}
