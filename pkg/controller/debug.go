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

	"github.com/SENERGY-Platform/automation-engine/pkg/debug"
	"github.com/SENERGY-Platform/automation-engine/pkg/fogsync/protocol"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
)

// DebugState returns the live state of an instance (DBG-02) with the read right on it or as
// admin (DBG-08); large values are cut and can be loaded with GetVariable (DBG-04).
func (this *Controller) DebugState(ctx context.Context, token string, namespace string, id string, options model.DebugOptions) (result model.DebugState, err error) {
	if networkId, inst, err := this.onGateway(ctx, token, namespace, id); err != nil || networkId != "" {
		if err == nil {
			err = this.networks.Call(ctx, networkId, protocol.MethodDebugState, namespace, id, requestOptions(options), &result)
			if errors.Is(err, model.ErrUnavailable) {
				return offlineState(inst), nil
			}
		}
		return result, err
	}
	err = this.store.Tx(ctx, func(tx *store.Tx) error {
		inst, err := this.readInstance(ctx, tx, token, namespace, id, Read)
		if err != nil {
			return err
		}
		result, err = this.engine.DebugState(tx, inst)
		return err
	})
	if err != nil {
		return result, err
	}
	if options.EvaluateEvents {
		this.evaluateEvents(ctx, &result)
	}
	return debug.Cut(result, this.engine.ActivityValueLimit()), nil
}

// HistoryState reconstructs the state of an instance after an activity entry (DBG-13).
func (this *Controller) HistoryState(ctx context.Context, token string, namespace string, id string, options model.HistoryOptions) (result model.DebugState, err error) {
	if networkId, inst, err := this.onGateway(ctx, token, namespace, id); err != nil || networkId != "" {
		if err == nil {
			err = this.networks.Call(ctx, networkId, protocol.MethodHistoryState, namespace, id, requestOptions(options), &result)
			if errors.Is(err, model.ErrUnavailable) {
				return offlineState(inst), nil
			}
		}
		return result, err
	}
	err = this.store.Tx(ctx, func(tx *store.Tx) error {
		result, err = this.historyState(ctx, tx, token, namespace, id, options)
		return err
	})
	return debug.Cut(result, this.engine.ActivityValueLimit()), err
}

func (this *Controller) historyState(ctx context.Context, tx *store.Tx, token string, namespace string, id string, options model.HistoryOptions) (model.DebugState, error) {
	inst, err := this.readInstance(ctx, tx, token, namespace, id, Read)
	if err != nil {
		return model.DebugState{}, err
	}
	seq := options.Seq
	if options.Time != nil {
		var found bool
		if seq, found, err = tx.ActivitySeqAt(id, *options.Time); err != nil {
			return model.DebugState{}, err
		}
		if !found {
			return model.DebugState{}, model.NotFound("instance %v has no step at or before %v", id, options.Time)
		}
	}
	return debug.At(tx, inst, seq)
}

// HistoryVariable returns the complete value of a variable at a step of the history; with
// elementId the local variable of that element (DBG-04).
func (this *Controller) HistoryVariable(ctx context.Context, token string, namespace string, id string, name string, elementId string, options model.HistoryOptions) (result model.HistoryVariable, err error) {
	if networkId, _, err := this.onGateway(ctx, token, namespace, id); err != nil || networkId != "" {
		if err == nil {
			err = this.networks.Call(ctx, networkId, protocol.MethodHistoryVariable, namespace, id, protocol.RequestArgs{Name: name, ElementId: elementId, Options: requestOptions(options).Options}, &result)
		}
		return result, err
	}
	err = this.store.Tx(ctx, func(tx *store.Tx) error {
		state, err := this.historyState(ctx, tx, token, namespace, id, options)
		if err != nil {
			return err
		}
		v, found := lookup(state, name, elementId)
		if !found {
			return model.NotFound("variable %v does not exist at step %v", name, state.Seq)
		}
		result = model.HistoryVariable{Seq: state.Seq, Name: name, ElementId: elementId, Available: !v.Truncated}
		if result.Available {
			result.Value = v.Value
		}
		return nil
	})
	return result, err
}

// GetVariable returns the complete current value of a variable; with elementId the local
// variable of a token at that element (DBG-04).
func (this *Controller) GetVariable(ctx context.Context, token string, namespace string, id string, name string, elementId string) (result model.Variable, err error) {
	if networkId, _, err := this.onGateway(ctx, token, namespace, id); err != nil || networkId != "" {
		if err == nil {
			err = this.networks.Call(ctx, networkId, protocol.MethodGetVariable, namespace, id, protocol.RequestArgs{Name: name, ElementId: elementId}, &result)
		}
		return result, err
	}
	err = this.store.Tx(ctx, func(tx *store.Tx) error {
		inst, err := this.readInstance(ctx, tx, token, namespace, id, Read)
		if err != nil {
			return err
		}
		state, err := this.engine.DebugState(tx, inst)
		if err != nil {
			return err
		}
		v, found := lookup(state, name, elementId)
		if !found {
			return model.NotFound("variable %v not found", name)
		}
		result = model.Variable{Name: name, Value: v.Value, Type: v.Type, UpdatedAt: v.UpdatedAt, ElementId: v.ElementId}
		return nil
	})
	return result, err
}

func lookup(state model.DebugState, name string, elementId string) (model.DebugVariable, bool) {
	if elementId == "" {
		v, ok := state.Variables[name]
		return v, ok
	}
	for _, a := range state.Active {
		if a.ElementId == elementId {
			v, ok := a.Locals[name]
			return v, ok
		}
	}
	return model.DebugVariable{}, false
}
