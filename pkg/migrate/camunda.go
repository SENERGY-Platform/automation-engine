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

package migrate

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Camunda reads deployments and running instances through the REST API of Camunda engines,
// e.g. the shards of the process engine; the API needs basic auth.
type Camunda struct {
	// base URLs ending in /engine-rest
	Urls     []string
	User     string
	Password string
	Http     *http.Client
}

const camundaPage = 100

func (this *Camunda) get(ctx context.Context, base string, path string, query url.Values, result any) error {
	target := base + path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return err
	}
	if this.User != "" {
		req.SetBasicAuth(this.User, this.Password)
	}
	client := this.Http
	if client == nil {
		client = &http.Client{Timeout: time.Minute}
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("GET %v: %v %s", path, resp.StatusCode, body)
	}
	return json.NewDecoder(resp.Body).Decode(result)
}

func paging(first int) url.Values {
	return url.Values{"firstResult": {strconv.Itoa(first)}, "maxResults": {strconv.Itoa(camundaPage)}}
}

// camundaTime is the format of times in the REST API of Camunda
const camundaTime = "2006-01-02T15:04:05.000-0700"

// CamundaDeployments lists the deployments of every engine with their first process definition
// and its BPMN; deployments without definition, e.g. of forms only, are left out
func (this *Camunda) CamundaDeployments(ctx context.Context) ([]CamundaDeployment, error) {
	var result []CamundaDeployment
	for _, base := range this.Urls {
		for first := 0; ; first += camundaPage {
			var page []struct {
				Id, Name, Source, DeploymentTime, TenantId string
			}
			query := paging(first)
			query.Set("sortBy", "deploymentTime")
			query.Set("sortOrder", "asc")
			if err := this.get(ctx, base, "/deployment", query, &page); err != nil {
				return nil, err
			}
			for _, d := range page {
				var definitions []struct {
					Id, Key   string
					Suspended bool
				}
				if err := this.get(ctx, base, "/process-definition", url.Values{"deploymentId": {d.Id}}, &definitions); err != nil {
					return nil, err
				}
				if len(definitions) == 0 {
					continue
				}
				var xml struct {
					Bpmn20Xml string `json:"bpmn20Xml"`
				}
				if err := this.get(ctx, base, "/process-definition/"+url.PathEscape(definitions[0].Id)+"/xml", nil, &xml); err != nil {
					return nil, err
				}
				deployed, _ := time.Parse(camundaTime, d.DeploymentTime)
				result = append(result, CamundaDeployment{Engine: base, Id: d.Id, Name: d.Name, Source: d.Source, TenantId: d.TenantId, Time: deployed,
					DefinitionId: definitions[0].Id, Key: definitions[0].Key, Bpmn: xml.Bpmn20Xml, Suspended: definitions[0].Suspended})
			}
			if len(page) < camundaPage {
				break
			}
		}
	}
	return result, nil
}

// camundaValue reads a variable of Camunda: JSON and serialized objects keep their text
func camundaValue(variable struct {
	Type  string
	Value json.RawMessage
}) any {
	var value any
	_ = json.Unmarshal(variable.Value, &value)
	switch variable.Type {
	case "Json", "Object", "Xml":
		if text, ok := value.(string); ok {
			var parsed any
			if json.Unmarshal([]byte(text), &parsed) == nil {
				return parsed
			}
		}
	}
	return value
}

// RunningInstances reads the running instances of a deployment with their current variables
// and, from the history, their start time
func (this *Camunda) RunningInstances(ctx context.Context, d CamundaDeployment) ([]CamundaInstance, error) {
	var result []CamundaInstance
	for first := 0; ; first += camundaPage {
		var page []struct {
			Id, BusinessKey, TenantId, DefinitionId string
		}
		query := paging(first)
		query.Set("processDefinitionId", d.DefinitionId)
		if err := this.get(ctx, d.Engine, "/process-instance", query, &page); err != nil {
			return nil, err
		}
		ids := make([]string, 0, len(page))
		for _, i := range page {
			ids = append(ids, i.Id)
		}
		started, err := this.startTimes(ctx, d.Engine, ids)
		if err != nil {
			return nil, err
		}
		for _, i := range page {
			var variables map[string]struct {
				Type  string
				Value json.RawMessage
			}
			if err := this.get(ctx, d.Engine, "/process-instance/"+url.PathEscape(i.Id)+"/variables", url.Values{"deserializeValues": {"false"}}, &variables); err != nil {
				return nil, err
			}
			instance := CamundaInstance{Id: i.Id, BusinessKey: i.BusinessKey, TenantId: i.TenantId, DefinitionId: i.DefinitionId, Variables: map[string]any{}, StartTime: started[i.Id]}
			for name, v := range variables {
				instance.Variables[name] = camundaValue(v)
			}
			result = append(result, instance)
		}
		if len(page) < camundaPage {
			return result, nil
		}
	}
}

// startTimes reads the start times of instances from the history; an engine that keeps no
// history gives none
func (this *Camunda) startTimes(ctx context.Context, base string, ids []string) (map[string]time.Time, error) {
	result := map[string]time.Time{}
	if len(ids) == 0 {
		return result, nil
	}
	var history []struct{ Id, StartTime string }
	query := url.Values{"processInstanceIds": {strings.Join(ids, ",")}, "maxResults": {strconv.Itoa(len(ids))}}
	if err := this.get(ctx, base, "/history/process-instance", query, &history); err != nil {
		return nil, err
	}
	for _, h := range history {
		if t, err := time.Parse(camundaTime, h.StartTime); err == nil {
			result[h.Id] = t.UTC()
		}
	}
	return result, nil
}
