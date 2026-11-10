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

package deprecated_test

import (
	"html"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"

	b "github.com/SENERGY-Platform/automation-engine/pkg/bpmn/bpmntest"
	"github.com/SENERGY-Platform/automation-engine/pkg/deprecated"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
)

// infoDesign is a design of the old designer with one info module per entry of moduleTypes;
// a type of the form type#key gives the module a key
func infoDesign(moduleTypes ...string) string {
	parts := []string{b.StartWithForm("start", "room=kitchen"), b.End("end")}
	previous := "start"
	for i, entry := range moduleTypes {
		moduleType, key, _ := strings.Cut(entry, "#")
		id := "info" + string(rune('a'+i))
		inputs := `<camunda:inputParameter name="info.module_type">` + moduleType + `</camunda:inputParameter>` +
			`<camunda:inputParameter name="info.module_data">{"room":"{{.room}}"}</camunda:inputParameter>`
		if key != "" {
			inputs += `<camunda:inputParameter name="info.key">` + key + `</camunda:inputParameter>`
		}
		parts = append(parts, `<bpmn:serviceTask id="`+id+`" camunda:type="external" camunda:topic="info"><bpmn:extensionElements><camunda:inputOutput>`+
			inputs+`</camunda:inputOutput></bpmn:extensionElements></bpmn:serviceTask>`, b.Flow(previous, id))
		previous = id
	}
	parts = append(parts, b.Flow(previous, "end"))
	return b.Process(parts...)
}

func (this *env) createInstance(token string, releaseId string, name string) deprecated.SmartServiceInstance {
	this.t.Helper()
	var instance deprecated.SmartServiceInstance
	if status, _ := this.call(token, http.MethodPost, "/smart-services/releases/"+url.PathEscape(releaseId)+"/instances", map[string]any{"name": name}, &instance); status != http.StatusOK {
		this.t.Fatalf("instance %v: %v", name, status)
	}
	return instance
}

func (this *env) waitReady(token string, instanceId string) {
	this.t.Helper()
	this.waitFor("the ready instance "+instanceId, func() bool {
		var instance deprecated.SmartServiceInstance
		this.call(token, http.MethodGet, "/smart-services/instances/"+url.PathEscape(instanceId), nil, &instance)
		return instance.Ready
	})
}

func instanceNames(instances []deprecated.SmartServiceInstance) []string {
	result := []string{}
	for _, i := range instances {
		result = append(result, i.Name)
	}
	return result
}

// The list of smart service instances is sorted by name, ascending unless asked otherwise,
// pages with limit and offset and leaves out a deleted instance.
func TestSmartServiceInstancesAreListedSortedAndPaged(t *testing.T) {
	e := newEnv(t)
	release := e.createRelease(alice, e.createDesign(alice, "Room info", infoDesign("widget")).Id, "release name")
	deleted := e.createInstance(alice, release.Id, "deleted")
	names := []string{"a", "b", "c", "d", "e", "f"}
	for _, name := range names {
		e.createInstance(alice, release.Id, name)
	}
	if status, _ := e.call(alice, http.MethodDelete, "/smart-services/instances/"+url.PathEscape(deleted.Id), nil, nil); status != http.StatusOK {
		t.Fatalf("delete: %v", status)
	}
	reversed := slices.Clone(names)
	slices.Reverse(reversed)
	for query, expected := range map[string][]string{
		"":                  names,
		"?sort=name.asc":    names,
		"?sort=name.desc":   reversed,
		"?limit=2&offset=1": names[1:3],
	} {
		var instances []deprecated.SmartServiceInstance
		if status, _ := e.call(alice, http.MethodGet, "/smart-services/instances"+query, nil, &instances); status != http.StatusOK || !slices.Equal(instanceNames(instances), expected) {
			t.Errorf("instances%v: %v %v, want %v", query, status, instanceNames(instances), expected)
		}
	}
}

// moduleSetup runs two instances: a with the modules widget and tile (with the key modulekey),
// b with a widget only
func moduleSetup(t *testing.T) (e *env, a deprecated.SmartServiceInstance, b deprecated.SmartServiceInstance) {
	e = newEnv(t)
	first := e.createRelease(alice, e.createDesign(alice, "Two modules", infoDesign("widget", "tile#modulekey")).Id, "first")
	second := e.createRelease(alice, e.createDesign(alice, "One module", infoDesign("widget")).Id, "second")
	a, b = e.createInstance(alice, first.Id, "a"), e.createInstance(alice, second.Id, "b")
	e.waitReady(alice, a.Id)
	e.waitReady(alice, b.Id)
	return e, a, b
}

type moduleOf struct{ instance, moduleType string }

func modulesOf(t *testing.T, e *env, query string) []moduleOf {
	t.Helper()
	var modules []deprecated.SmartServiceModule
	if status, _ := e.call(alice, http.MethodGet, "/smart-services/modules"+query, nil, &modules); status != http.StatusOK {
		t.Fatalf("modules%v: %v", query, status)
	}
	result := []moduleOf{}
	for _, m := range modules {
		result = append(result, moduleOf{m.InstanceId, m.ModuleType})
	}
	return sorted(result)
}

// sorted orders modules by instance and type, as the list has no defined order
func sorted(modules []moduleOf) []moduleOf {
	slices.SortFunc(modules, func(x, y moduleOf) int {
		return strings.Compare(x.instance+" "+x.moduleType, y.instance+" "+y.moduleType)
	})
	return modules
}

// The modules of all instances are listed together, per instance or per module type.
func TestModulesAreListedPerInstanceAndPerType(t *testing.T) {
	e, a, b := moduleSetup(t)
	expected := map[string][]moduleOf{
		"":                                   {{a.Id, "tile"}, {a.Id, "widget"}, {b.Id, "widget"}},
		"?instance_id=" + a.Id:               {{a.Id, "tile"}, {a.Id, "widget"}},
		"?instance_id=" + b.Id:               {{b.Id, "widget"}},
		"?module_type=tile":                  {{a.Id, "tile"}},
		"?module_type=widget":                {{a.Id, "widget"}, {b.Id, "widget"}},
		"?instance_ids=" + a.Id + "," + b.Id: {{a.Id, "tile"}, {a.Id, "widget"}, {b.Id, "widget"}},
	}
	for query, want := range expected {
		if got := modulesOf(t, e, query); !slices.Equal(got, sorted(want)) {
			t.Errorf("modules%v: %v, want %v", query, got, want)
		}
	}
}

// A module with a key is found by it, also together with its type.
func TestModulesAreFoundByKey(t *testing.T) {
	e, a, _ := moduleSetup(t)
	for _, query := range []string{"?key=modulekey", "?key=modulekey&module_type=tile", "?key=modulekey&instance_id=" + a.Id} {
		if got, want := modulesOf(t, e, query), []moduleOf{{a.Id, "tile"}}; !slices.Equal(got, want) {
			t.Errorf("modules%v: %v, want %v", query, got, want)
		}
	}
	if got := modulesOf(t, e, "?key=modulekey&module_type=widget"); len(got) != 0 {
		t.Errorf("key and type together: %v", got)
	}
}

// labelledDesign asks for a device, a list of devices, a mode with options and a free room
// without label
func labelledDesign() string {
	return labelledDesignWith("Device")
}

func labelledDesignWith(deviceLabel string) string {
	property := func(id string, value string) string {
		return `<camunda:property id="` + id + `" value="` + html.EscapeString(value) + `"/>`
	}
	return b.Process(
		`<bpmn:startEvent id="start"><bpmn:extensionElements><camunda:formData>`+
			`<camunda:formField id="device" label="`+deviceLabel+`" type="string"><camunda:properties>`+property("iot", "device")+`</camunda:properties></camunda:formField>`+
			`<camunda:formField id="devices" label="Devices" type="string"><camunda:properties>`+property("iot", "device")+property("multiple", "true")+`</camunda:properties></camunda:formField>`+
			`<camunda:formField id="mode" label="Mode" type="string"><camunda:properties>`+property("options", `{"Eco":"eco","Comfort":"comfort"}`)+`</camunda:properties></camunda:formField>`+
			`<camunda:formField id="room" type="string"/>`+
			`</camunda:formData></bpmn:extensionElements></bpmn:startEvent>`,
		b.End("end"), b.Flow("start", "end"),
	)
}

// selection is the value of an IoT option, the JSON of the selection with the label it was
// chosen by, as device-selection offers it
func selection(deviceId string, label string) string {
	return `{"device_selection":{"device_id":"` + deviceId + `","service_id":null,"path":null},"label":"` + label + `"}`
}

type labels struct{ label, valueLabel string }

func parameterLabels(instance deprecated.SmartServiceInstance) map[string]labels {
	result := map[string]labels{}
	for _, p := range instance.Parameters {
		result[p.Id] = labels{p.Label, p.ValueLabel}
	}
	return result
}

// checkLabels compares the labels of the instance in the answer, alone and in the list
func (this *env) checkLabels(what string, answer deprecated.SmartServiceInstance, want map[string]labels) {
	this.t.Helper()
	var single deprecated.SmartServiceInstance
	this.call(alice, http.MethodGet, "/smart-services/instances/"+url.PathEscape(answer.Id), nil, &single)
	var list []deprecated.SmartServiceInstance
	this.call(alice, http.MethodGet, "/smart-services/instances", nil, &list)
	listed := deprecated.SmartServiceInstance{}
	for _, i := range list {
		if i.Id == answer.Id {
			listed = i
		}
	}
	for source, instance := range map[string]deprecated.SmartServiceInstance{"answer": answer, "instance": single, "list": listed} {
		if got := parameterLabels(instance); !maps.Equal(got, want) {
			this.t.Errorf("%v, %v: %v, want %v", what, source, got, want)
		}
	}
}

// An instance shows the title of each form field as label, the id without one, and the
// value_label the client sent with a value (SNRGY-5107).
func TestInstanceParametersShowTheFormLabelsAndTheValueLabelsSent(t *testing.T) {
	e := newEnv(t)
	release := e.createRelease(alice, e.createDesign(alice, "Labels", labelledDesign()).Id, "labels")
	var instance deprecated.SmartServiceInstance
	status, _ := e.call(alice, http.MethodPost, "/smart-services/releases/"+url.PathEscape(release.Id)+"/instances", map[string]any{"name": "Kitchen", "parameters": []map[string]any{
		{"id": "device", "value": selection("d1", "Lamp"), "label": "sent label", "value_label": "Kitchen lamp"},
		{"id": "mode", "value": "eco", "value_label": "Saving"},
		{"id": "room", "value": "kitchen"},
	}}, &instance)
	if status != http.StatusOK {
		t.Fatalf("instance: %v", status)
	}
	e.checkLabels("the labels sent", instance, map[string]labels{"device": {"Device", "Kitchen lamp"}, "mode": {"Mode", "Saving"}, "room": {"room", ""}})
}

// New parameters replace the value labels: a parameter sent without one shows the label of its
// option, as an instance of the time before the engine kept them; a restart keeps them.
func TestChangedParametersReplaceTheValueLabels(t *testing.T) {
	e := newEnv(t)
	release := e.createRelease(alice, e.createDesign(alice, "Labels", labelledDesign()).Id, "labels")
	var instance deprecated.SmartServiceInstance
	e.call(alice, http.MethodPost, "/smart-services/releases/"+url.PathEscape(release.Id)+"/instances", map[string]any{"name": "Kitchen", "parameters": []map[string]any{
		{"id": "device", "value": selection("d1", "Lamp"), "value_label": "Kitchen lamp"},
		{"id": "mode", "value": "eco", "value_label": "Saving"},
	}}, &instance)
	status, _ := e.call(alice, http.MethodPut, "/smart-services/instances/"+url.PathEscape(instance.Id)+"/parameters", []map[string]any{
		{"id": "device", "value": selection("d2", "Heater")},
		{"id": "mode", "value": "comfort", "value_label": "Warm"},
	}, &instance)
	if status != http.StatusOK {
		t.Fatalf("parameters: %v", status)
	}
	want := map[string]labels{"device": {"Device", "Heater"}, "mode": {"Mode", "Warm"}}
	e.checkLabels("new parameters", instance, want)
	restarted, err := e.srv.Controller.RestartInstance(ctx, alice, "smart-services", instance.Id, model.RestartInput{})
	if err != nil {
		t.Fatal(err)
	}
	if !maps.Equal(restarted.StartValueLabels, map[string]string{"mode": "Warm"}) {
		t.Errorf("a restart keeps the labels: %v", restarted.StartValueLabels)
	}
	e.checkLabels("after a restart", instance, want)
}

// An instance without value labels, as all instances taken over from smart-service-repository,
// shows the labels of the options its values hold: an IoT value carries the label it was chosen
// by, also each of a list, which a string form field keeps as JSON; a free value has none. No
// device-selection is asked (SNRGY-5107).
func TestValueLabelsOfInstancesWithoutThemAreTheLabelsOfTheirOptions(t *testing.T) {
	e := newEnv(t)
	release := e.createRelease(alice, e.createDesign(alice, "Labels", labelledDesign()).Id, "labels")
	var instance deprecated.SmartServiceInstance
	status, _ := e.call(alice, http.MethodPost, "/smart-services/releases/"+url.PathEscape(release.Id)+"/instances", map[string]any{"name": "Kitchen", "parameters": []map[string]any{
		{"id": "device", "value": selection("d1", "Lamp")},
		{"id": "devices", "value": []string{selection("d1", "Lamp"), selection("d2", "Heater")}},
		{"id": "mode", "value": "comfort"},
		{"id": "room", "value": "kitchen"},
	}}, &instance)
	if status != http.StatusOK {
		t.Fatalf("instance: %v", status)
	}
	if stored, err := e.srv.Controller.GetInstance(ctx, alice, "smart-services", instance.Id); err != nil || len(stored.StartValueLabels) != 0 {
		t.Fatalf("nothing stored: %v %v", stored.StartValueLabels, err)
	}
	e.checkLabels("derived", instance, map[string]labels{"device": {"Device", "Lamp"}, "devices": {"Devices", "Lamp, Heater"}, "mode": {"Mode", "Comfort"}, "room": {"room", ""}})

	var other deprecated.SmartServiceInstance
	e.call(alice, http.MethodPost, "/smart-services/releases/"+url.PathEscape(release.Id)+"/instances", map[string]any{"name": "Hall", "parameters": []map[string]any{
		{"id": "device", "value": `{"device_selection":{"device_id":"d1"}}`},
		{"id": "mode", "value": "turbo"},
	}}, &other)
	e.checkLabels("values without option", other, map[string]labels{"device": {"Device", ""}, "mode": {"Mode", ""}})
}

// The label is the title of the form field in the release the instance runs, also after a newer
// release renamed it, until its parameters move it there.
func TestInstanceParametersAreLabelledByTheReleaseTheInstanceRuns(t *testing.T) {
	e := newEnv(t)
	design := e.createDesign(alice, "Labels", labelledDesign())
	release := e.createRelease(alice, design.Id, "v1")
	var instance deprecated.SmartServiceInstance
	e.call(alice, http.MethodPost, "/smart-services/releases/"+url.PathEscape(release.Id)+"/instances", map[string]any{"name": "Kitchen", "parameters": []map[string]any{
		{"id": "device", "value": selection("d1", "Lamp")},
	}}, &instance)
	e.call(alice, http.MethodPut, "/smart-services/designs/"+url.PathEscape(design.Id), map[string]any{"name": "Labels", "bpmn_xml": labelledDesignWith("Switched device"), "svg_xml": "<svg/>"}, nil)
	e.createRelease(alice, design.Id, "v2")
	e.call(alice, http.MethodGet, "/smart-services/instances/"+url.PathEscape(instance.Id), nil, &instance)
	e.checkLabels("on the older release", instance, map[string]labels{"device": {"Device", "Lamp"}})
	if status, _ := e.call(alice, http.MethodPut, "/smart-services/instances/"+url.PathEscape(instance.Id)+"/parameters?release_id="+url.QueryEscape(release.Id), []map[string]any{
		{"id": "device", "value": selection("d1", "Lamp")},
	}, &instance); status != http.StatusOK {
		t.Fatalf("parameters: %v", status)
	}
	e.checkLabels("on the current release", instance, map[string]labels{"device": {"Switched device", "Lamp"}})
}
