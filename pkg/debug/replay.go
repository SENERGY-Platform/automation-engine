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

package debug

import (
	"maps"
	"slices"
	"sort"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/model"
)

// Begin is the state before the first activity entry of an instance, the start of a replay
// when no snapshot precedes the step.
func Begin(inst model.Instance) model.DebugState {
	inst.Run = 0
	inst.State = model.StateRunning
	inst.EndedAt, inst.EndReason, inst.RestartAt = nil, "", nil
	return model.DebugState{Instance: inst, Active: []model.ActiveElement{}, Waits: []model.Wait{},
		Variables: map[string]model.DebugVariable{}, Incidents: []model.Incident{}, Elements: []model.ElementStats{}}
}

// Replay applies activity entries in order to a state (DBG-13). Values the activity log holds
// cut stay cut; the snapshots hold them complete (DBG-14).
func Replay(state model.DebugState, activities []model.Activity) model.DebugState {
	r := &replay{state: clone(state)}
	for _, a := range activities {
		r.apply(a)
		r.state.Seq, r.state.Time = a.Seq, a.Time
	}
	sort.Slice(r.state.Active, func(i, j int) bool { return r.state.Active[i].ElementId < r.state.Active[j].ElementId })
	sortWaits(r.state.Waits)
	return r.state
}

type replay struct {
	state model.DebugState
}

func clone(state model.DebugState) model.DebugState {
	state.Runs = slices.Clone(state.Runs)
	state.Active = slices.Clone(state.Active)
	for i := range state.Active {
		state.Active[i].Locals = maps.Clone(state.Active[i].Locals)
	}
	state.Waits = slices.Clone(state.Waits)
	state.Variables = maps.Clone(state.Variables)
	if state.Variables == nil {
		state.Variables = map[string]model.DebugVariable{}
	}
	state.Incidents = slices.Clone(state.Incidents)
	state.Elements = slices.Clone(state.Elements)
	return state
}

func (this *replay) apply(a model.Activity) {
	s := &this.state
	switch a.Kind {
	case model.ActivityRunStarted:
		s.Instance.Run = a.Run
		s.Instance.DeploymentVersion = toInt(a.Data["deployment_version"])
		s.Instance.State = model.StateRunning
		s.Instance.StartedAt, s.Instance.EndedAt, s.Instance.EndReason, s.Instance.RestartAt = a.Time, nil, "", nil
		s.Active, s.Waits, s.Incidents, s.Elements = []model.ActiveElement{}, []model.Wait{}, []model.Incident{}, []model.ElementStats{}
		s.Variables = map[string]model.DebugVariable{}
		s.Runs = slices.DeleteFunc(s.Runs, func(r model.Run) bool { return r.Run >= a.Run })
		cause, _ := a.Data["cause"].(string)
		s.Runs = append(s.Runs, model.Run{InstanceId: s.Instance.Id, Run: a.Run, DeploymentVersion: s.Instance.DeploymentVersion, Cause: model.RunCause(cause), State: model.StateRunning, StartedAt: a.Time})
	case model.ActivityRunEnded:
		state, _ := a.Data["state"].(string)
		reason, _ := a.Data["reason"].(string)
		ended := a.Time
		s.Instance.State, s.Instance.EndReason, s.Instance.EndedAt = model.InstanceState(state), reason, &ended
		s.Active, s.Waits = []model.ActiveElement{}, []model.Wait{}
		for i := range s.Runs {
			if s.Runs[i].Run == a.Run {
				s.Runs[i].State, s.Runs[i].EndReason, s.Runs[i].EndedAt = s.Instance.State, reason, &ended
			}
		}
	case model.ActivityElementEntered:
		this.count(a.ElementId, a.Time)
		s.Waits = slices.DeleteFunc(s.Waits, func(w model.Wait) bool { return w.Kind == model.WaitJoin && w.ElementId == a.ElementId })
		if i := this.active(a.ElementId); i >= 0 {
			s.Active[i].Tokens++
			return
		}
		name, _ := a.Data["name"].(string)
		s.Active = append(s.Active, model.ActiveElement{ElementId: a.ElementId, ElementType: a.ElementType, Name: name, Since: a.Time, Tokens: 1})
	case model.ActivityElementLeft:
		if i := this.active(a.ElementId); i >= 0 {
			s.Active[i].Tokens--
			if s.Active[i].Tokens <= 0 {
				s.Active = slices.Delete(s.Active, i, i+1)
			} else {
				s.Active[i].Locals = nil
			}
		}
		s.Waits = slices.DeleteFunc(s.Waits, func(w model.Wait) bool {
			return w.Kind != model.WaitJoin && (w.ElementId == a.ElementId || w.GatewayId == a.ElementId)
		})
	case model.ActivityFlowTaken:
		if flow, _ := a.Data["flow_id"].(string); flow != "" {
			this.count(flow, a.Time)
		}
	case model.ActivityVariableSet:
		name, _ := a.Data["name"].(string)
		v := Variable(a.Data["value"], a.Time, a.ElementId, 0)
		if cut, ok := a.Data["value"].(map[string]any); ok && cut["truncated"] == true && len(cut) == 3 {
			v = model.DebugVariable{Value: cut["preview"], Type: "", UpdatedAt: a.Time, ElementId: a.ElementId, Truncated: true, Size: toInt(cut["size"])}
		}
		if a.Data["scope"] == "local" {
			if i := this.active(a.ElementId); i >= 0 {
				if s.Active[i].Locals == nil {
					s.Active[i].Locals = map[string]model.DebugVariable{}
				}
				s.Active[i].Locals[name] = v
			}
			return
		}
		s.Variables[name] = v
	case model.ActivityVariableDeleted:
		name, _ := a.Data["name"].(string)
		delete(s.Variables, name)
	case model.ActivityMessageWaiting:
		name, _ := a.Data["message_name"].(string)
		this.wait(a, model.Wait{Kind: model.WaitMessage, Message: &model.MessageWait{Name: name}})
	case model.ActivityTimerScheduled:
		definition, _ := a.Data["definition"].(string)
		kind, _ := a.Data["kind"].(string)
		this.wait(a, model.Wait{Kind: model.WaitTimer, Timer: &model.TimerWait{Definition: definition, Kind: kind, DueAt: toTime(a.Data["due"])}})
	case model.ActivityTimerFired, model.ActivityMessageReceived:
		s.Waits = slices.DeleteFunc(s.Waits, func(w model.Wait) bool {
			return w.ElementId == a.ElementId && (w.Kind == model.WaitTimer || w.Kind == model.WaitMessage)
		})
	case model.ActivityTaskCreated:
		jobId, _ := a.Data["job_id"].(string)
		taskType, _ := a.Data["task_type"].(string)
		timeout, _ := a.Data["timeout"].(string)
		this.wait(a, model.Wait{Kind: model.WaitTask, Task: &model.TaskWait{JobId: jobId, TaskType: taskType, Retries: toInt(a.Data["retries"]), Timeout: timeout}})
	case model.ActivityTaskFailed:
		jobId, _ := a.Data["job_id"].(string)
		message, _ := a.Data["error"].(string)
		for i, w := range s.Waits {
			if w.Task != nil && w.Task.JobId == jobId {
				task := *w.Task
				task.Attempt, task.LastError = toInt(a.Data["attempt"]), message
				s.Waits[i].Task = &task
			}
		}
	case model.ActivityTaskCompleted:
		jobId, _ := a.Data["job_id"].(string)
		s.Waits = slices.DeleteFunc(s.Waits, func(w model.Wait) bool { return w.Task != nil && w.Task.JobId == jobId })
	case model.ActivityTokenJoinWaiting:
		join := &model.JoinWait{Arrived: toInt(a.Data["arrived"]), Required: toInt(a.Data["required"])}
		for i, w := range s.Waits {
			if w.Kind == model.WaitJoin && w.ElementId == a.ElementId {
				s.Waits[i].Join = join
				return
			}
		}
		s.Waits = append(s.Waits, model.Wait{ElementId: a.ElementId, Kind: model.WaitJoin, Since: a.Time, Join: join})
	case model.ActivityIncident:
		id, _ := a.Data["incident_id"].(string)
		message, _ := a.Data["message"].(string)
		s.Incidents = append(s.Incidents, model.Incident{Id: id, Namespace: s.Instance.Namespace, Owner: s.Instance.Owner, InstanceId: s.Instance.Id, Run: a.Run,
			DeploymentId: s.Instance.DeploymentId, DeploymentVersion: s.Instance.DeploymentVersion, ElementId: a.ElementId, Message: message, Time: a.Time})
		for i := range s.Elements {
			if s.Elements[i].ElementId == a.ElementId {
				at := a.Time
				s.Elements[i].LastError, s.Elements[i].ErrorAt = message, &at
			}
		}
	case model.ActivityRestartScheduled:
		s.Instance.RestartAt = toTime(a.Data["due"])
	}
}

func (this *replay) active(elementId string) int {
	return slices.IndexFunc(this.state.Active, func(a model.ActiveElement) bool { return a.ElementId == elementId })
}

func (this *replay) count(elementId string, at time.Time) {
	s := &this.state
	for i := range s.Elements {
		if s.Elements[i].ElementId == elementId {
			s.Elements[i].Count++
			s.Elements[i].LastAt = at
			return
		}
	}
	s.Elements = append(s.Elements, model.ElementStats{ElementId: elementId, Run: s.Instance.Run, Count: 1, LastAt: at})
}

func (this *replay) wait(a model.Activity, w model.Wait) {
	w.ElementId, w.Since = a.ElementId, a.Time
	w.GatewayId, _ = a.Data["gateway_id"].(string)
	this.state.Waits = append(this.state.Waits, w)
}

func toInt(value any) int {
	switch v := value.(type) {
	case float64:
		return int(v)
	case int:
		return v
	case int64:
		return int(v)
	}
	return 0
}

func toTime(value any) *time.Time {
	switch v := value.(type) {
	case time.Time:
		return &v
	case string:
		if t, err := time.Parse(time.RFC3339Nano, v); err == nil {
			return &t
		}
	}
	return nil
}
