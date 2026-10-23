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
	"testing"
)

// A gateway module offers the storage budget readable; a number of bytes still works.
func TestParseByteSize(t *testing.T) {
	for text, expected := range map[string]ByteSize{
		"1073741824": 1 << 30,
		"0":          0,
		"-1":         -1,
		"1GiB":       1 << 30,
		"1 GiB":      1 << 30,
		"1.5gib":     3 << 29,
		"512MiB":     512 << 20,
		"500MB":      500_000_000,
		"2GB":        2_000_000_000,
		"64kB":       64_000,
		"100B":       100,
		" 2TiB ":     2 << 40,
	} {
		if got, err := ParseByteSize(text); err != nil || got != expected {
			t.Errorf("%q: %v %v", text, got, err)
		}
	}
	for _, text := range []string{"", "GiB", "1 GiBs", "1 Gb2", "1e3", "x", "99999999TiB"} {
		if got, err := ParseByteSize(text); err == nil {
			t.Errorf("%q: expected error, got %v", text, got)
		}
	}
}

func TestStorageBudgetFromEnvironmentAndJson(t *testing.T) {
	t.Setenv("STORAGE_BUDGET", "2GiB")
	config, err := Load("")
	if err != nil || config.StorageBudget != 2<<30 {
		t.Fatalf("%v %v", config.StorageBudget, err)
	}
	var fromJson struct {
		Number ByteSize `json:"number"`
		Text   ByteSize `json:"text"`
	}
	if err = json.Unmarshal([]byte(`{"number": 1024, "text": "1KiB"}`), &fromJson); err != nil || fromJson.Number != 1024 || fromJson.Text != 1024 {
		t.Fatalf("%+v %v", fromJson, err)
	}
	t.Setenv("STORAGE_BUDGET", "lots")
	if _, err = Load(""); err == nil {
		t.Fatal("an invalid size fails the start")
	}
}
