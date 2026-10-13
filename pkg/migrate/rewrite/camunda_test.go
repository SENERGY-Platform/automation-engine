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

package rewrite

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/script"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// variable is a typed Camunda variable; the engine sees the JSON value
type variable struct {
	Type  string
	Value any
}

func (this variable) engine() any {
	switch v := this.Value.(type) {
	case int:
		return float64(v)
	default:
		return v
	}
}

type comparison struct {
	name string
	// a JUEL condition or a mapping text, as in the exported models
	condition, mapping string
	variables          map[string]variable
	// why Camunda and the engine differ, for differences that are accepted and documented
	differs string
}

func vars(pairs ...any) map[string]variable {
	result := map[string]variable{}
	for i := 0; i+1 < len(pairs); i += 2 {
		v := pairs[i+1]
		var t string
		switch v.(type) {
		case nil:
			t = "Null"
		case int:
			t = "Integer"
		case float64:
			t = "Double"
		case string:
			t = "String"
		case bool:
			t = "Boolean"
		}
		result[pairs[i].(string)] = variable{Type: t, Value: v}
	}
	return result
}

// The cases cover the conditions and mappings of the exported models with the variable types a
// process sees: numbers from devices, strings, booleans and null (MIG-05).
var comparisons = []comparison{
	{name: "greater", condition: "${outputs > 1000}", variables: vars("outputs", 1500)},
	{name: "greater false", condition: "${outputs > 1000}", variables: vars("outputs", 500)},
	{name: "greater double", condition: "${outputs > 1000}", variables: vars("outputs", 1000.5)},
	{name: "greater numeric string", condition: "${outputs > 1000}", variables: vars("outputs", "1500")},
	{name: "range", condition: "${outputs >300 && outputs <=350}", variables: vars("outputs", 320)},
	{name: "range edge", condition: "${outputs >300 && outputs <=350}", variables: vars("outputs", 350.0)},
	{name: "less or equal", condition: "${power <= standby_power}", variables: vars("power", 3, "standby_power", 3.0)},
	{name: "equal true", condition: "${anyBelowLimit == true}", variables: vars("anyBelowLimit", true)},
	{name: "equal false", condition: "${anyBelowLimit == false}", variables: vars("anyBelowLimit", false)},
	{name: "boolean as string", condition: "${anyBelowLimit == false}", variables: vars("anyBelowLimit", "false"),
		differs: "JUEL turns the string \"false\" into a boolean for ==, JavaScript compares string and boolean as numbers"},
	{name: "boolean variable", condition: "${was_active}", variables: vars("was_active", true)},
	{name: "negation", condition: "${!was_active}", variables: vars("was_active", false)},
	{name: "integer and double", condition: "${distance_km < distance_toleration_km && current_request  != available_current}", variables: vars("distance_km", 2, "distance_toleration_km", 2.5, "current_request", 16, "available_current", 16.0)},
	{name: "mixed", condition: "${fan_on && delta_td > 1.0 && bath_rh > 65.0 && !stalled}", variables: vars("fan_on", true, "delta_td", 1.5, "bath_rh", 70, "stalled", false)},
	{name: "word operators", condition: "${a gt 1 and not (b eq 'x')}", variables: vars("a", 2, "b", "y")},
	{name: "string equality", condition: "${state == 'on'}", variables: vars("state", "on")},
	{name: "number and numeric string", condition: "${limit == 15}", variables: vars("limit", "15")},

	{name: "single expression keeps type", mapping: "${result}", variables: vars("result", 42)},
	{name: "single expression string", mapping: "${result}", variables: vars("result", "on")},
	{name: "single expression null", mapping: "${result}", variables: vars("result", nil)},
	{name: "composite", mapping: "PT${seconds}S", variables: vars("seconds", 30)},
	{name: "composite string", mapping: "http://${ip}/ta", variables: vars("ip", "10.0.0.2")},
	{name: "composite null", mapping: "value: ${x}", variables: vars("x", nil)},
	{name: "composite boolean", mapping: "On: ${on}", variables: vars("on", true)},
	{name: "composite double", mapping: "PT${seconds}S", variables: vars("seconds", 2.5)},
	{name: "composite whole double", mapping: "PT${seconds}S", variables: vars("seconds", 30.0),
		differs: "Java writes the double 30.0 as \"30.0\"; the engine holds JSON numbers and writes 30"},
}

func TestRewrittenExpressionsBehaveLikeCamunda(t *testing.T) {
	if os.Getenv("AE_TEST_CAMUNDA") == "off" {
		t.Skip("AE_TEST_CAMUNDA=off")
	}
	camunda := startCamunda(t)
	sandbox := script.NewSandbox()
	for _, c := range comparisons {
		t.Run(c.name, func(t *testing.T) {
			expected, camundaErr := camunda.evaluate(c)
			actual, engineErr := evaluateRewritten(sandbox, c)
			t.Logf("Camunda %v (%v), engine %v (%v)", expected, camundaErr, actual, engineErr)
			same := fmt.Sprint(expected) == fmt.Sprint(actual) && (camundaErr == nil) == (engineErr == nil)
			switch {
			case c.differs == "" && !same:
				t.Errorf("Camunda %v (%v), engine %v (%v)", expected, camundaErr, actual, engineErr)
			case c.differs != "" && same:
				t.Errorf("documented difference no longer holds (%v): both %v", c.differs, actual)
			}
		})
	}
}

// evaluateRewritten runs the rewritten expression the way the engine does
func evaluateRewritten(sandbox *script.Sandbox, c comparison) (any, error) {
	text := c.condition + c.mapping
	js, err := toScript(text)
	if c.condition != "" {
		js, err = toScript(strings.TrimSpace(text))
	}
	if err != nil {
		return nil, err
	}
	variables := map[string]any{}
	for name, v := range c.variables {
		variables[name] = v.engine()
	}
	result, err := sandbox.Run(js, variables)
	if err != nil {
		return nil, err
	}
	if c.condition != "" {
		return script.Truthy(result.Value)
	}
	return result.Value, nil
}

type camundaClient struct {
	url string
}

func startCamunda(t *testing.T) camundaClient {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	// the image of the platform; Camunda 7 Community Edition is Apache-2.0
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        "camunda/camunda-bpm-platform:tomcat-7.24.0",
			ExposedPorts: []string{"8080/tcp"},
			WaitingFor:   wait.ForHTTP("/engine-rest/engine").WithPort("8080/tcp").WithStartupTimeout(3 * time.Minute),
		},
		Started: true,
	})
	if err != nil {
		t.Skipf("no container runtime for Camunda: %v", err)
	}
	t.Cleanup(func() { _ = container.Terminate(context.Background()) })
	endpoint, err := container.PortEndpoint(ctx, "8080/tcp", "http")
	if err != nil {
		t.Fatal(err)
	}
	return camundaClient{url: endpoint + "/engine-rest"}
}

// evaluate deploys a process that takes a flow by the condition or maps the text into a
// variable, starts it and reads the result
func (this camundaClient) evaluate(c comparison) (any, error) {
	id := fmt.Sprintf("p%d", time.Now().UnixNano())
	var body string
	if c.condition != "" {
		body = `<bpmn:startEvent id="start"/><bpmn:exclusiveGateway id="gw" default="no"/>` +
			`<bpmn:endEvent id="end_yes"/><bpmn:endEvent id="end_no"/>` +
			`<bpmn:sequenceFlow id="f1" sourceRef="start" targetRef="gw"/>` +
			`<bpmn:sequenceFlow id="yes" sourceRef="gw" targetRef="end_yes"><bpmn:conditionExpression xsi:type="bpmn:tFormalExpression">` + html.EscapeString(c.condition) + `</bpmn:conditionExpression></bpmn:sequenceFlow>` +
			`<bpmn:sequenceFlow id="no" sourceRef="gw" targetRef="end_no"/>`
	} else {
		body = `<bpmn:startEvent id="start"/><bpmn:manualTask id="map"><bpmn:extensionElements><camunda:inputOutput>` +
			`<camunda:outputParameter name="result">` + html.EscapeString(c.mapping) + `</camunda:outputParameter>` +
			`</camunda:inputOutput></bpmn:extensionElements></bpmn:manualTask><bpmn:endEvent id="end"/>` +
			`<bpmn:sequenceFlow id="f1" sourceRef="start" targetRef="map"/><bpmn:sequenceFlow id="f2" sourceRef="map" targetRef="end"/>`
	}
	xml := `<?xml version="1.0" encoding="UTF-8"?><bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" ` +
		`xmlns:camunda="http://camunda.org/schema/1.0/bpmn" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" id="d" targetNamespace="t">` +
		`<bpmn:process id="` + id + `" isExecutable="true" camunda:historyTimeToLive="1">` + body + `</bpmn:process></bpmn:definitions>`
	if err := this.deploy(id, xml); err != nil {
		return nil, err
	}
	variables := map[string]any{}
	for name, v := range c.variables {
		variables[name] = map[string]any{"value": v.Value, "type": v.Type}
	}
	var instance struct {
		Id string `json:"id"`
	}
	if err := this.call(http.MethodPost, "/process-definition/key/"+id+"/start", map[string]any{"variables": variables}, &instance); err != nil {
		return nil, err
	}
	if c.condition != "" {
		var activities []struct {
			ActivityId string `json:"activityId"`
		}
		if err := this.call(http.MethodGet, "/history/activity-instance?processInstanceId="+instance.Id+"&activityType=noneEndEvent", nil, &activities); err != nil {
			return nil, err
		}
		if len(activities) != 1 {
			return nil, fmt.Errorf("no end reached: %v", activities)
		}
		return activities[0].ActivityId == "end_yes", nil
	}
	var values []struct {
		Value any `json:"value"`
	}
	if err := this.call(http.MethodGet, "/history/variable-instance?processInstanceId="+instance.Id+"&variableName=result", nil, &values); err != nil {
		return nil, err
	}
	if len(values) != 1 {
		return nil, fmt.Errorf("no result: %v", values)
	}
	if n, ok := values[0].Value.(json.Number); ok {
		f, _ := n.Float64()
		return f, nil
	}
	return values[0].Value, nil
}

func (this camundaClient) deploy(name string, xml string) error {
	var buf bytes.Buffer
	form := multipart.NewWriter(&buf)
	_ = form.WriteField("deployment-name", name)
	file, _ := form.CreateFormFile("data", name+".bpmn")
	_, _ = file.Write([]byte(xml))
	_ = form.Close()
	resp, err := http.Post(this.url+"/deployment/create", form.FormDataContentType(), &buf)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("deploy: %v %v", resp.StatusCode, string(raw))
	}
	return nil
}

func (this camundaClient) call(method string, path string, body any, result any) error {
	var reader io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, this.url+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return fmt.Errorf("%v %v: %v %v", method, path, resp.StatusCode, string(raw))
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	return decoder.Decode(result)
}
