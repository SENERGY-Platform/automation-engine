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

package configuration

import (
	"testing"
)

func TestDefaultIsValid(t *testing.T) {
	config := Default()
	if err := config.validate(); err != nil {
		t.Fatal(err)
	}
	if config.Mode != "cloud" || config.Role != "engine" || !config.ModifiedDevices || config.MaxIoValuesPerUser == 0 {
		t.Fatalf("unexpected defaults %+v", config)
	}
}

func TestLoadReadsEnvironment(t *testing.T) {
	t.Setenv("ROLE", "event")
	t.Setenv("KAFKA_URL", "kafka:9092")
	config, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if config.Role != "event" || config.KafkaUrl != "kafka:9092" || len(config.Namespaces) != 2 {
		t.Fatalf("unexpected %+v", config)
	}
}

func TestValidateRejectsUnknownRole(t *testing.T) {
	config := Default()
	config.Role = "worker"
	if err := config.validate(); err == nil {
		t.Fatal("expected error")
	}
}

func TestValidateRejectsCorsOriginWithPath(t *testing.T) {
	for _, origin := range []string{"https://ui.example.org/", "ui.example.org", "https://ui.example.org/app"} {
		config := Default()
		config.CorsAllowedOrigins = "https://ok.example.org, " + origin
		if err := config.validate(); err == nil {
			t.Errorf("%v: expected error", origin)
		}
	}
}

func TestValidateAcceptsCorsOrigins(t *testing.T) {
	config := Default()
	config.CorsAllowedOrigins = "https://ui.example.org, http://localhost:4200,*"
	if err := config.validate(); err != nil {
		t.Fatal(err)
	}
	if origins := List(config.CorsAllowedOrigins); len(origins) != 3 || origins[1] != "http://localhost:4200" {
		t.Fatalf("got %q", origins)
	}
}

// Smart services may loop in their sequence flow like processes (decided 2026-10-22); a
// namespace that wants to forbid it sets forbid_cycles itself.
func TestDefaultNamespacesAllowLoops(t *testing.T) {
	for _, ns := range DefaultNamespaces() {
		if ns.ForbidCycles {
			t.Errorf("namespace %v forbids loops", ns.Name)
		}
	}
}
