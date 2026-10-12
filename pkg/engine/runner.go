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
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/bpmn"
	"github.com/SENERGY-Platform/automation-engine/pkg/debug"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/script"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
	"github.com/SENERGY-Platform/automation-engine/pkg/timer"
	"github.com/google/uuid"
)

// runner advances the tokens of one run inside one transaction that holds the instance lock.
// Every change is written through to the store; the transaction makes the step atomic (RUN-08).
type runner struct {
	e      *Engine
	tx     *store.Tx
	inst   *store.Instance
	proc   *bpmn.Process
	now    time.Time
	limits model.Limits

	vars     map[string]any
	varSizes map[string]int
	tokens   map[string]*store.Token
	ready    []string
	// set when the run ended through an incident or a stop; nothing executes after that
	halted bool
	// a job or timer has been created; the scheduler is woken after commit
	scheduled bool
}

// incidentError aborts execution and turns into an incident of the run (INC-01)
type incidentError struct {
	elementId string
	err       error
}

func (this *incidentError) Error() string {
	return this.err.Error()
}

func incidentf(elementId string, format string, args ...any) error {
	return &incidentError{elementId: elementId, err: fmt.Errorf(format, args...)}
}

func (this *Engine) newRunner(tx *store.Tx, inst *store.Instance) (*runner, error) {
	proc, err := this.process(tx, inst.DeploymentId, inst.DeploymentVersion)
	if err != nil {
		return nil, err
	}
	r := &runner{e: this, tx: tx, inst: inst, proc: proc, now: this.now(), limits: this.limits(inst.Namespace), vars: map[string]any{}, varSizes: map[string]int{}, tokens: map[string]*store.Token{}}
	variables, err := tx.GetVariables(inst.Id)
	if err != nil {
		return nil, err
	}
	for _, v := range variables {
		r.vars[v.Name] = v.Value
		r.varSizes[v.Name] = jsonSize(v.Value)
	}
	tokens, err := tx.ListTokens(inst.Id)
	if err != nil {
		return nil, err
	}
	for i := range tokens {
		t := tokens[i]
		if t.Locals == nil {
			t.Locals = map[string]any{}
		}
		r.tokens[t.Id] = &t
	}
	return r, nil
}

func jsonSize(value any) int {
	raw, _ := json.Marshal(value)
	return len(raw)
}

// finish runs all ready tokens, ends the run when no token is left and writes the instance.
func (this *runner) finish() error {
	if err := this.loop(); err != nil {
		return err
	}
	if !this.halted && this.inst.State == model.StateRunning && len(this.tokens) == 0 {
		if err := this.endRun(model.StateCompleted, ""); err != nil {
			return err
		}
	}
	if this.inst.ActivitySeq-this.inst.ActivityMinSeq+1 > int64(this.limits.MaxActivities) {
		this.inst.ActivityMinSeq = this.inst.ActivitySeq - int64(this.limits.MaxActivities) + 1
		if err := this.tx.TrimActivities(this.inst.Id, this.inst.ActivityMinSeq); err != nil {
			return err
		}
		if err := this.tx.TrimSnapshots(this.inst.Id, this.inst.ActivityMinSeq, this.e.config.SnapshotsKept); err != nil {
			return err
		}
	}
	if err := this.snapshot(); err != nil {
		return err
	}
	if err := this.tx.UpdateInstance(*this.inst); err != nil {
		return err
	}
	if this.scheduled {
		this.e.signal()
	}
	return nil
}

// snapshot writes the full state for the history when enough activity entries or time passed
// since the last one (DBG-14); tokens, subscriptions and variables of this transaction are
// already written
func (this *runner) snapshot() error {
	interval := int64(max(50, this.limits.MaxActivities/max(1, this.e.config.SnapshotsPerLog)))
	since := this.inst.ActivitySeq - this.inst.SnapshotSeq
	if since <= 0 || (since < interval && this.now.Sub(this.inst.SnapshotAt) < this.e.config.SnapshotMaxAge) {
		return nil
	}
	state, err := debug.Live(this.tx, *this.inst, this.proc)
	if err != nil {
		return err
	}
	if err = this.tx.SaveSnapshot(this.inst.Id, this.inst.ActivitySeq, this.inst.Run, this.now, state); err != nil {
		return err
	}
	this.inst.SnapshotSeq, this.inst.SnapshotAt = this.inst.ActivitySeq, this.now
	return this.tx.TrimSnapshots(this.inst.Id, this.inst.ActivityMinSeq, this.e.config.SnapshotsKept)
}

// loop executes ready tokens; afterwards it activates inclusive joins that no other token
// can reach anymore and continues until nothing is ready
func (this *runner) loop() error {
	for !this.halted {
		if len(this.ready) == 0 {
			activated, err := this.activateInclusiveJoins()
			if err != nil {
				return this.handle(err)
			}
			if !activated {
				return nil
			}
			continue
		}
		id := this.ready[0]
		this.ready = this.ready[1:]
		token, ok := this.tokens[id]
		if !ok {
			continue
		}
		if err := this.execute(token); err != nil {
			if err = this.handle(err); err != nil {
				return err
			}
		}
	}
	return nil
}

// handle turns an incident error into an incident and returns other errors unchanged
func (this *runner) handle(err error) error {
	var inc *incidentError
	if !errors.As(err, &inc) {
		return err
	}
	return this.incident(inc.elementId, inc.err.Error())
}

func (this *runner) log(kind model.ActivityKind, element *bpmn.Element, data map[string]any) error {
	this.inst.ActivitySeq++
	a := model.Activity{InstanceId: this.inst.Id, Run: this.inst.Run, Seq: this.inst.ActivitySeq, Time: this.now, Kind: kind, Data: data}
	if element != nil {
		a.ElementId = element.Id
		a.ElementType = string(element.Type)
	}
	return this.tx.InsertActivity(a)
}

// preview truncates a value for the activity log
func (this *runner) preview(value any) any {
	raw, err := json.Marshal(value)
	if err != nil || len(raw) <= this.e.config.MaxActivityValueBytes {
		return value
	}
	return map[string]any{"truncated": true, "size": len(raw), "preview": string(raw[:this.e.config.MaxActivityValueBytes])}
}

func (this *runner) newToken(elementId string, arrivedVia string, locals map[string]any) (*store.Token, error) {
	if len(this.tokens) >= this.limits.MaxTokens {
		return nil, incidentf(elementId, "instance exceeds %v concurrent tokens", this.limits.MaxTokens)
	}
	if locals == nil {
		locals = map[string]any{}
	}
	t := &store.Token{Id: uuid.NewString(), InstanceId: this.inst.Id, Run: this.inst.Run, ElementId: elementId, State: store.TokenReady, ArrivedVia: arrivedVia, Locals: locals, CreatedAt: this.now}
	this.tokens[t.Id] = t
	this.ready = append(this.ready, t.Id)
	return t, this.tx.SaveToken(*t)
}

func (this *runner) saveToken(t *store.Token) error {
	return this.tx.SaveToken(*t)
}

func (this *runner) removeToken(t *store.Token) error {
	delete(this.tokens, t.Id)
	if err := this.tx.DeleteSubscriptionsOfToken(t.Id); err != nil {
		return err
	}
	return this.tx.DeleteToken(t.Id)
}

// scope is what scripts see: process variables overlaid with the token's local variables
func (this *runner) scope(t *store.Token) map[string]any {
	result := maps.Clone(this.vars)
	if t != nil {
		maps.Copy(result, t.Locals)
	}
	return result
}

func (this *runner) eval(element *bpmn.Element, t *store.Token, src string) (any, error) {
	result, err := this.e.sandbox.Run(src, this.scope(t))
	var logs []string
	var scriptErr *script.Error
	if errors.As(err, &scriptErr) {
		logs = scriptErr.Logs
	} else {
		logs = result.Logs
	}
	for _, line := range logs {
		if logErr := this.log(model.ActivityScriptLog, element, map[string]any{"message": line}); logErr != nil {
			return nil, logErr
		}
	}
	if err != nil {
		return nil, incidentf(element.Id, "script error: %v", err)
	}
	for _, a := range result.Assignments {
		if err = this.assign(element, t, a.Name, a.Value); err != nil {
			return nil, err
		}
	}
	return result.Value, nil
}

func (this *runner) evalValue(element *bpmn.Element, t *store.Token, v bpmn.Value) (any, error) {
	switch v.Kind {
	case bpmn.ValueScript:
		return this.eval(element, t, v.Script)
	case bpmn.ValueMap:
		result := map[string]any{}
		for _, entry := range v.Map {
			value, err := this.evalValue(element, t, entry.Value)
			if err != nil {
				return nil, err
			}
			result[entry.Key] = value
		}
		return result, nil
	case bpmn.ValueList:
		result := []any{}
		for _, item := range v.List {
			value, err := this.evalValue(element, t, item)
			if err != nil {
				return nil, err
			}
			result = append(result, value)
		}
		return result, nil
	default:
		return v.Text, nil
	}
}

func (this *runner) checkSize(elementId string, name string, value any) (int, error) {
	size := jsonSize(value)
	if size > this.limits.MaxVariableBytes {
		return 0, incidentf(elementId, "variable %v has %v bytes, the limit is %v", name, size, this.limits.MaxVariableBytes)
	}
	return size, nil
}

// setProcessVariable writes a variable of the instance (LONG-08 limits apply)
func (this *runner) setProcessVariable(element *bpmn.Element, name string, value any) error {
	elementId := ""
	if element != nil {
		elementId = element.Id
	}
	size, err := this.checkSize(elementId, name, value)
	if err != nil {
		return err
	}
	total := size
	for n, s := range this.varSizes {
		if n != name {
			total += s
		}
	}
	if total > this.limits.MaxInstanceVariablesBytes {
		return incidentf(elementId, "variables of the instance have %v bytes, the limit is %v", total, this.limits.MaxInstanceVariablesBytes)
	}
	this.vars[name] = value
	this.varSizes[name] = size
	if err = this.tx.SetVariable(this.inst.Id, name, value, elementId, this.now); err != nil {
		return err
	}
	return this.log(model.ActivityVariableSet, element, map[string]any{"name": name, "value": this.preview(value), "scope": "instance"})
}

func (this *runner) setLocal(element *bpmn.Element, t *store.Token, name string, value any) error {
	if _, err := this.checkSize(element.Id, name, value); err != nil {
		return err
	}
	t.Locals[name] = value
	return this.log(model.ActivityVariableSet, element, map[string]any{"name": name, "value": this.preview(value), "scope": "local"})
}

// assign follows the variable scoping of Camunda: a variable that exists locally is set
// locally, every other variable on the instance
func (this *runner) assign(element *bpmn.Element, t *store.Token, name string, value any) error {
	if t != nil {
		if _, local := t.Locals[name]; local {
			return this.setLocal(element, t, name, value)
		}
	}
	return this.setProcessVariable(element, name, value)
}

func (this *runner) applyInputs(element *bpmn.Element, t *store.Token) error {
	for _, m := range element.Inputs {
		value, err := this.evalValue(element, t, m.Value)
		if err != nil {
			return err
		}
		if err = this.setLocal(element, t, m.Name, value); err != nil {
			return err
		}
	}
	return nil
}

func (this *runner) applyOutputs(element *bpmn.Element, t *store.Token) error {
	for _, m := range element.Outputs {
		value, err := this.evalValue(element, t, m.Value)
		if err != nil {
			return err
		}
		if err = this.setProcessVariable(element, m.Name, value); err != nil {
			return err
		}
	}
	return nil
}

func (this *runner) enter(element *bpmn.Element, t *store.Token) error {
	this.inst.LastActivityAt = this.now
	if err := this.tx.CountElementPass(this.inst.Id, this.inst.Run, element.Id, this.now); err != nil {
		return err
	}
	data := map[string]any{}
	if element.Name != "" {
		data["name"] = element.Name
	}
	if t.ArrivedVia != "" {
		data["flow_id"] = t.ArrivedVia
	}
	return this.log(model.ActivityElementEntered, element, data)
}

func (this *runner) execute(t *store.Token) error {
	element := this.proc.Element(t.ElementId)
	if element == nil {
		return incidentf(t.ElementId, "element %v does not exist in deployment version %v", t.ElementId, this.inst.DeploymentVersion)
	}
	if element.Type == bpmn.ParallelGateway || element.Type == bpmn.InclusiveGateway {
		if len(element.Incoming) > 1 {
			return this.arriveAtJoin(element, t)
		}
	}
	if err := this.enter(element, t); err != nil {
		return err
	}
	switch element.Type {
	case bpmn.StartEvent, bpmn.EndEvent, bpmn.IntermediateThrowEvent, bpmn.Task:
		if err := this.applyInputs(element, t); err != nil {
			return err
		}
		if err := this.applyOutputs(element, t); err != nil {
			return err
		}
		return this.leave(element, t)
	case bpmn.ScriptTask:
		if err := this.applyInputs(element, t); err != nil {
			return err
		}
		result, err := this.eval(element, t, element.Script)
		if err != nil {
			return err
		}
		if element.ResultVariable != "" {
			if err = this.assign(element, t, element.ResultVariable, result); err != nil {
				return err
			}
		}
		if err = this.applyOutputs(element, t); err != nil {
			return err
		}
		return this.leave(element, t)
	case bpmn.ServiceTask:
		if err := this.applyInputs(element, t); err != nil {
			return err
		}
		return this.createJob(element, t)
	case bpmn.IntermediateCatchEvent:
		if err := this.applyInputs(element, t); err != nil {
			return err
		}
		if err := this.subscribe(element, t, ""); err != nil {
			return err
		}
		t.State = store.TokenWaiting
		return this.saveToken(t)
	case bpmn.ExclusiveGateway:
		return this.exclusive(element, t)
	case bpmn.ParallelGateway:
		return this.forkAll(element, t)
	case bpmn.InclusiveGateway:
		return this.inclusiveFork(element, t)
	case bpmn.EventBasedGateway:
		for _, flow := range element.Outgoing {
			if err := this.subscribe(flow.Target, t, t.Id); err != nil {
				return err
			}
		}
		t.State = store.TokenWaiting
		return this.saveToken(t)
	default:
		return incidentf(element.Id, "element type %v is not executable", element.Type)
	}
}

// subscribe makes the token wait for the message or timer of a catch event; after an
// event-based gateway the group is the token id
func (this *runner) subscribe(element *bpmn.Element, t *store.Token, group string) error {
	s := store.Subscription{Id: uuid.NewString(), InstanceId: this.inst.Id, Run: this.inst.Run, TokenId: t.Id, ElementId: element.Id, GroupId: group, CreatedAt: this.now}
	data := map[string]any{}
	if group != "" {
		// the token waits at the event-based gateway; leaving it cancels this wait
		data["gateway_id"] = t.ElementId
	}
	switch element.Kind {
	case bpmn.EventMessage:
		s.Kind = store.SubscriptionMessage
		s.MessageName = messageName(this.inst.DeploymentId, element)
		if err := this.tx.InsertSubscription(s); err != nil {
			return err
		}
		data["message_name"] = s.MessageName
		return this.log(model.ActivityMessageWaiting, element, data)
	case bpmn.EventTimer:
		value, err := this.timerValue(element, t)
		if err != nil {
			return err
		}
		due, err := timer.Next(element.Timer.Kind, value, this.now, this.e.config.Location)
		if err != nil {
			return incidentf(element.Id, "timer: %v", err)
		}
		s.Kind = store.SubscriptionTimer
		s.DueAt = &due
		s.Definition = value
		this.scheduled = true
		if err = this.tx.InsertSubscription(s); err != nil {
			return err
		}
		data["due"], data["definition"], data["kind"] = due, value, string(element.Timer.Kind)
		return this.log(model.ActivityTimerScheduled, element, data)
	default:
		return incidentf(element.Id, "catch event without message or timer")
	}
}

func (this *runner) timerValue(element *bpmn.Element, t *store.Token) (string, error) {
	if !element.Timer.Expression {
		return element.Timer.Value, nil
	}
	value, err := this.eval(element, t, element.Timer.Value)
	if err != nil {
		return "", err
	}
	s, ok := value.(string)
	if !ok {
		return "", incidentf(element.Id, "timer expression must evaluate to a string, got %v", value)
	}
	return s, nil
}

func (this *runner) createJob(element *bpmn.Element, t *store.Token) error {
	if ns, ok := this.e.namespaces.Get(this.inst.Namespace); !ok || !ns.TaskTypes.Permits(element.TaskType) {
		return incidentf(element.Id, "task type %q is not allowed in namespace %v", element.TaskType, this.inst.Namespace)
	}
	if this.e.config.ExecutesTasks && !this.e.TaskTypeAvailable(element.TaskType) {
		return incidentf(element.Id, "task type %q is not available", element.TaskType)
	}
	retries := this.e.config.TaskRetries
	timeout := this.e.config.TaskTimeout
	if taskType, ok := this.e.taskType(element.TaskType); ok {
		if defaults, ok := taskType.(JobDefaults); ok {
			r, d := defaults.Defaults(t.Locals)
			if r != nil {
				retries = *r
			}
			if d != nil {
				timeout = *d
			}
		}
	}
	if element.Retries != nil {
		retries = *element.Retries
	}
	if element.Timeout != "" {
		d, err := timer.ParseDuration(element.Timeout)
		if err != nil {
			return incidentf(element.Id, "timeout: %v", err)
		}
		timeout = d.AddTo(this.now).Sub(this.now)
	}
	job := store.Job{
		Id: uuid.NewString(), InstanceId: this.inst.Id, Run: this.inst.Run, TokenId: t.Id, ElementId: element.Id, TaskType: element.TaskType,
		Input: t.Locals, State: store.JobPending, Retries: retries, Timeout: timeout, DueAt: this.now, CreatedAt: this.now,
	}
	if err := this.tx.SaveJob(job); err != nil {
		return err
	}
	this.scheduled = true
	t.State = store.TokenWaiting
	if err := this.saveToken(t); err != nil {
		return err
	}
	return this.log(model.ActivityTaskCreated, element, map[string]any{"job_id": job.Id, "task_type": job.TaskType, "retries": retries, "timeout": timeout.String()})
}

// leave takes all outgoing flows that are unconditional or whose condition is true, or the
// default flow if none is; an element without outgoing flows ends the token (BPMN-05)
func (this *runner) leave(element *bpmn.Element, t *store.Token) error {
	flows, err := this.selectFlows(element, t, true)
	if err != nil {
		return err
	}
	return this.take(element, t, flows)
}

func (this *runner) selectFlows(element *bpmn.Element, t *store.Token, all bool) ([]*bpmn.Flow, error) {
	if len(element.Outgoing) == 0 {
		return nil, nil
	}
	var selected []*bpmn.Flow
	for _, flow := range element.Outgoing {
		if flow == element.DefaultFlow {
			continue
		}
		ok := true
		if flow.Condition != "" {
			value, err := this.eval(element, t, flow.Condition)
			if err != nil {
				return nil, err
			}
			ok, err = script.Truthy(value)
			if err != nil {
				return nil, incidentf(flow.Id, "%v", err)
			}
			if err = this.log(model.ActivityCondition, element, map[string]any{"flow_id": flow.Id, "condition": flow.Condition, "result": ok}); err != nil {
				return nil, err
			}
		}
		if ok {
			selected = append(selected, flow)
			if !all {
				return selected, nil
			}
		}
	}
	if len(selected) == 0 {
		if element.DefaultFlow == nil {
			return nil, incidentf(element.Id, "no outgoing flow of %v can be taken", element.Id)
		}
		selected = []*bpmn.Flow{element.DefaultFlow}
	}
	return selected, nil
}

// take moves the token along the first flow and creates new tokens for the others
func (this *runner) take(element *bpmn.Element, t *store.Token, flows []*bpmn.Flow) error {
	if err := this.log(model.ActivityElementLeft, element, nil); err != nil {
		return err
	}
	this.inst.LastActivityAt = this.now
	if len(flows) == 0 {
		return this.removeToken(t)
	}
	for i, flow := range flows {
		if err := this.log(model.ActivityFlowTaken, element, map[string]any{"flow_id": flow.Id, "target": flow.Target.Id}); err != nil {
			return err
		}
		// taken flows are marked in the diagram like passed elements (DBG-07)
		if err := this.tx.CountElementPass(this.inst.Id, this.inst.Run, flow.Id, this.now); err != nil {
			return err
		}
		if i == 0 {
			if err := this.tx.DeleteSubscriptionsOfToken(t.Id); err != nil {
				return err
			}
			t.ElementId = flow.Target.Id
			t.ArrivedVia = flow.Id
			t.State = store.TokenReady
			t.Locals = map[string]any{}
			if err := this.saveToken(t); err != nil {
				return err
			}
			this.ready = append(this.ready, t.Id)
			continue
		}
		if _, err := this.newToken(flow.Target.Id, flow.Id, nil); err != nil {
			return err
		}
	}
	return nil
}

func (this *runner) exclusive(element *bpmn.Element, t *store.Token) error {
	flows, err := this.selectFlows(element, t, false)
	if err != nil {
		return err
	}
	return this.take(element, t, flows)
}

func (this *runner) forkAll(element *bpmn.Element, t *store.Token) error {
	return this.take(element, t, element.Outgoing)
}

func (this *runner) inclusiveFork(element *bpmn.Element, t *store.Token) error {
	flows, err := this.selectFlows(element, t, true)
	if err != nil {
		return err
	}
	return this.take(element, t, flows)
}

func (this *runner) joining(element *bpmn.Element) []*store.Token {
	var result []*store.Token
	for _, t := range this.tokens {
		if t.ElementId == element.Id && t.State == store.TokenJoining {
			result = append(result, t)
		}
	}
	slices.SortFunc(result, func(a, b *store.Token) int {
		if c := a.CreatedAt.Compare(b.CreatedAt); c != 0 {
			return c
		}
		return compareStrings(a.Id, b.Id)
	})
	return result
}

func compareStrings(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// arriveAtJoin parks the token at a joining gateway. Like Camunda 7, a parallel join fires
// when as many tokens wait as the gateway has incoming flows; an inclusive join also fires
// when no other token can reach it anymore (BPMN-14, BPMN-15).
func (this *runner) arriveAtJoin(element *bpmn.Element, t *store.Token) error {
	t.State = store.TokenJoining
	if err := this.saveToken(t); err != nil {
		return err
	}
	waiting := this.joining(element)
	if len(waiting) >= len(element.Incoming) {
		if element.Type == bpmn.ParallelGateway {
			return this.fire(element, waiting[:len(element.Incoming)])
		}
		return this.fire(element, waiting)
	}
	if err := this.log(model.ActivityTokenJoinWaiting, element, map[string]any{"arrived": len(waiting), "required": len(element.Incoming), "flow_id": t.ArrivedVia}); err != nil {
		return err
	}
	if element.Type == bpmn.InclusiveGateway && !this.reachableByOthers(element) {
		return this.fire(element, waiting)
	}
	return nil
}

// fire merges the joined tokens into the first one and leaves the gateway
func (this *runner) fire(element *bpmn.Element, joined []*store.Token) error {
	keep := joined[0]
	for _, other := range joined[1:] {
		if err := this.removeToken(other); err != nil {
			return err
		}
	}
	keep.State = store.TokenReady
	if err := this.enter(element, keep); err != nil {
		return err
	}
	if element.Type == bpmn.ParallelGateway {
		return this.forkAll(element, keep)
	}
	return this.inclusiveFork(element, keep)
}

// reachableByOthers reports whether a token that does not wait at the gateway can still
// arrive there
func (this *runner) reachableByOthers(gateway *bpmn.Element) bool {
	for _, t := range this.tokens {
		if t.ElementId == gateway.Id && t.State == store.TokenJoining {
			continue
		}
		start := this.proc.Element(t.ElementId)
		if start != nil && reachable(start, gateway, map[string]bool{}) {
			return true
		}
	}
	return false
}

func reachable(from *bpmn.Element, target *bpmn.Element, visited map[string]bool) bool {
	if from == target {
		return true
	}
	if visited[from.Id] {
		return false
	}
	visited[from.Id] = true
	for _, flow := range from.Outgoing {
		if reachable(flow.Target, target, visited) {
			return true
		}
	}
	return false
}

// activateInclusiveJoins fires inclusive joins whose missing tokens ended or went elsewhere
func (this *runner) activateInclusiveJoins() (bool, error) {
	for _, element := range this.proc.Ordered() {
		if element.Type != bpmn.InclusiveGateway || len(element.Incoming) < 2 {
			continue
		}
		waiting := this.joining(element)
		if len(waiting) == 0 || this.reachableByOthers(element) {
			continue
		}
		if err := this.fire(element, waiting); err != nil {
			return false, err
		}
		return true, nil
	}
	return false, nil
}

// trigger continues a token waiting at a catch event or an event-based gateway
func (this *runner) trigger(s store.Subscription, locals map[string]any, variables map[string]any, activity model.ActivityKind, data map[string]any) error {
	t, ok := this.tokens[s.TokenId]
	if !ok {
		return fmt.Errorf("token %v of subscription %v not found", s.TokenId, s.Id)
	}
	if s.GroupId != "" {
		gateway := this.proc.Element(t.ElementId)
		if gateway == nil {
			return incidentf(t.ElementId, "element does not exist")
		}
		if err := this.tx.DeleteSubscriptionsOfToken(t.Id); err != nil {
			return err
		}
		var via *bpmn.Flow
		for _, flow := range gateway.Outgoing {
			if flow.Target.Id == s.ElementId {
				via = flow
			}
		}
		if via == nil {
			return incidentf(gateway.Id, "event-based gateway has no flow to %v", s.ElementId)
		}
		if err := this.take(gateway, t, []*bpmn.Flow{via}); err != nil {
			return err
		}
		// take queued the token as ready; it continues here at the catch event instead
		this.ready = slices.DeleteFunc(this.ready, func(id string) bool { return id == t.Id })
		element := this.proc.Element(s.ElementId)
		if err := this.enter(element, t); err != nil {
			return err
		}
	} else if err := this.tx.DeleteSubscriptionsOfToken(t.Id); err != nil {
		return err
	}
	element := this.proc.Element(s.ElementId)
	if element == nil {
		return incidentf(s.ElementId, "element does not exist")
	}
	if err := this.log(activity, element, data); err != nil {
		return err
	}
	for name, value := range variables {
		if err := this.setProcessVariable(element, name, value); err != nil {
			return err
		}
	}
	for name, value := range locals {
		if err := this.setLocal(element, t, name, value); err != nil {
			return err
		}
	}
	if err := this.applyOutputs(element, t); err != nil {
		return err
	}
	return this.leave(element, t)
}

// completeTask continues the token of a finished job with the task output as local variables
func (this *runner) completeTask(job store.Job, output map[string]any) error {
	t, ok := this.tokens[job.TokenId]
	if !ok {
		return fmt.Errorf("token %v of job %v not found", job.TokenId, job.Id)
	}
	element := this.proc.Element(job.ElementId)
	if element == nil {
		return incidentf(job.ElementId, "element does not exist")
	}
	if err := this.tx.DeleteJob(job.Id); err != nil {
		return err
	}
	if err := this.log(model.ActivityTaskCompleted, element, map[string]any{"job_id": job.Id, "attempt": job.Attempt, "output": this.preview(output)}); err != nil {
		return err
	}
	for name, value := range output {
		normalized, err := script.Normalize(value, this.limits.MaxVariableBytes)
		if err != nil {
			return incidentf(element.Id, "task output %v: %v", name, err)
		}
		if err = this.setLocal(element, t, name, normalized); err != nil {
			return err
		}
	}
	if err := this.applyOutputs(element, t); err != nil {
		return err
	}
	return this.leave(element, t)
}

// incident records the failure with the variables at that time and ends the run (INC-01,
// INC-06); handling by deployment settings (restart, notify) follows in INC-02
func (this *runner) incident(elementId string, message string) error {
	element := this.proc.Element(elementId)
	incident := model.Incident{
		Id: uuid.NewString(), Namespace: this.inst.Namespace, Owner: this.inst.Owner, InstanceId: this.inst.Id, Run: this.inst.Run,
		DeploymentId: this.inst.DeploymentId, DeploymentVersion: this.inst.DeploymentVersion, ElementId: elementId, Message: message, Time: this.now,
		Variables: this.incidentVariables(elementId),
	}
	if element != nil {
		incident.ElementName = element.Name
	}
	if err := this.tx.InsertIncident(incident); err != nil {
		return err
	}
	if elementId != "" {
		if err := this.tx.RecordElementError(this.inst.Id, this.inst.Run, elementId, message, this.now); err != nil {
			return err
		}
	}
	activityElement := element
	if activityElement == nil {
		activityElement = &bpmn.Element{Id: elementId}
	}
	if err := this.log(model.ActivityIncident, activityElement, map[string]any{"incident_id": incident.Id, "message": message}); err != nil {
		return err
	}
	this.e.logger.Info("incident", "instance_id", this.inst.Id, "run", this.inst.Run, "deployment_id", this.inst.DeploymentId, "element_id", elementId, "message", message)
	this.e.metrics.Incident(this.inst.Namespace, this.inst.DeploymentId, this.inst.Owner)
	if err := this.endRun(model.StateIncident, message); err != nil {
		return err
	}
	return this.handleIncident(message)
}

func (this *runner) incidentVariables(elementId string) map[string]any {
	result := map[string]any{}
	for name, value := range this.vars {
		result[name] = this.preview(value)
	}
	for _, t := range this.tokens {
		if t.ElementId == elementId {
			for name, value := range t.Locals {
				result[name] = this.preview(value)
			}
		}
	}
	return result
}

// endRun finishes the current run and removes its runtime state
func (this *runner) endRun(state model.InstanceState, reason string) error {
	this.halted = true
	this.ready = nil
	this.tokens = map[string]*store.Token{}
	if err := this.tx.ClearRuntime(this.inst.Id); err != nil {
		return err
	}
	ended := this.now
	this.inst.State = state
	this.inst.EndReason = reason
	this.inst.EndedAt = &ended
	run, err := this.tx.GetRun(this.inst.Id, this.inst.Run)
	if err != nil {
		return err
	}
	run.State = state
	run.EndReason = reason
	run.EndedAt = &ended
	if err = this.tx.UpdateRun(run); err != nil {
		return err
	}
	this.e.metrics.InstanceEnded(this.inst.Namespace, this.inst.DeploymentId, this.inst.Owner, state)
	data := map[string]any{"state": string(state)}
	if reason != "" {
		data["reason"] = reason
	}
	return this.log(model.ActivityRunEnded, nil, data)
}
