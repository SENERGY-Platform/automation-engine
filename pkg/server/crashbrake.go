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
	"log/slog"

	"github.com/SENERGY-Platform/automation-engine/pkg/configuration"
	"github.com/SENERGY-Platform/automation-engine/pkg/crashbrake"
	"github.com/SENERGY-Platform/automation-engine/pkg/engine"
)

// openCrashBrake opens the crash brake in SCRIPT_CRASH_DIR, stops what ended the previous process
// with a fatal error and gives the brake to the engine (EXPR-03). It runs before anything evaluates
// a script. A brake that cannot be opened leaves scripts unprotected rather than the engine down.
func openCrashBrake(ctx context.Context, config configuration.Config, e *engine.Engine, logger *slog.Logger) *crashbrake.Brake {
	brake, decisions, err := crashbrake.Open(config.ScriptCrashDir, logger)
	if err != nil {
		logger.Error("crash brake", "dir", config.ScriptCrashDir, "error", err)
	}
	if len(decisions) > 0 {
		brake.Applied(e.ApplyCrashDecisions(ctx, decisions))
	}
	e.SetCrashBrake(brake)
	return brake
}

// closeCrashBrake records a deliberate stop, so that the next start finds no crash
func closeCrashBrake(brake *crashbrake.Brake) {
	brake.Shutdown()
	brake.Close()
}
