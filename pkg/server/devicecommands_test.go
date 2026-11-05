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

package server

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/configuration"
	"github.com/SENERGY-Platform/automation-engine/pkg/tasks/devicecommand"
	devicerepositoryclient "github.com/SENERGY-Platform/device-repository/v3/lib/client"
)

// the device command type is never called here; nil methods would panic
type unusedRepo struct {
	devicerepositoryclient.Interface
}
type unusedMarshaller struct{ devicecommand.Marshaller }
type unusedProducer struct{ devicecommand.Producer }

// GROUP_SCHEDULER and GROUP_CONCURRENCY reach the device command type (SNRGY-5093)
func TestDeviceCommandsTakeTheGroupSettings(t *testing.T) {
	config := configuration.Default()
	config.GroupScheduler = "sequential"
	config.GroupConcurrency = 3
	deps := Dependencies{Marshaller: unusedMarshaller{}, Producer: unusedProducer{}}
	commands, err := deviceCommands(context.Background(), config, deps, nil, nil, unusedRepo{}, time.Minute, nil, nil, NewMetrics(time.Hour), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil || commands == nil {
		t.Fatal(commands, err)
	}
	if got := commands.Config(); got.GroupScheduler != "sequential" || got.GroupConcurrency != 3 {
		t.Fatalf("unexpected %v, %v", got.GroupScheduler, got.GroupConcurrency)
	}
}
