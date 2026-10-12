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

	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/script"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
)

// CorrelationRequest correlates a message with waiting catch events and message start events.
type CorrelationRequest struct {
	model.MessageInput
	// restricts correlation to resources of this owner; empty for all (admins)
	OwnerFilter string
}

// Correlate delivers a message by name (EVT-01, EVT-02). Without All exactly one target is
// allowed. A message that finds no target is not an error (EVT-08). With a message id, each
// target receives the message at most once (EVT-05).
func (this *Engine) Correlate(ctx context.Context, request CorrelationRequest) (result model.CorrelationResult, err error) {
	result.Correlations = []model.Correlation{}
	if request.Name == "" {
		return result, model.BadRequest("message name is required")
	}
	variables, err := normalizeAll(request.Variables, this.config.MaxVariableBytes)
	if err != nil {
		return result, err
	}
	locals, err := normalizeAll(request.LocalVariables, this.config.MaxVariableBytes)
	if err != nil {
		return result, err
	}
	var subscriptions []store.Subscription
	var starts []store.StartEvent
	err = this.store.Tx(ctx, func(tx *store.Tx) (err error) {
		subscriptions, err = tx.MessageSubscriptions(request.Name, request.InstanceId, request.OwnerFilter)
		if err != nil {
			return err
		}
		if request.InstanceId == "" || len(subscriptions) == 0 {
			starts, err = tx.MessageStartEvents(request.Name, request.OwnerFilter)
		}
		return err
	})
	if err != nil {
		return result, err
	}
	// group subscriptions per instance; an instance receives the message once per correlation
	var instanceIds []string
	perInstance := map[string][]store.Subscription{}
	for _, s := range subscriptions {
		if _, ok := perInstance[s.InstanceId]; !ok {
			instanceIds = append(instanceIds, s.InstanceId)
		}
		perInstance[s.InstanceId] = append(perInstance[s.InstanceId], s)
	}
	if !request.All {
		switch {
		case len(instanceIds)+len(starts) > 1:
			return result, model.Conflict("message %v matches %v targets, correlate with all or name an instance", request.Name, len(instanceIds)+len(starts))
		case len(instanceIds) == 1:
			// a single waiting event; with several waiting events of one instance only the first
			perInstance[instanceIds[0]] = perInstance[instanceIds[0]][:1]
		}
	}
	for _, instanceId := range instanceIds {
		correlations, err := this.correlateInstance(ctx, instanceId, perInstance[instanceId], request, variables, locals)
		if err != nil {
			return result, err
		}
		result.Correlations = append(result.Correlations, correlations...)
	}
	for _, start := range starts {
		correlation, err := this.correlateStart(ctx, start, request, variables, locals)
		if err != nil {
			return result, err
		}
		if correlation != nil {
			result.Correlations = append(result.Correlations, *correlation)
		}
	}
	switch {
	case len(result.Correlations) == 0:
		this.metrics.Correlation("unmatched")
	case result.Correlations[0].Duplicate:
		this.metrics.Correlation("duplicate")
	default:
		this.metrics.Correlation("matched")
	}
	return result, nil
}

func normalizeAll(values map[string]any, maxBytes int) (map[string]any, error) {
	result := map[string]any{}
	for name, value := range values {
		normalized, err := script.Normalize(value, maxBytes)
		if err != nil {
			return nil, model.BadRequest("variable %v: %v", name, err)
		}
		result[name] = normalized
	}
	return result, nil
}

func (this *Engine) correlateInstance(ctx context.Context, instanceId string, subscriptions []store.Subscription, request CorrelationRequest, variables, locals map[string]any) (result []model.Correlation, err error) {
	err = this.store.Tx(ctx, func(tx *store.Tx) error {
		result = nil
		inst, err := tx.GetInstance(instanceId, true)
		if err != nil {
			return ignoreNotFound(err)
		}
		if inst.State != model.StateRunning {
			return nil
		}
		if request.MessageId != "" {
			isNew, err := tx.InsertReceipt(request.MessageId, instanceId, this.now())
			if err != nil {
				return err
			}
			if !isNew {
				result = append(result, model.Correlation{InstanceId: instanceId, ElementId: subscriptions[0].ElementId, Duplicate: true})
				return nil
			}
		}
		r, err := this.newRunner(tx, &inst)
		if err != nil {
			return err
		}
		for _, candidate := range subscriptions {
			// re-read under the instance lock; it may have been consumed meanwhile
			s, err := tx.GetSubscription(candidate.Id)
			if err != nil {
				return err
			}
			if s == nil || r.halted {
				continue
			}
			data := map[string]any{"message_name": request.Name}
			if request.MessageId != "" {
				data["message_id"] = request.MessageId
			}
			if len(locals) > 0 {
				data["local_variables"] = r.preview(locals)
			}
			if err = r.trigger(*s, locals, variables, model.ActivityMessageReceived, data); err != nil {
				if err = r.handle(err); err != nil {
					return err
				}
			}
			result = append(result, model.Correlation{InstanceId: instanceId, ElementId: s.ElementId})
		}
		return r.finish()
	})
	return result, err
}

func (this *Engine) correlateStart(ctx context.Context, start store.StartEvent, request CorrelationRequest, variables, locals map[string]any) (result *model.Correlation, err error) {
	err = this.store.Tx(ctx, func(tx *store.Tx) error {
		result = nil
		// the start event may have been replaced by an update meanwhile
		current, err := tx.GetStartEvent(start.DeploymentId, start.ElementId)
		if err != nil {
			return ignoreNotFound(err)
		}
		if request.MessageId != "" {
			target := "start:" + start.DeploymentId + "/" + start.ElementId
			if request.InstanceId != "" {
				target = request.InstanceId
			}
			isNew, err := tx.InsertReceipt(request.MessageId, target, this.now())
			if err != nil {
				return err
			}
			if !isNew {
				result = &model.Correlation{InstanceId: request.InstanceId, ElementId: start.ElementId, Duplicate: true}
				return nil
			}
		}
		inst, created, err := this.startInstance(tx, StartRequest{
			DeploymentId: start.DeploymentId, DeploymentVersion: current.Version, InstanceId: request.InstanceId,
			Owner: start.Owner, Variables: variables, StartElementId: start.ElementId, Locals: locals,
		}, model.CauseStart)
		if errors.Is(err, model.ErrConflict) {
			return model.Conflict("instance %v already exists", request.InstanceId)
		}
		if err != nil {
			return err
		}
		result = &model.Correlation{InstanceId: inst.Id, ElementId: start.ElementId, Started: created, Duplicate: !created}
		return nil
	})
	return result, err
}
