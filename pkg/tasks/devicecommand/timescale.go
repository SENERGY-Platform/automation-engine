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

package devicecommand

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
)

// Timescale reads the last values of event services from timescale-wrapper.
type Timescale interface {
	Query(token string, request []TimescaleRequest, timeout time.Duration) ([]TimescaleResponse, error)
}

type timescaleWrapper struct {
	url string
}

func NewTimescale(url string) Timescale {
	return &timescaleWrapper{url: url}
}

type timescaleQuery struct {
	DeviceId  string
	ServiceId string
	Limit     int
	Columns   []timescaleColumn
}

type timescaleColumn struct {
	Name string
}

// Query asks for the last row per device and service and answers in the order of the request;
// a value is nil without data
func (this *timescaleWrapper) Query(token string, request []TimescaleRequest, timeout time.Duration) ([]TimescaleResponse, error) {
	query := timescaleQueries(request)
	body, err := json.Marshal(query)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodPost, this.url+"/queries", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", token)
	resp, err := (&http.Client{Timeout: timeout}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		temp, _ := io.ReadAll(resp.Body)
		return nil, errors.New(strings.TrimSpace(string(temp)))
	}
	// [query][row][column + 1], the first column is the time
	var response [][][]any
	if err = json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return nil, err
	}
	return timescaleValues(request, query, response)
}

func timescaleQueries(request []TimescaleRequest) (result []timescaleQuery) {
	index := map[[2]string]int{}
	for _, r := range request {
		key := [2]string{r.Device.Id, r.Service.Id}
		i, ok := index[key]
		if !ok {
			i = len(result)
			index[key] = i
			result = append(result, timescaleQuery{DeviceId: r.Device.Id, ServiceId: r.Service.Id, Limit: 1})
		}
		result[i].Columns = append(result[i].Columns, timescaleColumn{Name: r.ColumnName})
	}
	return result
}

func timescaleValues(request []TimescaleRequest, query []timescaleQuery, response [][][]any) (result []TimescaleResponse, err error) {
	for _, r := range request {
		i, j := -1, -1
		for qi, q := range query {
			if q.DeviceId != r.Device.Id || q.ServiceId != r.Service.Id {
				continue
			}
			for ci, c := range q.Columns {
				if c.Name == r.ColumnName {
					i, j = qi, ci+1
				}
			}
		}
		if i < 0 {
			return result, errors.New("column name not found in timescale query")
		}
		if i >= len(response) {
			return result, errors.New("timescale: response has fewer queries than the request")
		}
		if len(response[i]) != 1 {
			result = append(result, TimescaleResponse{Value: nil})
			continue
		}
		if j >= len(response[i][0]) {
			return result, errors.New("timescale: response has fewer columns than the request")
		}
		result = append(result, TimescaleResponse{Value: response[i][0][j]})
	}
	return result, nil
}
