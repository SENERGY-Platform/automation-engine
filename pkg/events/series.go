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

package events

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/tracing"
	"github.com/SENERGY-Platform/models/go/models"
)

// Row is a stored message of a device service: its time and its values by column.
type Row struct {
	Time   time.Time
	Values map[string]any
}

// Series reads stored device messages, the source of the reconstruction of event evaluations
// (EVT-10) and of the current value of a waiting event (DBG-18).
type Series interface {
	// Range returns rows after from and before to in ascending time, at most limit
	Range(ctx context.Context, token string, deviceId string, serviceId string, columns []string, from time.Time, to time.Time, limit int) ([]Row, error)
	// Last returns the newest row; nil if there is none
	Last(ctx context.Context, token string, deviceId string, serviceId string, columns []string) (*Row, error)
}

// Columns lists the columns timescale-tableworker creates for the outputs of a service: the
// names of the content variables joined by dots, lists of variable length as one column.
func Columns(service models.Service) []string {
	var result []string
	var walk func(c models.ContentVariable, prefix string)
	walk = func(c models.ContentVariable, prefix string) {
		name := c.Name
		if prefix != "" {
			name = prefix + "." + c.Name
		}
		switch c.Type {
		case models.Structure:
			for _, sub := range c.SubContentVariables {
				walk(sub, name)
			}
		case models.List:
			if len(c.SubContentVariables) > 0 && c.SubContentVariables[0].Name == "*" {
				result = append(result, name)
				return
			}
			for _, sub := range c.SubContentVariables {
				walk(sub, name)
			}
		default:
			result = append(result, name)
		}
	}
	for _, output := range service.Outputs {
		walk(output.ContentVariable, "")
	}
	return result
}

// MessageOf rebuilds the value of a device message from the columns of a row, the input of the
// evaluation. Lists with fixed entries come back as maps keyed by entry name.
func MessageOf(values map[string]any) map[string]any {
	result := map[string]any{}
	for column, value := range values {
		if value == nil {
			continue
		}
		parts := strings.Split(column, ".")
		current := result
		for _, part := range parts[:len(parts)-1] {
			next, ok := current[part].(map[string]any)
			if !ok {
				next = map[string]any{}
				current[part] = next
			}
			current = next
		}
		current[parts[len(parts)-1]] = value
	}
	return result
}

// ServiceOf reads the service stored in an event description.
func ServiceOf(d model.EventDescription) (service models.Service, err error) {
	if len(d.Service) == 0 {
		return service, fmt.Errorf("event description without service")
	}
	err = json.Unmarshal(d.Service, &service)
	return service, err
}

// Timescale reads rows through timescale-wrapper /queries with the token of the owner.
type Timescale struct {
	url  string
	http *http.Client
}

func NewTimescale(timescaleWrapperUrl string) *Timescale {
	return &Timescale{url: timescaleWrapperUrl, http: &http.Client{Timeout: 10 * time.Second}}
}

type queryTime struct {
	Start string `json:"start,omitempty"`
	End   string `json:"end,omitempty"`
}

type queryColumn struct {
	Name string `json:"name"`
}

type query struct {
	DeviceId         string        `json:"deviceId"`
	ServiceId        string        `json:"serviceId"`
	Time             *queryTime    `json:"time,omitempty"`
	Limit            int           `json:"limit"`
	Columns          []queryColumn `json:"columns"`
	OrderColumnIndex int           `json:"orderColumnIndex"`
	OrderDirection   string        `json:"orderDirection"`
}

func (this *Timescale) Range(ctx context.Context, token string, deviceId string, serviceId string, columns []string, from time.Time, to time.Time, limit int) ([]Row, error) {
	q := query{DeviceId: deviceId, ServiceId: serviceId, Limit: limit, OrderDirection: "asc",
		Time: &queryTime{Start: from.UTC().Format(time.RFC3339Nano), End: to.UTC().Format(time.RFC3339Nano)}}
	return this.query(ctx, token, q, columns)
}

func (this *Timescale) Last(ctx context.Context, token string, deviceId string, serviceId string, columns []string) (*Row, error) {
	rows, err := this.query(ctx, token, query{DeviceId: deviceId, ServiceId: serviceId, Limit: 1, OrderDirection: "desc"}, columns)
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	return &rows[0], nil
}

func (this *Timescale) query(ctx context.Context, token string, q query, columns []string) ([]Row, error) {
	if len(columns) == 0 {
		return nil, fmt.Errorf("the service has no output columns")
	}
	for _, c := range columns {
		q.Columns = append(q.Columns, queryColumn{Name: c})
	}
	body, err := json.Marshal([]query{q})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, this.url+"/queries", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", token)
	req.Header.Set("Content-Type", "application/json")
	if err = tracing.Inject(ctx, req); err != nil {
		return nil, err
	}
	resp, err := this.http.Do(req)
	if err != nil {
		return nil, model.Unavailable("timescale-wrapper: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, model.Unavailable("timescale-wrapper answered %v: %s", resp.Status, bytes.TrimSpace(raw))
	}
	// one table per query, rows of time followed by the columns
	var tables [][][]any
	if err = json.NewDecoder(resp.Body).Decode(&tables); err != nil {
		return nil, model.Unavailable("unable to read the answer of timescale-wrapper: %v", err)
	}
	var result []Row
	if len(tables) == 0 {
		return result, nil
	}
	for _, line := range tables[0] {
		if len(line) != len(columns)+1 {
			continue
		}
		raw, _ := line[0].(string)
		at, err := time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			continue
		}
		row := Row{Time: at, Values: map[string]any{}}
		for i, c := range columns {
			row.Values[c] = line[i+1]
		}
		result = append(result, row)
	}
	return result, nil
}
