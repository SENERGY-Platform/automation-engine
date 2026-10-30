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

package main

//go:generate go run github.com/swaggo/swag/cmd/swag@v1.16.4 init --parseDependency --parseInternal -g api.go -d ./pkg/api,./pkg/model,./pkg/deprecated -o ./docs --outputTypes json,yaml,go
//go:generate go run ./docs/asyncapi-gen -o docs/asyncapi.json

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	// timer time zones work without tzdata in the image
	_ "time/tzdata"

	"github.com/SENERGY-Platform/automation-engine/pkg/configuration"
	"github.com/SENERGY-Platform/automation-engine/pkg/migrate"
	"github.com/SENERGY-Platform/automation-engine/pkg/server"
	"github.com/SENERGY-Platform/automation-engine/pkg/tracing"
	"github.com/SENERGY-Platform/automation-engine/pkg/transfer"
)

func main() {
	// automation-engine migrate [flags]: moves the data of the Camunda based services (MIG-01)
	if len(os.Args) > 1 && os.Args[1] == "migrate" {
		os.Exit(migrate.Main(os.Args[2:], os.Stdout, os.Stderr))
	}
	// automation-engine export-gateway [-sqlite <path>]: the deployments of a gateway for the
	// import into a new cloud (SNRGY-5066)
	if len(os.Args) > 1 && os.Args[1] == "export-gateway" {
		os.Exit(transfer.Main(os.Args[2:], os.Stdout, os.Stderr))
	}
	configPath := flag.String("config", "config.json", "configuration file")
	flag.Parse()
	// an unknown subcommand would otherwise start a complete engine that does not end
	if flag.NArg() > 0 {
		slog.Error("unknown subcommand, expected migrate or export-gateway", "args", flag.Args())
		os.Exit(2)
	}

	config, err := configuration.Load(*configPath)
	if err != nil {
		slog.Error("invalid configuration", "error", err)
		os.Exit(1)
	}
	logger := config.Logger()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err = tracing.Init(ctx, config.OtelEndpoint); err != nil {
		logger.Warn("unable to init open-telemetry, continuing without tracing", "error", err)
	}

	if config.Role == "event" {
		worker, err := server.NewEventServer(ctx, config, logger, server.EventDependencies{})
		if err != nil {
			logger.Error("unable to start event worker", "error", err)
			os.Exit(1)
		}
		defer worker.Close()
		if err = worker.Run(ctx); err != nil {
			logger.Error("event worker stopped with error", "error", err)
			os.Exit(1)
		}
		return
	}

	srv, err := server.New(ctx, config, logger, server.Dependencies{})
	if err != nil {
		logger.Error("unable to start", "error", err)
		os.Exit(1)
	}
	defer srv.Close()
	if err = srv.Run(ctx); err != nil {
		logger.Error("stopped with error", "error", err)
		os.Exit(1)
	}
}
