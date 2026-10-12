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
	"fmt"
	"sort"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/auth"
	"github.com/SENERGY-Platform/automation-engine/pkg/bpmn"
	"github.com/SENERGY-Platform/automation-engine/pkg/debug"
	"github.com/SENERGY-Platform/automation-engine/pkg/events"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
)

// a correlation follows a triggering value within this time, or the reconstruction notes a deviation
const correlationTolerance = 5 * time.Minute

// SetEventDebugging enables the reconstruction of event evaluations (EVT-10) and the evaluation
// of the current value of waiting events (DBG-18); both read with the token of the owner.
func (this *Controller) SetEventDebugging(series events.Series, evaluator *events.Evaluator, userToken auth.UserTokenSource) {
	this.series = series
	this.evaluator = evaluator
	this.userToken = userToken
}

func (this *Controller) eventDebugging() error {
	if this.series == nil || this.evaluator == nil || this.userToken == nil {
		return model.Unavailable("event debugging needs timescale_wrapper_url, device_repository_url and auth_endpoint")
	}
	return nil
}

// EventEvaluations reconstructs the evaluations of a device event while its element waited,
// from the values in the time series and with the code of the event worker (EVT-10, EVT-11).
func (this *Controller) EventEvaluations(ctx context.Context, token string, namespace string, id string, options model.EventReconstructionOptions) (result model.EventReconstruction, err error) {
	if err = this.eventDebugging(); err != nil {
		return result, err
	}
	if options.ElementId == "" {
		return result, model.BadRequest("element_id is required")
	}
	limit := options.Limit
	if limit <= 0 {
		limit = 100
	}
	limit = min(limit, model.MaxPageLimit)
	var inst store.Instance
	var deployment model.Deployment
	var version model.DeploymentVersion
	var activities []model.Activity
	var descriptions []model.EventDescription
	var runEnd *time.Time
	err = this.store.Tx(ctx, func(tx *store.Tx) error {
		if inst, err = this.readInstance(ctx, tx, token, namespace, id, Read); err != nil {
			return err
		}
		result.ElementId, result.Run = options.ElementId, options.Run
		if result.Run <= 0 {
			result.Run = inst.Run
		}
		runs, err := tx.ListRuns(id)
		if err != nil {
			return err
		}
		result.DeploymentVersion = -1
		for _, r := range runs {
			if r.Run == result.Run {
				result.DeploymentVersion, runEnd = r.DeploymentVersion, r.EndedAt
			}
		}
		if result.DeploymentVersion < 0 {
			return model.NotFound("run %v of instance %v not found", result.Run, id)
		}
		for after := int64(0); ; {
			page, err := tx.ListActivities(id, model.ActivityListOptions{Run: result.Run, ElementId: options.ElementId, AfterSeq: after, Limit: model.MaxPageLimit})
			if err != nil {
				return err
			}
			activities = append(activities, page...)
			if len(page) < model.MaxPageLimit {
				break
			}
			after = page[len(page)-1].Seq
		}
		if deployment, err = tx.GetDeployment(inst.DeploymentId, false); err != nil {
			return err
		}
		stored, err := tx.ListEventDescriptions(inst.DeploymentId)
		if err != nil {
			return err
		}
		for _, d := range stored {
			if d.ElementId == options.ElementId && d.Version == result.DeploymentVersion {
				descriptions = append(descriptions, d)
			}
		}
		if len(descriptions) == 0 {
			version, err = tx.GetDeploymentVersion(inst.DeploymentId, result.DeploymentVersion)
		}
		return err
	})
	if err != nil {
		return result, err
	}
	if len(descriptions) == 0 && this.describer != nil && version.Xml != "" {
		// only the current version has stored descriptions; groups expand to their members of today
		process, _, err := bpmn.Parse(version.Xml)
		if err != nil {
			return result, err
		}
		all, _, err := this.describer.Describe(deployment, version.Version, process, version.Selections)
		if err != nil {
			return result, model.Unavailable("unable to describe the events of version %v: %v", version.Version, err)
		}
		for _, d := range all {
			if d.ElementId == options.ElementId {
				descriptions = append(descriptions, d)
			}
		}
		result.Limitations = append(result.Limitations, fmt.Sprintf("the event descriptions of version %v were derived again; device groups expand to their current members", version.Version))
	}
	result.Waiting, result.Correlations = waitPeriods(activities, runEnd)
	result.Evaluations = []model.SourceEvaluation{}
	if len(descriptions) == 0 {
		result.Limitations = append(result.Limitations, "the element has no device or import event")
		return result, nil
	}
	result.MessageName = descriptions[0].MessageName
	ownerToken, err := this.userToken(inst.Owner)
	if err != nil {
		return result, model.Unavailable("no token of the owner: %v", err)
	}
	now := time.Now()
	for _, d := range descriptions {
		result.Sources = append(result.Sources, debug.Source(d))
		if d.ImportId != "" {
			result.Limitations = append(result.Limitations, "import events are not reconstructed, it is open whether import data reaches timescale (EVT-12)")
			continue
		}
		service, err := events.ServiceOf(d)
		if err != nil {
			result.Limitations = append(result.Limitations, fmt.Sprintf("%v: %v", d.Id, err))
			continue
		}
		columns := events.Columns(service)
		count := 0
		for _, period := range result.Waiting {
			from, to := period.From, now
			if period.To != nil {
				to = *period.To
			}
			if options.After != nil && options.After.After(from) {
				from = *options.After
			}
			if !from.Before(to) || count >= limit {
				continue
			}
			rows, err := this.series.Range(ctx, ownerToken, d.DeviceId, d.ServiceId, columns, from, to, limit-count)
			if err != nil {
				return result, err
			}
			for _, row := range rows {
				message := events.MessageOf(row.Values)
				evaluation, err := this.evaluator.Evaluate(d, message)
				at := row.Time
				e := model.SourceEvaluation{EventDescriptionId: d.Id, EventEvaluation: model.EventEvaluation{Time: &at, Raw: message, Value: evaluation.Value, Triggered: evaluation.Triggered, Logs: evaluation.Logs}}
				if err != nil {
					e.Error = err.Error()
				}
				result.Evaluations = append(result.Evaluations, e)
			}
			count += len(rows)
			if count >= limit && len(rows) > 0 {
				last := rows[len(rows)-1].Time
				if result.Next == nil || last.Before(*result.Next) {
					result.Next = &last
				}
			}
		}
	}
	sort.SliceStable(result.Evaluations, func(i, j int) bool { return result.Evaluations[i].Time.Before(*result.Evaluations[j].Time) })
	if result.Next == nil && options.After == nil {
		result.Hints = deviations(result.Waiting, result.Evaluations)
	} else {
		// a page compares only part of the values with the correlations
		result.Limitations = append(result.Limitations, "deviations are named only when all evaluations fit on one page")
	}
	if len(result.Evaluations) == 0 {
		result.Limitations = append(result.Limitations, "the time series holds no values for the waiting time; timescale keeps data as long as its retention rules allow (EVT-12)")
	}
	return result, nil
}

// waitPeriods derives from the activities of an element when it waited and how it stopped
func waitPeriods(activities []model.Activity, runEnd *time.Time) (periods []model.WaitPeriod, correlations []model.Activity) {
	periods, correlations = []model.WaitPeriod{}, []model.Activity{}
	open := -1
	closeAt := func(at time.Time, correlated bool) {
		if open >= 0 {
			periods[open].To, periods[open].Correlated = &at, correlated
			open = -1
		}
	}
	for _, a := range activities {
		switch a.Kind {
		case model.ActivityMessageWaiting:
			closeAt(a.Time, false)
			periods = append(periods, model.WaitPeriod{From: a.Time})
			open = len(periods) - 1
		case model.ActivityMessageReceived:
			correlations = append(correlations, a)
			closeAt(a.Time, true)
		case model.ActivityElementLeft:
			closeAt(a.Time, false)
		}
	}
	if runEnd != nil {
		closeAt(*runEnd, false)
	}
	return periods, correlations
}

// deviations names where reconstruction and correlations disagree, with the possible causes (EVT-11)
func deviations(periods []model.WaitPeriod, evaluations []model.SourceEvaluation) (hints []string) {
	causes := "possible causes: the message was older than the maximum age (EVT-06), it was dropped with qos 0, the event worker was disturbed, or the condition is not deterministic, e.g. through Date"
	for _, p := range periods {
		var triggered []time.Time
		for _, e := range evaluations {
			if e.Triggered && !e.Time.Before(p.From) && (p.To == nil || !e.Time.After(*p.To)) {
				triggered = append(triggered, *e.Time)
			}
		}
		switch {
		case len(triggered) > 0 && (p.To == nil || !p.Correlated || p.To.Sub(triggered[0]) > correlationTolerance):
			hints = append(hints, fmt.Sprintf("the value at %v would have triggered, but no correlation followed within %v; %v", triggered[0].Format(time.RFC3339), correlationTolerance, causes))
		case p.Correlated && len(triggered) == 0:
			hints = append(hints, fmt.Sprintf("the correlation at %v has no triggering value in the time series: the value may be missing there, or the message came through the API", p.To.Format(time.RFC3339)))
		}
	}
	return hints
}

// evaluateEvents adds the evaluation of the current value to waiting device events (DBG-18);
// failures are reported per source and do not fail the state
func (this *Controller) evaluateEvents(ctx context.Context, state *model.DebugState) {
	if this.eventDebugging() != nil {
		return
	}
	var ownerToken string
	for i, w := range state.Waits {
		if w.Message == nil || len(w.Message.Events) == 0 {
			continue
		}
		message := *w.Message
		message.Events = append([]model.EventSource(nil), message.Events...)
		for j, source := range message.Events {
			message.Events[j].Current = this.currentEvaluation(ctx, state.Instance, source, &ownerToken)
		}
		state.Waits[i].Message = &message
	}
}

func (this *Controller) currentEvaluation(ctx context.Context, inst model.Instance, source model.EventSource, ownerToken *string) *model.EventEvaluation {
	if source.ImportId != "" {
		return &model.EventEvaluation{Error: "the current value of import events is not available"}
	}
	var d model.EventDescription
	found := false
	_ = this.store.Tx(ctx, func(tx *store.Tx) error {
		descriptions, err := tx.ListEventDescriptions(inst.DeploymentId)
		for _, candidate := range descriptions {
			if candidate.Id == source.EventDescriptionId {
				d, found = candidate, true
			}
		}
		return err
	})
	if !found {
		return &model.EventEvaluation{Error: "event description not found"}
	}
	if *ownerToken == "" {
		token, err := this.userToken(inst.Owner)
		if err != nil {
			return &model.EventEvaluation{Error: "no token of the owner: " + err.Error()}
		}
		*ownerToken = token
	}
	service, err := events.ServiceOf(d)
	if err != nil {
		return &model.EventEvaluation{Error: err.Error()}
	}
	row, err := this.series.Last(ctx, *ownerToken, d.DeviceId, d.ServiceId, events.Columns(service))
	if err != nil {
		return &model.EventEvaluation{Error: err.Error()}
	}
	if row == nil {
		return &model.EventEvaluation{Error: "no value stored yet"}
	}
	message := events.MessageOf(row.Values)
	evaluation, err := this.evaluator.Evaluate(d, message)
	at := row.Time
	result := &model.EventEvaluation{Time: &at, Raw: message, Value: evaluation.Value, Triggered: evaluation.Triggered, Logs: evaluation.Logs}
	if err != nil {
		result.Error = err.Error()
	}
	return result
}
