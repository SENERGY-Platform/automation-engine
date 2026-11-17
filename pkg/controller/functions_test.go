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

package controller

import (
	"context"
	"errors"
	"html"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	devicerepository "github.com/SENERGY-Platform/device-repository/v3/lib/client"
	devicerepomodel "github.com/SENERGY-Platform/device-repository/v3/lib/model"
	"github.com/SENERGY-Platform/models/go/models"
	"github.com/SENERGY-Platform/service-commons/pkg/jwt"

	b "github.com/SENERGY-Platform/automation-engine/pkg/bpmn/bpmntest"
	"github.com/SENERGY-Platform/automation-engine/pkg/engine"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/prepare"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
)

const (
	fnOn         = models.CONTROLLING_FUNCTION_PREFIX + "setOn"
	fnBrightness = models.CONTROLLING_FUNCTION_PREFIX + "setBrightness"
)

// the device command of the tests: on, and brightness as additional function
const withAdditional = `{"function":{"id":"` + fnOn + `"},"additional_functions":[{"name":"brightness","function":{"id":"` + fnBrightness + `"}}]}`
const withoutAdditional = `{"function":{"id":"` + fnOn + `"}}`

func service(id string, functions ...string) models.Service {
	result := models.Service{Id: id}
	for _, f := range functions {
		result.Inputs = append(result.Inputs, models.Content{ContentVariable: models.ContentVariable{Name: f, FunctionId: f}})
	}
	return result
}

// fitRepository answers the reads of the function check and counts them; any other method of
// the client is not expected
type fitRepository struct {
	devicerepository.Interface
	mux     sync.Mutex
	calls   int
	devices map[string]models.Device
	types   map[string]models.DeviceType
	groups  map[string]models.DeviceGroup
}

func (this *fitRepository) count() {
	this.mux.Lock()
	defer this.mux.Unlock()
	this.calls++
}

func (this *fitRepository) ReadDevice(_ context.Context, id string, _ string, _ devicerepomodel.AuthAction) (models.Device, error, int) {
	this.count()
	if d, ok := this.devices[id]; ok {
		return d, nil, http.StatusOK
	}
	return models.Device{}, errors.New("not found"), http.StatusNotFound
}

func (this *fitRepository) ReadDeviceType(_ context.Context, id string, _ string) (models.DeviceType, error, int) {
	this.count()
	if t, ok := this.types[id]; ok {
		return t, nil, http.StatusOK
	}
	return models.DeviceType{}, errors.New("not found"), http.StatusNotFound
}

func (this *fitRepository) ReadDeviceGroup(_ context.Context, id string, _ string, _ bool) (models.DeviceGroup, error, int) {
	this.count()
	if g, ok := this.groups[id]; ok {
		return g, nil, http.StatusOK
	}
	return models.DeviceGroup{}, errors.New("not found"), http.StatusNotFound
}

func (this *fitRepository) ListAspectNodes(_ context.Context, options devicerepomodel.AspectListOptions) ([]models.AspectNode, int64, error, int) {
	this.count()
	var result []models.AspectNode
	for _, id := range options.Ids {
		// device-repository leaves an unknown id out of the list
		if !strings.HasPrefix(id, "unknown") {
			result = append(result, models.AspectNode{Id: id})
		}
	}
	return result, int64(len(result)), nil, http.StatusOK
}

// newFitRepository has a lamp with a service for both functions and one for on only, a device
// type of a switch that only turns on, and groups of them
func newFitRepository() *fitRepository {
	return &fitRepository{
		devices: map[string]models.Device{
			"lamp":   {Id: "lamp", DeviceTypeId: "lamp-type"},
			"switch": {Id: "switch", DeviceTypeId: "switch-type"},
		},
		types: map[string]models.DeviceType{
			"lamp-type":   {Id: "lamp-type", Services: []models.Service{service("s-on", fnOn), service("s-both", fnOn, fnBrightness)}},
			"switch-type": {Id: "switch-type", Services: []models.Service{service("s-on", fnOn)}},
		},
		groups: map[string]models.DeviceGroup{
			"mixed":         {Id: "mixed", DeviceIds: []string{"switch", "lamp"}},
			"only-switches": {Id: "only-switches", DeviceIds: []string{"switch"}},
		},
	}
}

type testNamespaces struct{}

func (testNamespaces) Get(name string) (model.Namespace, bool) {
	return model.Namespace{Name: name}, true
}
func (testNamespaces) List() []model.Namespace { return nil }
func (testNamespaces) Put(context.Context, model.Namespace) (model.Namespace, bool, error) {
	return model.Namespace{}, false, nil
}
func (testNamespaces) Delete(context.Context, string) error { return nil }

type engineNamespaces struct{}

func (engineNamespaces) Get(name string) (model.Namespace, bool) {
	return model.Namespace{Name: name}, true
}

func deviceCommandXml(id string, payload string) string {
	return `<bpmn:serviceTask id="` + id + `" senergy:taskType="device_command"><bpmn:extensionElements><camunda:inputOutput><camunda:inputParameter name="payload">` + html.EscapeString(payload) + `</camunda:inputParameter></camunda:inputOutput></bpmn:extensionElements></bpmn:serviceTask>`
}

// planFit runs the validation of a deployment of one device command with the given selection
func planFit(t *testing.T, repo *fitRepository, admin bool, payload string, selection model.Selection) error {
	t.Helper()
	ctx := context.Background()
	s, err := store.Open(ctx, store.SQLite, "file:"+filepath.Join(t.TempDir(), "controller.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	e := engine.New(s, engine.DefaultConfig(), engineNamespaces{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	e.RegisterTaskType("device_command", engine.TaskTypeFunc(func(context.Context, engine.Task) (engine.TaskResult, error) {
		return engine.TaskResult{}, nil
	}))
	c := New(s, e, testNamespaces{}, nil, nil)
	c.SetPreparation(prepare.New(nil, prepare.Config{DeviceRepository: repo}), nil)
	user := jwt.Token{Token: "Bearer token", Sub: "user"}
	if admin {
		user.RealmAccess = map[string][]string{"roles": {"admin"}}
	}
	xml := b.Process(b.Start("start"), deviceCommandXml("task", payload), b.Flow("start", "task"))
	_, err = c.planSelections(ctx, user, model.Namespace{Name: "processes"}, model.DeploymentInput{Xml: xml, Selections: map[string]model.Selection{"task": selection}}, model.Deployment{Id: "d"}, 1)
	return err
}

// issuesOf is the issues of a rejected deployment; nil for an accepted one
func issuesOf(t *testing.T, err error) []model.Issue {
	t.Helper()
	if err == nil {
		return nil
	}
	var modelErr *model.Error
	if !errors.As(err, &modelErr) || modelErr.Kind != model.KindValidation {
		t.Fatalf("expected a validation error, got %v", err)
	}
	return modelErr.Issues
}

func expectIssue(t *testing.T, err error, message string) {
	t.Helper()
	issues := issuesOf(t, err)
	if len(issues) != 1 || issues[0].ElementId != "task" || issues[0].Message != message {
		t.Errorf("expected the issue %q for task, got %+v (%v)", message, issues, err)
	}
}

func TestDeployRejectsADeviceWhoseServiceLacksAFunction(t *testing.T) {
	err := planFit(t, newFitRepository(), false, withAdditional, model.Selection{DeviceId: "lamp", ServiceId: "s-on"})
	expectIssue(t, err, "service s-on of device lamp does not serve brightness ("+fnBrightness+")")
}

func TestDeployAcceptsADeviceWhoseServiceServesAllFunctions(t *testing.T) {
	if err := planFit(t, newFitRepository(), false, withAdditional, model.Selection{DeviceId: "lamp", ServiceId: "s-both"}); err != nil {
		t.Error(err)
	}
}

func TestDeployRejectsADeviceWithoutAServiceThatServesAllFunctions(t *testing.T) {
	err := planFit(t, newFitRepository(), false, withAdditional, model.Selection{DeviceId: "switch"})
	expectIssue(t, err, "no service of device switch serves all of "+fnOn+", brightness ("+fnBrightness+")")
}

func TestDeployAcceptsADeviceWithOneServiceThatServesAllFunctions(t *testing.T) {
	if err := planFit(t, newFitRepository(), false, withAdditional, model.Selection{DeviceId: "lamp"}); err != nil {
		t.Error(err)
	}
}

func TestDeployRejectsAGroupWithoutAMemberThatServesAllFunctions(t *testing.T) {
	err := planFit(t, newFitRepository(), false, withAdditional, model.Selection{DeviceGroupId: "only-switches"})
	expectIssue(t, err, "no device of group only-switches has a service that serves all of "+fnOn+", brightness ("+fnBrightness+")")
}

func TestDeployAcceptsAGroupWithOneMemberThatServesAllFunctions(t *testing.T) {
	if err := planFit(t, newFitRepository(), false, withAdditional, model.Selection{DeviceGroupId: "mixed"}); err != nil {
		t.Error(err)
	}
}

func TestDeployChecksTheFunctionsForAnAdminToo(t *testing.T) {
	err := planFit(t, newFitRepository(), true, withAdditional, model.Selection{DeviceId: "switch"})
	expectIssue(t, err, "no service of device switch serves all of "+fnOn+", brightness ("+fnBrightness+")")
}

func TestDeployWithoutAdditionalFunctionsAsksDeviceRepositoryNothing(t *testing.T) {
	repo := newFitRepository()
	if err := planFit(t, repo, false, withoutAdditional, model.Selection{DeviceId: "switch"}); err != nil {
		t.Fatal(err)
	}
	if repo.calls != 0 {
		t.Errorf("device-repository was asked %v times", repo.calls)
	}
}

// the aspects of the functions are read as nodes, because a service serves an aspect by itself
// or a descendant
func TestDeployChecksTheAspectsOfTheAdditionalFunctions(t *testing.T) {
	repo := newFitRepository()
	repo.types["lamp-type"] = models.DeviceType{Id: "lamp-type", Services: []models.Service{
		{Id: "s-dark", Inputs: []models.Content{
			{ContentVariable: models.ContentVariable{Name: "on", FunctionId: fnOn}},
			{ContentVariable: models.ContentVariable{Name: "brightness", FunctionId: fnBrightness, AspectId: "dark"}},
		}},
	}}
	payload := `{"function":{"id":"` + fnOn + `"},"additional_functions":[{"name":"brightness","function":{"id":"` + fnBrightness + `"},"aspects":[{"id":"light"}]}]}`
	err := planFit(t, repo, false, payload, model.Selection{DeviceId: "lamp", ServiceId: "s-dark"})
	expectIssue(t, err, "service s-dark of device lamp does not serve brightness ("+fnBrightness+")")
}

func TestDeployRejectsAnAdditionalFunctionWithoutAName(t *testing.T) {
	payload := `{"function":{"id":"` + fnOn + `"},"additional_functions":[{"function":{"id":"` + fnBrightness + `"}}]}`
	err := planFit(t, newFitRepository(), false, payload, model.Selection{DeviceId: "lamp"})
	expectIssue(t, err, "additional function 1 needs a name")
}

func TestDeployRejectsAnAspectDeviceRepositoryDoesNotKnow(t *testing.T) {
	payload := `{"function":{"id":"` + fnOn + `"},"additional_functions":[{"name":"brightness","function":{"id":"` + fnBrightness + `"},"aspects":[{"id":"unknown-aspect"}]}]}`
	err := planFit(t, newFitRepository(), false, payload, model.Selection{DeviceId: "lamp"})
	expectIssue(t, err, "unknown aspect nodes: unknown-aspect")
}
