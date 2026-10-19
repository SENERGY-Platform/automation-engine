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

package client_test

import (
	"context"
	"errors"
	"testing"

	b "github.com/SENERGY-Platform/automation-engine/pkg/bpmn/bpmntest"
	"github.com/SENERGY-Platform/automation-engine/pkg/client"
	"github.com/SENERGY-Platform/automation-engine/pkg/configuration"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/server"
)

// With MIN_TIMER_DURATION, a deploy rejects a shorter timer of the model and one a deploy
// parameter sets, as process-deployment did with 5 s.
func TestDeployRejectsTimersBelowTheConfiguredMinimum(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	srv, err := client.NewTestServer(ctx, func(config *configuration.Config, _ *server.Dependencies) { config.MinTimerDuration = "5s" })
	if err != nil {
		t.Fatal(err)
	}
	process := func(duration string) string {
		return b.Process(b.Start("start"), b.CatchTimer("pause", "timeDuration", duration), b.End("end"), b.Flow("start", "pause"), b.Flow("pause", "end"))
	}
	if _, err := srv.Controller.CreateDeployment(ctx, alice, ns, model.DeploymentInput{Xml: process("PT2S")}); !errors.Is(err, model.ErrValidation) {
		t.Errorf("a model timer below the minimum is a validation error, got %v", err)
	}
	_, err = srv.Controller.CreateDeployment(ctx, alice, ns, model.DeploymentInput{Xml: process("PT1H"), Parameters: map[string]model.ElementParameters{"pause": {Timer: "PT2S"}}})
	if !errors.Is(err, model.ErrValidation) {
		t.Errorf("a deploy parameter below the minimum is a validation error, got %v", err)
	}
	if _, err := srv.Controller.CreateDeployment(ctx, alice, ns, model.DeploymentInput{Xml: process("PT1H"), Parameters: map[string]model.ElementParameters{"pause": {Timer: "PT5S"}}}); err != nil {
		t.Errorf("the minimum itself is valid: %v", err)
	}
}
