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

//go:generate go run github.com/swaggo/swag/cmd/swag@v1.16.4 init --parseDependency --parseInternal -g api.go -d ./pkg/api,./pkg/model -o ./docs --outputTypes json,yaml,go

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
	"github.com/SENERGY-Platform/automation-engine/pkg/server"
)

func main() {
	configPath := flag.String("config", "config.json", "configuration file")
	flag.Parse()

	config, err := configuration.Load(*configPath)
	if err != nil {
		slog.Error("invalid configuration", "error", err)
		os.Exit(1)
	}
	logger := config.Logger()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

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
