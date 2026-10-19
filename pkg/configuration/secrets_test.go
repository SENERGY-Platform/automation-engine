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
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// event-worker v1.2.0 TestConfigFormattingMasksSecrets: a configuration that reaches a log
// does not show its secrets.

var secrets = map[string]string{
	"AUTH_CLIENT_SECRET": "client-s3cr3t",
	"POSTGRES_DSN":       "postgres://user:pg-s3cr3t@db/engine",
	"MQTT_PASSWORD":      "mqtt-s3cr3t",
}

func loadWithSecrets(t *testing.T) Config {
	t.Helper()
	for key, value := range secrets {
		t.Setenv(key, value)
	}
	config, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if config.AuthClientSecret.Value() != secrets["AUTH_CLIENT_SECRET"] {
		t.Fatalf("the secret is read: %v", config.AuthClientSecret.Value())
	}
	return config
}

func assertMasked(t *testing.T, outputs map[string]string) {
	t.Helper()
	for format, output := range outputs {
		for _, secret := range secrets {
			if strings.Contains(output, secret) {
				t.Errorf("%v shows %v", format, secret)
			}
		}
	}
}

func TestConfigFormattingMasksSecrets(t *testing.T) {
	config := loadWithSecrets(t)
	raw, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	assertMasked(t, map[string]string{
		"json":   string(raw),
		"%v":     fmt.Sprintf("%v", config),
		"%+v":    fmt.Sprintf("%+v", config),
		"ptr %v": fmt.Sprintf("%v", &config),
	})
}
