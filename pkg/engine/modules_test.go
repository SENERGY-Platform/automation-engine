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

package engine

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	b "github.com/SENERGY-Platform/automation-engine/pkg/bpmn/bpmntest"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
)

// creating is a task type that creates a module per execution and reports its done event
type creating struct {
	mux    sync.Mutex
	before func(task Task)
	undone []string
}

func (this *creating) Execute(ctx context.Context, task Task) (TaskResult, error) {
	if this.before != nil {
		this.before(task)
	}
	done := "done_" + task.JobId
	return TaskResult{
		Output: map[string]any{"done_event": done},
		Effects: &Effects{
			Modules:   []model.Module{{Id: task.JobId, ModuleType: "resource", Data: map[string]any{"name": task.Input["name"]}, Keys: []string{"k"}, DeleteInfo: &model.ModuleDeleteInfo{Url: "http://resources/" + task.JobId, UserId: task.Owner}}},
			Variables: map[string]any{"created": task.JobId},
			Messages:  []string{done},
		},
	}, nil
}

func (this *creating) Undo(_ context.Context, task Task, _ *Effects, _ error) {
	this.mux.Lock()
	defer this.mux.Unlock()
	this.undone = append(this.undone, task.JobId)
}

// moduleProcess creates a resource and waits for its done event, whose name it reads from the output
func moduleProcess() string {
	return b.Process(
		b.Start("start"),
		b.Service("create", "creating", b.InText("name", "res"), b.OutScript("done", "done_event")),
		`<bpmn:intermediateCatchEvent id="ready" senergy:messageName="done"><bpmn:messageEventDefinition/></bpmn:intermediateCatchEvent>`,
		b.Script("after", "finished", "true"), b.End("end"),
		b.Flow("start", "create"), b.Flow("create", "ready"), b.Flow("ready", "after"), b.Flow("after", "end"),
	)
}

func (this *harness) modules(instanceId string) (result []model.Module) {
	this.t.Helper()
	err := this.s.Tx(ctx, func(tx *store.Tx) (err error) {
		result, err = tx.ModulesOfInstance(instanceId, "", "")
		return err
	})
	if err != nil {
		this.t.Fatal(err)
	}
	return result
}

func (this *harness) removals() (result []store.ModuleRemoval) {
	this.t.Helper()
	err := this.s.Tx(ctx, func(tx *store.Tx) (err error) {
		result, err = tx.DueModuleRemovals(time.Now().Add(time.Hour), 100)
		return err
	})
	if err != nil {
		this.t.Fatal(err)
	}
	return result
}

func TestTaskEffectsCreateModulesAndDeliverTheirDoneEvent(t *testing.T) {
	run(t, func(t *testing.T, h *harness) {
		h.e.RegisterTaskType("creating", &creating{})
		d := h.deploy(moduleProcess())
		inst := h.start(d, nil)
		h.process()
		h.expectState(inst.Id, model.StateCompleted)
		vars := h.vars(inst.Id)
		if vars["finished"] != true || vars["created"] == nil {
			t.Fatalf("the buffered done event continues the instance without correlation (SMS-04): %v", vars)
		}
		modules := h.modules(inst.Id)
		if len(modules) != 1 || modules[0].ModuleType != "resource" || modules[0].Owner != "user" || modules[0].Data["name"] != "res" || modules[0].Keys[0] != "k" || modules[0].ElementId != "create" {
			t.Fatalf("the module belongs to the instance (SMS-12): %+v", modules)
		}
		err := h.s.Tx(ctx, func(tx *store.Tx) error {
			deleted, err := tx.DeleteFinishedInstances("deployment_id", d, time.Now().Add(24*time.Hour), 10)
			if err == nil && deleted != 0 {
				err = errors.New("an instance with modules was removed by retention")
			}
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
	})
}

func TestNewRunRemovesModulesAndMaintenanceProcedures(t *testing.T) {
	run(t, func(t *testing.T, h *harness) {
		h.e.RegisterTaskType("creating", &creating{})
		d := h.deploy(moduleProcess())
		parent := h.start(d, nil)
		h.process()
		child, _, err := h.e.StartInstance(ctx, StartRequest{DeploymentId: d, Owner: "user", ParentId: parent.Id})
		if err != nil {
			t.Fatal(err)
		}
		h.process()
		modules := h.modules(parent.Id)
		if len(modules) != 2 || !slices.ContainsFunc(modules, func(m model.Module) bool { return m.CreatedBy == child.Id }) {
			t.Fatalf("modules of a maintenance procedure belong to its parent (SMS-13): %+v", modules)
		}
		if len(h.modules(child.Id)) != 0 || h.vars(child.Id)["created"] != nil || h.vars(parent.Id)["created"] == nil {
			t.Fatalf("variables of modules are shared with the parent: child %v parent %v", h.vars(child.Id), h.vars(parent.Id))
		}
		if _, err = h.e.RestartInstance(ctx, parent.Id, 0, model.CauseRestart); err != nil {
			t.Fatal(err)
		}
		removals := h.removals()
		if len(removals) != 2 || removals[0].DeleteInfo.Url == "" {
			t.Fatalf("the resources of the previous run are queued for removal (SMS-03): %+v", removals)
		}
		if _, _, err = h.e.StartInstance(ctx, StartRequest{DeploymentId: d, Owner: "user", InstanceId: child.Id, ParentId: parent.Id}); err != nil {
			t.Fatalf("the maintenance procedure was removed with the old run, its id is free: %v", err)
		}
	})
}

func TestEffectsOfAnUnappliedResultAreUndone(t *testing.T) {
	run(t, func(t *testing.T, h *harness) {
		task := &creating{}
		h.e.RegisterTaskType("creating", task)
		d := h.deploy(moduleProcess())
		inst := h.start(d, nil)
		task.before = func(Task) {
			if err := h.e.StopInstance(context.Background(), inst.Id, "stopped while the task ran"); err != nil {
				t.Error(err)
			}
		}
		h.process()
		if len(task.undone) != 1 || len(h.modules(inst.Id)) != 0 {
			t.Fatalf("a resource created for a stopped instance is undone: undone %v, modules %v", task.undone, h.modules(inst.Id))
		}
	})
}
