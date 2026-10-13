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

// Package debug builds the state of an instance for the debugger (DBG-02), reconstructs it at a
// step of the history (DBG-13), derives the deltas of the live stream (DBG-03) and wakes the
// live streams of a replica when their instance changed (Hub, DBG-09).
package debug

import (
	"encoding/json"
	"sort"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/bpmn"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
)

// Variable describes a value; with limit > 0, values whose JSON form is longer are cut (DBG-04).
func Variable(value any, updatedAt time.Time, elementId string, limit int) model.DebugVariable {
	raw, _ := json.Marshal(value)
	result := model.DebugVariable{Value: value, Type: model.ValueType(value), UpdatedAt: updatedAt, ElementId: elementId, Size: len(raw)}
	if limit > 0 && len(raw) > limit {
		result.Value = string(raw[:limit])
		result.Truncated = true
	}
	return result
}

// Cut applies the transfer limit to all values of a state; values that are already cut stay so.
func Cut(state model.DebugState, limit int) model.DebugState {
	if limit <= 0 {
		return state
	}
	state.Variables = cutAll(state.Variables, limit)
	if len(state.Instance.StartVariables) > 0 {
		// in the form the activity log uses for cut values
		start := make(map[string]any, len(state.Instance.StartVariables))
		for name, value := range state.Instance.StartVariables {
			if raw, _ := json.Marshal(value); len(raw) > limit {
				value = map[string]any{"truncated": true, "size": len(raw), "preview": string(raw[:limit])}
			}
			start[name] = value
		}
		state.Instance.StartVariables = start
	}
	active := make([]model.ActiveElement, len(state.Active))
	for i, a := range state.Active {
		a.Locals = cutAll(a.Locals, limit)
		active[i] = a
	}
	state.Active = active
	return state
}

func cutAll(variables map[string]model.DebugVariable, limit int) map[string]model.DebugVariable {
	if variables == nil {
		return nil
	}
	result := make(map[string]model.DebugVariable, len(variables))
	for name, v := range variables {
		if !v.Truncated {
			v = Variable(v.Value, v.UpdatedAt, v.ElementId, limit)
		}
		result[name] = v
	}
	return result
}

// Live builds the current state from the runtime state of an instance. Values are complete;
// Cut limits them for transfer.
func Live(tx *store.Tx, inst store.Instance, proc *bpmn.Process) (result model.DebugState, err error) {
	result = model.DebugState{Instance: inst.Instance, Seq: inst.ActivitySeq, Time: inst.LastActivityAt, Truncated: inst.ActivityMinSeq > 1,
		Active: []model.ActiveElement{}, Waits: []model.Wait{}, Variables: map[string]model.DebugVariable{}, Incidents: []model.Incident{}}
	if result.Runs, err = tx.ListRuns(inst.Id); err != nil {
		return result, err
	}
	if result.Elements, err = tx.ListElementStats(inst.Id, inst.Run); err != nil {
		return result, err
	}
	lastPass := map[string]time.Time{}
	for _, s := range result.Elements {
		lastPass[s.ElementId] = s.LastAt
	}
	variables, err := tx.GetVariables(inst.Id)
	if err != nil {
		return result, err
	}
	for _, v := range variables {
		result.Variables[v.Name] = Variable(v.Value, v.UpdatedAt, v.ElementId, 0)
	}
	tokens, err := tx.ListTokens(inst.Id)
	if err != nil {
		return result, err
	}
	tokenElement := map[string]string{}
	active := map[string]*model.ActiveElement{}
	joins := map[string]int{}
	for _, t := range tokens {
		tokenElement[t.Id] = t.ElementId
		if t.State == store.TokenJoining {
			joins[t.ElementId]++
			continue
		}
		a, ok := active[t.ElementId]
		if !ok {
			a = &model.ActiveElement{ElementId: t.ElementId, Since: lastPass[t.ElementId]}
			if element := proc.Element(t.ElementId); element != nil {
				a.ElementType, a.Name = string(element.Type), element.Name
			}
			active[t.ElementId] = a
		}
		a.Tokens++
		for name, value := range t.Locals {
			if a.Locals == nil {
				a.Locals = map[string]model.DebugVariable{}
			}
			a.Locals[name] = Variable(value, time.Time{}, t.ElementId, 0)
		}
	}
	for _, a := range active {
		result.Active = append(result.Active, *a)
	}
	sort.Slice(result.Active, func(i, j int) bool { return result.Active[i].ElementId < result.Active[j].ElementId })
	for elementId, arrived := range joins {
		required := 0
		if element := proc.Element(elementId); element != nil {
			required = len(element.Incoming)
		}
		result.Waits = append(result.Waits, model.Wait{ElementId: elementId, Kind: model.WaitJoin, Since: lastPass[elementId], Join: &model.JoinWait{Arrived: arrived, Required: required}})
	}
	subscriptions, err := tx.ListSubscriptions(inst.Id)
	if err != nil {
		return result, err
	}
	var descriptions []model.EventDescription
	for _, s := range subscriptions {
		wait := model.Wait{ElementId: s.ElementId, Since: s.CreatedAt}
		if at := tokenElement[s.TokenId]; at != s.ElementId {
			wait.GatewayId = at
		}
		switch s.Kind {
		case store.SubscriptionTimer:
			wait.Kind = model.WaitTimer
			wait.Timer = &model.TimerWait{Definition: s.Definition, DueAt: s.DueAt}
			if element := proc.Element(s.ElementId); element != nil && element.Timer != nil {
				wait.Timer.Kind = string(element.Timer.Kind)
			}
		default:
			wait.Kind = model.WaitMessage
			wait.Message = &model.MessageWait{Name: s.MessageName}
			if descriptions == nil {
				if descriptions, err = tx.ListEventDescriptions(inst.DeploymentId); err != nil {
					return result, err
				}
			}
			for _, d := range descriptions {
				if d.ElementId == s.ElementId && d.Version == inst.DeploymentVersion {
					wait.Message.Events = append(wait.Message.Events, Source(d))
				}
			}
		}
		result.Waits = append(result.Waits, wait)
	}
	jobs, err := tx.ListJobs(inst.Id)
	if err != nil {
		return result, err
	}
	for _, j := range jobs {
		task := &model.TaskWait{JobId: j.Id, TaskType: j.TaskType, State: j.State, Attempt: j.Attempt, Retries: j.Retries, Timeout: j.Timeout.String(), LastError: j.LastError}
		due := j.DueAt
		task.DueAt = &due
		subtasks, err := tx.ListSubtasks(j.Id)
		if err != nil {
			return result, err
		}
		for _, s := range subtasks {
			var command struct {
				DeviceId  string `json:"device_id"`
				ServiceId string `json:"service_id"`
			}
			_ = json.Unmarshal(s.Command, &command)
			state := model.SubtaskState{Id: s.SubId, DeviceId: command.DeviceId, ServiceId: command.ServiceId, Done: s.HasResult, Tries: s.Tries}
			if !s.LastTry.IsZero() {
				lastTry := s.LastTry
				state.LastTry = &lastTry
			}
			task.Subtasks = append(task.Subtasks, state)
		}
		result.Waits = append(result.Waits, model.Wait{ElementId: j.ElementId, Kind: model.WaitTask, Since: j.CreatedAt, Task: task})
	}
	sortWaits(result.Waits)
	incidents, err := tx.ListIncidents(inst.Namespace, model.IncidentListOptions{InstanceId: inst.Id, Run: inst.Run, PageOptions: model.PageOptions{Limit: model.MaxPageLimit}}, store.Access{Admin: true})
	if err != nil {
		return result, err
	}
	result.Incidents = incidents.Items
	return result, nil
}

// Source describes the event source of a description (DBG-02).
func Source(d model.EventDescription) model.EventSource {
	return model.EventSource{EventDescriptionId: d.Id, DeviceId: d.DeviceId, ServiceId: d.ServiceId, DeviceGroupId: d.DeviceGroupId, ImportId: d.ImportId,
		Script: d.Script, ValueVariable: d.ValueVariable, Variables: d.Variables}
}

func sortWaits(waits []model.Wait) {
	sort.SliceStable(waits, func(i, j int) bool {
		if waits[i].ElementId != waits[j].ElementId {
			return waits[i].ElementId < waits[j].ElementId
		}
		return waits[i].Kind < waits[j].Kind
	})
}
