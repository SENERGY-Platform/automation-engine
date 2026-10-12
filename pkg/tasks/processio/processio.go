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

// Package processio is the key-value store of processes (IO-01 to IO-03): the task type
// "process_io" and the store behind the IO API. The input conventions follow process-io-worker
// v1.0.13 (Apache-2.0): io.read.<output>, io.write.<key>, io.default.<key>, placeholders and
// JSON parsing of written strings.
package processio

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/engine"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
)

const TaskType = "process_io"

const (
	readPrefix    = "io.read."
	writePrefix   = "io.write."
	defaultPrefix = "io.default."
)

// Limits bound the values of a user that no instance holds (LONG-11).
type Limits struct {
	MaxValuesPerUser int
	MaxValueBytes    int
}

type Service struct {
	store  *store.Store
	limits Limits
	now    func() time.Time
}

func New(s *store.Store, limits Limits) *Service {
	return &Service{store: s, limits: limits, now: time.Now}
}

// Set writes a value of a user; a bound instance or deployment must belong to the user.
func (this *Service) Set(ctx context.Context, userId string, admin bool, v model.IoValue) error {
	return this.store.Tx(ctx, func(tx *store.Tx) error {
		return this.set(tx, userId, admin, v)
	})
}

func (this *Service) set(tx *store.Tx, userId string, admin bool, v model.IoValue) error {
	if strings.TrimSpace(v.Key) == "" {
		return model.BadRequest("key is required")
	}
	if strings.HasPrefix(v.Key, calculatePrefix) {
		return model.BadRequest("keys with prefix %v are computed and cannot be written", calculatePrefix)
	}
	raw, err := json.Marshal(v.Value)
	if err != nil {
		return model.BadRequest("value is not JSON: %v", err)
	}
	if this.limits.MaxValueBytes > 0 && len(raw) > this.limits.MaxValueBytes {
		return model.BadRequest("value has %v bytes, the limit is %v", len(raw), this.limits.MaxValueBytes)
	}
	if v.InstanceId != "" {
		inst, err := tx.GetInstance(v.InstanceId, false)
		if err != nil {
			return err
		}
		if !admin && inst.Owner != userId {
			return model.Forbidden("instance %v belongs to another user", v.InstanceId)
		}
	}
	if v.DeploymentId != "" {
		d, err := tx.GetDeployment(v.DeploymentId, false)
		if err != nil {
			return err
		}
		if !admin && d.Owner != userId && v.InstanceId == "" {
			return model.Forbidden("deployment %v belongs to another user", v.DeploymentId)
		}
	}
	if v.InstanceId == "" && this.limits.MaxValuesPerUser > 0 {
		_, exists, err := tx.GetIoValue(userId, v.Key)
		if err != nil {
			return err
		}
		n, err := tx.CountIoValues(userId)
		if err != nil {
			return err
		}
		if !exists && n >= this.limits.MaxValuesPerUser {
			return model.Conflict("user has %v values, the limit is %v", n, this.limits.MaxValuesPerUser)
		}
	}
	v.UpdatedAt = this.now()
	return tx.SetIoValue(userId, v)
}

// Get returns the value of a key, computed keys included; ok is false for a missing key.
func (this *Service) Get(ctx context.Context, userId string, key string) (result model.IoValue, ok bool, err error) {
	if value, computed, err := calculate(key, this.now()); computed {
		return model.IoValue{Key: key, Value: value, UpdatedAt: this.now()}, err == nil, err
	}
	err = this.store.Tx(ctx, func(tx *store.Tx) (err error) {
		result, ok, err = tx.GetIoValue(userId, key)
		return err
	})
	return result, ok, err
}

func (this *Service) Delete(ctx context.Context, userId string, key string) error {
	return this.store.Tx(ctx, func(tx *store.Tx) error {
		return tx.DeleteIoValue(userId, key)
	})
}

// List filters by deployment, instance and a regular expression on the key.
func (this *Service) List(ctx context.Context, userId string, options model.IoListOptions) (result model.Page[model.IoValue], err error) {
	var pattern *regexp.Regexp
	if options.KeyRegex != "" {
		if pattern, err = regexp.Compile(options.KeyRegex); err != nil {
			return result, model.BadRequest("invalid key_regex: %v", err)
		}
	}
	var all []model.IoValue
	err = this.store.Tx(ctx, func(tx *store.Tx) (err error) {
		all, err = tx.ListIoValues(userId, options.DeploymentId, options.InstanceId, options.Sort)
		return err
	})
	if err != nil {
		return result, err
	}
	filtered := []model.IoValue{}
	for _, v := range all {
		if pattern == nil || pattern.MatchString(v.Key) {
			filtered = append(filtered, v)
		}
	}
	page := options.PageOptions.Normalized()
	result = model.Page[model.IoValue]{Items: []model.IoValue{}, Total: len(filtered), Limit: page.Limit, Offset: page.Offset}
	if page.Offset < len(filtered) {
		result.Items = filtered[page.Offset:min(page.Offset+page.Limit, len(filtered))]
	}
	return result, nil
}

// Bulk writes, then reads, so that a key written in the same call reads back the new value.
func (this *Service) Bulk(ctx context.Context, userId string, admin bool, bulk model.IoBulk) (result []model.IoValue, err error) {
	err = this.store.Tx(ctx, func(tx *store.Tx) error {
		for _, v := range bulk.Set {
			if err := this.set(tx, userId, admin, v); err != nil {
				return err
			}
		}
		result = []model.IoValue{}
		for _, key := range bulk.Get {
			if value, computed, err := calculate(key, this.now()); computed {
				if err != nil {
					return err
				}
				result = append(result, model.IoValue{Key: key, Value: value})
				continue
			}
			v, _, err := tx.GetIoValue(userId, key)
			if err != nil {
				return err
			}
			v.Key = key
			result = append(result, v)
		}
		return nil
	})
	return result, err
}

// Type is the task type process_io.
type Type struct {
	service *Service
}

func NewType(service *Service) *Type {
	return &Type{service: service}
}

func resolve(key string, task engine.Task) string {
	key = strings.ReplaceAll(key, "{{InstanceId}}", task.InstanceId)
	key = strings.ReplaceAll(key, "{{DeploymentId}}", task.DeploymentId)
	// models from before the migration name the deployment by its old term (DATA-04)
	return strings.ReplaceAll(key, "{{DefinitionId}}", task.DeploymentId)
}

// parse reads strings as JSON where possible, so "5" becomes the number 5
func parse(value any) any {
	s, ok := value.(string)
	if !ok {
		return value
	}
	var parsed any
	if json.Unmarshal([]byte(s), &parsed) == nil {
		return parsed
	}
	return s
}

// Execute writes, then reads; read values become local variables named after the io.read.
// suffix. A key with {{InstanceId}} is bound to the instance, one with only {{DeploymentId}}
// to the deployment, so that the value is deleted with it (IO-01, IO-03).
func (this *Type) Execute(ctx context.Context, task engine.Task) (engine.TaskResult, error) {
	keys := make([]string, 0, len(task.Input))
	for key := range task.Input {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	bulk := model.IoBulk{}
	outputs := map[string]string{}
	defaults := map[string]any{}
	for _, name := range keys {
		value := task.Input[name]
		switch {
		case strings.HasPrefix(name, writePrefix):
			raw := strings.TrimPrefix(name, writePrefix)
			v := model.IoValue{Key: resolve(raw, task), Value: parse(value)}
			switch {
			case strings.Contains(raw, "{{InstanceId}}"):
				v.InstanceId, v.DeploymentId = task.InstanceId, task.DeploymentId
			case strings.Contains(raw, "{{DeploymentId}}") || strings.Contains(raw, "{{DefinitionId}}"):
				v.DeploymentId = task.DeploymentId
			}
			bulk.Set = append(bulk.Set, v)
		case strings.HasPrefix(name, readPrefix):
			rawKey, ok := value.(string)
			if !ok {
				return engine.TaskResult{}, &engine.PermanentError{Message: fmt.Sprintf("unable to interpret value of %v as string", name)}
			}
			outputs[strings.TrimPrefix(name, readPrefix)] = rawKey
			bulk.Get = append(bulk.Get, resolve(rawKey, task))
		case strings.HasPrefix(name, defaultPrefix):
			// the default is found by the key with unresolved placeholders, as before
			defaults[strings.TrimPrefix(name, defaultPrefix)] = parse(value)
		}
	}
	values, err := this.service.Bulk(ctx, task.Owner, false, bulk)
	if err != nil {
		return engine.TaskResult{}, err
	}
	byKey := map[string]any{}
	for _, v := range values {
		byKey[v.Key] = v.Value
	}
	output := map[string]any{}
	for name, rawKey := range outputs {
		value := byKey[resolve(rawKey, task)]
		if value == nil {
			value = defaults[rawKey]
		}
		output[name] = value
	}
	return engine.TaskResult{Output: output}, nil
}
