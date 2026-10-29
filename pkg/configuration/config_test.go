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

func TestValidatePprofPort(t *testing.T) {
	config := Default()
	if config.PprofPort != 0 {
		t.Fatal("pprof must be off by default")
	}
	for _, port := range []int{-1, 65536, config.ServerPort, config.InternalPort} {
		config.PprofPort = port
		if err := config.validate(); err == nil {
			t.Fatalf("expected error for pprof_port %v", port)
		}
	}
	config.PprofPort = 6060
	if err := config.validate(); err != nil {
		t.Fatal(err)
	}
}

func TestValidateRejectsVerifyTokensWithoutCertUrl(t *testing.T) {
	config := Default()
	config.VerifyTokens = true
	if err := config.validate(); err == nil {
		t.Fatal("verifying every token needs the certificates of Keycloak")
	}
	config.KeycloakCertUrl = "http://keycloak:8080/auth/realms/master/protocol/openid-connect/certs"
	if err := config.validate(); err != nil {
		t.Fatal(err)
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

// The gateway module sets the HTTP policy of the gateway as comma separated lists (NS-04).
func TestLocalHttpPolicyFromEnvironment(t *testing.T) {
	t.Setenv("MODE", "fog")
	t.Setenv("MQTT_BROKER", "tcp://broker:1881")
	t.Setenv("HTTP_ENABLED", "true")
	t.Setenv("HTTP_ALLOW_HOSTS", "api.sunrise-sunset.org, *.example.org")
	t.Setenv("HTTP_ALLOW_NETWORKS", "192.168.0.0/16,10.0.0.0/8")
	t.Setenv("HTTP_ALLOW_PORTS", "80,443")
	config, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	policy, local, err := config.LocalHttpPolicy()
	if err != nil || !local {
		t.Fatalf("local %v, %v", local, err)
	}
	if !policy.Enabled || len(policy.AllowHosts) != 2 || policy.AllowHosts[1] != "*.example.org" || len(policy.AllowNetworks) != 2 || len(policy.AllowPorts) != 2 || policy.AllowPorts[1] != 443 {
		t.Fatalf("unexpected %+v", policy)
	}
	if policy.Timeout != "10s" || policy.MaxResponseBytes != 1<<20 {
		t.Fatalf("defaults missing: %+v", policy)
	}
}

func TestLocalHttpPolicyUnsetKeepsNamespaces(t *testing.T) {
	if _, local, err := Default().LocalHttpPolicy(); local || err != nil {
		t.Fatalf("local %v, %v", local, err)
	}
}

func TestValidateRejectsInvalidLocalHttpPolicy(t *testing.T) {
	for name, change := range map[string]func(*Config){
		"in the cloud":          func(c *Config) { c.HttpEnabled = "true" },
		"list without enabled":  func(c *Config) { c.Mode = "fog"; c.HttpAllowHosts = "example.org" },
		"no boolean":            func(c *Config) { c.Mode = "fog"; c.HttpEnabled = "yes please" },
		"invalid network":       func(c *Config) { c.Mode = "fog"; c.HttpEnabled = "true"; c.HttpAllowNetworks = "192.168.0.0" },
		"invalid port":          func(c *Config) { c.Mode = "fog"; c.HttpEnabled = "true"; c.HttpAllowPorts = "80,https" },
		"invalid timeout":       func(c *Config) { c.Mode = "fog"; c.HttpEnabled = "true"; c.HttpTimeout = "10" },
		"negative response cap": func(c *Config) { c.Mode = "fog"; c.HttpEnabled = "true"; c.HttpMaxResponseBytes = -1 },
	} {
		config := Default()
		config.MqttBroker = "tcp://broker:1881"
		change(&config)
		if err := config.validate(); err == nil {
			t.Errorf("%v: expected error", name)
		}
	}
}

func TestValidateAcceptsNetworkInfoUrlOnlyOnAGateway(t *testing.T) {
	config := Default()
	config.NetworkInfoUrl = "http://gateway:8080/api/network"
	if err := config.validate(); err == nil {
		t.Error("the cloud reads the owners of networks from device-repository (FOG-11)")
	}
	config.Mode, config.MqttBroker = "fog", "tcp://broker:1881"
	if err := config.validate(); err != nil {
		t.Errorf("a gateway reads the owner of its network from the cloud proxy: %v", err)
	}
}
