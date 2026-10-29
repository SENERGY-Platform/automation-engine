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
	"io"
	"log/slog"
	"maps"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/bpmn"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
	"github.com/SENERGY-Platform/automation-engine/pkg/store/storetest"
	"github.com/google/uuid"
)

var ctx = context.Background()

type staticNamespaces map[string]model.Namespace

func (this staticNamespaces) Get(name string) (model.Namespace, bool) {
	ns, ok := this[name]
	return ns, ok
}

type harness struct {
	t          *testing.T
	e          *Engine
	s          *store.Store
	mux        sync.Mutex
	clock      time.Time
	config     Config
	namespaces staticNamespaces
	notifier   *fakeNotifier
}

type fakeNotifier struct {
	mux  sync.Mutex
	sent []store.Notification
	fail bool
	// fails without answer, as a notifier behind a timeout does
	unreachable bool
	attempts    int
	// with hang, Notify reports on hanging and waits for ctx, as a call running into its timeout
	hang    bool
	hanging chan struct{}
}

func (this *fakeNotifier) Notify(ctx context.Context, n store.Notification) error {
	this.mux.Lock()
	if this.hang {
		this.attempts++
		this.mux.Unlock()
		this.hanging <- struct{}{}
		<-ctx.Done()
		return &NotifierUnreachableError{Err: ctx.Err()}
	}
	defer this.mux.Unlock()
	this.attempts++
	if this.unreachable {
		return &NotifierUnreachableError{Err: errors.New("context deadline exceeded")}
	}
	if this.fail {
		return errors.New("notifier unavailable")
	}
	this.sent = append(this.sent, n)
	return nil
}

func (this *fakeNotifier) attempted() int {
	this.mux.Lock()
	defer this.mux.Unlock()
	return this.attempts
}

func (this *fakeNotifier) messages() []store.Notification {
	this.mux.Lock()
	defer this.mux.Unlock()
	return slices.Clone(this.sent)
}

func newHarness(t *testing.T, s *store.Store, configure ...func(*Config)) *harness {
	config := DefaultConfig()
	config.Location = time.UTC
	config.TaskRetryBackoff = time.Second
	for _, c := range configure {
		c(&config)
	}
	h := &harness{t: t, s: s, clock: time.Date(2026, 10, 12, 10, 30, 0, 0, time.UTC), config: config, notifier: &fakeNotifier{}}
	h.namespaces = staticNamespaces{"processes": {Name: "processes"}, "restricted": {Name: "restricted", TaskTypes: model.ListRule{Deny: []string{"echo"}}}}
	h.e = New(s, config, h.namespaces, slog.New(slog.NewTextHandler(io.Discard, nil)))
	h.e.SetClock(h.now)
	h.e.SetNotifier(h.notifier)
	return h
}

// replica creates a second engine on the same store and clock
func (this *harness) replica() *Engine {
	e := New(this.s, this.config, this.e.namespaces, this.e.logger)
	e.SetClock(this.now)
	e.tasks = this.e.tasks
	return e
}

func (this *harness) now() time.Time {
	this.mux.Lock()
	defer this.mux.Unlock()
	return this.clock
}

// advance moves the clock and processes everything that became due
func (this *harness) advance(d time.Duration) {
	this.t.Helper()
	this.mux.Lock()
	this.clock = this.clock.Add(d)
	this.mux.Unlock()
	this.process()
}

func (this *harness) process() {
	this.t.Helper()
	if err := this.e.ProcessDue(ctx); err != nil {
		this.t.Fatal(err)
	}
	if err := this.e.ProcessNotifications(ctx); err != nil {
		this.t.Fatal(err)
	}
}

func (this *harness) deployIn(namespace string, xml string) string {
	this.t.Helper()
	return this.deployWith(namespace, xml, model.IncidentHandling{})
}

func (this *harness) deployWith(namespace string, xml string, handling model.IncidentHandling) string {
	this.t.Helper()
	if _, issues, err := bpmn.ParseAndValidate(xml, bpmn.Rules{}); err != nil || len(issues) > 0 {
		this.t.Fatalf("invalid test model: %v %v", err, issues)
	}
	id := uuid.NewString()
	err := this.s.Tx(ctx, func(tx *store.Tx) error {
		now := this.now()
		if err := tx.InsertDeployment(model.Deployment{Id: id, Namespace: namespace, Name: "test", Owner: "owner", Version: 1, CreatedAt: now, UpdatedAt: now}); err != nil {
			return err
		}
		if err := tx.InsertDeploymentVersion(model.DeploymentVersion{DeploymentId: id, Version: 1, Xml: xml, IncidentHandling: handling, CreatedAt: now}); err != nil {
			return err
		}
		return this.e.ActivateVersion(tx, id, 1)
	})
	if err != nil {
		this.t.Fatal(err)
	}
	return id
}

func (this *harness) deploy(xml string) string {
	this.t.Helper()
	return this.deployIn("processes", xml)
}

// update adds a version to a deployment and applies the strategy
func (this *harness) update(deploymentId string, xml string, strategy model.UpdateStrategy) {
	this.t.Helper()
	err := this.s.Tx(ctx, func(tx *store.Tx) error {
		d, err := tx.GetDeployment(deploymentId, true)
		if err != nil {
			return err
		}
		d.Version++
		if err = tx.InsertDeploymentVersion(model.DeploymentVersion{DeploymentId: deploymentId, Version: d.Version, Xml: xml, UpdateStrategy: strategy, CreatedAt: this.now()}); err != nil {
			return err
		}
		if err = tx.UpdateDeployment(d); err != nil {
			return err
		}
		if err = this.e.ActivateVersion(tx, deploymentId, d.Version); err != nil {
			return err
		}
		return this.e.ApplyUpdate(tx, deploymentId, strategy)
	})
	if err != nil {
		this.t.Fatal(err)
	}
}

func (this *harness) start(deploymentId string, variables map[string]any) model.Instance {
	this.t.Helper()
	inst, _, err := this.e.StartInstance(ctx, StartRequest{DeploymentId: deploymentId, Owner: "user", Variables: variables})
	if err != nil {
		this.t.Fatal(err)
	}
	return inst
}

func (this *harness) instance(id string) store.Instance {
	this.t.Helper()
	var result store.Instance
	err := this.s.Tx(ctx, func(tx *store.Tx) (err error) {
		result, err = tx.GetInstance(id, false)
		return err
	})
	if err != nil {
		this.t.Fatal(err)
	}
	return result
}

func (this *harness) vars(id string) map[string]any {
	this.t.Helper()
	result := map[string]any{}
	err := this.s.Tx(ctx, func(tx *store.Tx) error {
		vars, err := tx.GetVariables(id)
		for _, v := range vars {
			result[v.Name] = v.Value
		}
		return err
	})
	if err != nil {
		this.t.Fatal(err)
	}
	return result
}

func (this *harness) activities(id string, kind model.ActivityKind) []model.Activity {
	this.t.Helper()
	var result []model.Activity
	err := this.s.Tx(ctx, func(tx *store.Tx) (err error) {
		result, err = tx.ListActivities(id, model.ActivityListOptions{Kind: string(kind)})
		return err
	})
	if err != nil {
		this.t.Fatal(err)
	}
	return result
}

func (this *harness) incidents(instanceId string) []model.Incident {
	this.t.Helper()
	var result model.Page[model.Incident]
	err := this.s.Tx(ctx, func(tx *store.Tx) error {
		inst, err := tx.GetInstance(instanceId, false)
		if err != nil {
			return err
		}
		result, err = tx.ListIncidents(inst.Namespace, model.IncidentListOptions{InstanceId: instanceId}, store.Access{Admin: true})
		return err
	})
	if err != nil {
		this.t.Fatal(err)
	}
	return result.Items
}

type runtimeCounts struct{ tokens, subscriptions, jobs int }

func (this *harness) runtime(id string) (result runtimeCounts) {
	this.t.Helper()
	err := this.s.Tx(ctx, func(tx *store.Tx) error {
		tokens, err := tx.ListTokens(id)
		if err != nil {
			return err
		}
		subs, err := tx.ListSubscriptions(id)
		if err != nil {
			return err
		}
		result.tokens, result.subscriptions = len(tokens), len(subs)
		result.jobs, err = tx.CountJobs(id)
		return err
	})
	if err != nil {
		this.t.Fatal(err)
	}
	return result
}

func (this *harness) correlate(input model.MessageInput) model.CorrelationResult {
	this.t.Helper()
	result, err := this.e.Correlate(ctx, CorrelationRequest{MessageInput: input})
	if err != nil {
		this.t.Fatal(err)
	}
	return result
}

func (this *harness) expectState(id string, state model.InstanceState) store.Instance {
	this.t.Helper()
	inst := this.instance(id)
	if inst.State != state {
		this.t.Fatalf("expected state %v, got %v (%v); incidents %+v", state, inst.State, inst.EndReason, this.incidents(id))
	}
	return inst
}

func (this *harness) expectVars(id string, expected map[string]any) {
	this.t.Helper()
	got := this.vars(id)
	for name, value := range expected {
		if !maps.EqualFunc(map[string]any{name: got[name]}, map[string]any{name: value}, func(a, b any) bool { return sameJson(map[string]any{"v": a}, map[string]any{"v": b}) }) {
			this.t.Errorf("variable %v: expected %#v, got %#v", name, value, got[name])
		}
	}
}

func run(t *testing.T, fn func(t *testing.T, h *harness)) {
	storetest.Run(t, func(t *testing.T, s *store.Store) {
		fn(t, newHarness(t, s))
	})
}

type storeTx = store.Tx

var adminAccess = store.Access{Admin: true}
