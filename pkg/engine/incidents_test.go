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
	"strings"
	"testing"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/model"
)

func TestIncidentWithoutRestartKeepsTheInstanceStopped(t *testing.T) {
	run(t, func(t *testing.T, h *harness) {
		inst := h.start(h.deployWith("processes", failingProcess(), model.IncidentHandling{Notify: true}), nil)
		h.fail(inst.Id)
		h.advance(time.Hour)
		if got := h.expectState(inst.Id, model.StateIncident); got.Run != 1 || got.RestartAt != nil {
			t.Fatalf("expected the first run to stay stopped without a restart, got %+v", got)
		}
	})
}

func TestIncidentWithoutNotifySendsNoNotification(t *testing.T) {
	run(t, func(t *testing.T, h *harness) {
		inst := h.start(h.deployWith("processes", failingProcess(), model.IncidentHandling{}), nil)
		h.fail(inst.Id)
		h.advance(time.Second)
		if sent := h.notifier.messages(); len(sent) != 0 {
			t.Fatalf("expected no notification, got %+v", sent)
		}
	})
}

func TestIncidentNotificationWithoutRestartNamesTheErrorOnly(t *testing.T) {
	run(t, func(t *testing.T, h *harness) {
		inst := h.start(h.deployWith("processes", failingProcess(), model.IncidentHandling{Notify: true}), nil)
		h.fail(inst.Id)
		h.advance(time.Second)
		sent := h.notifier.messages()
		if len(sent) != 1 || sent[0].UserId != "user" || !strings.Contains(sent[0].Message, "missing") || strings.Contains(sent[0].Message, "restarted") {
			t.Fatalf("expected one notification with the error and without a restart notice, got %+v", sent)
		}
	})
}
