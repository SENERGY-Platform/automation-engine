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

package script

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

type boundItem struct {
	Id       string    `json:"id"`
	Optional []string  `json:"optional,omitempty"`
	Untagged string    // invisible, as on goja
	Hidden   string    `json:"-"`
	When     time.Time `json:"when"`
	boundEmbedded
}

type boundEmbedded struct {
	Promoted string `json:"promoted"`
}

type boundApi struct {
	calls []string
}

func (this *boundApi) Echo(name string, count int64, item boundItem) boundItem {
	this.calls = append(this.calls, name)
	item.Id = strings.Repeat(name, int(count))
	return item
}

func (this *boundApi) Join(parts ...any) string {
	result := []string{}
	for _, p := range parts {
		result = append(result, reflect.TypeOf(p).String())
	}
	return strings.Join(result, ",")
}

func (this *boundApi) Fail(message string) string {
	panic(errors.New(message))
}

func (this *boundApi) Checked() (string, error) {
	return "", errors.New("checked failed")
}

func (this *boundApi) Nothing() {}

func bound(t *testing.T) (*VM, *boundApi) {
	t.Helper()
	vm, err := NewVM(time.Second, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(vm.Close)
	api := &boundApi{}
	if err = vm.Bind("api", api); err != nil {
		t.Fatal(err)
	}
	return vm, api
}

// Module scripts call Go methods by their lower case name and see struct fields as goja showed
// them: under the json tag, every visible field present, untagged ones left out.
func TestBindCallsGoMethodsAsGojaDid(t *testing.T) {
	vm, api := bound(t)
	got, err := vm.Eval(`var r = api.echo("ab", 2, {id: "x", promoted: "p"}); [r.id, r.optional, r.promoted, "Untagged" in r, "Hidden" in r, typeof r.when, api.nothing()]`, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []any{"abab", nil, "p", false, false, "string", nil}) || len(api.calls) != 1 {
		t.Fatalf("%#v %v", got, api.calls)
	}
	// numbers to text and missing arguments to zero values, as JavaScript converts them
	if got, err = vm.Eval(`api.echo(5).id + "|" + api.echo("a", 2.7).id`, 0); err != nil || got != "|aa" {
		t.Fatalf("%#v %v", got, err)
	}
	// a function arrives as its text, as Go printed one on goja
	if got, err = vm.Eval(`api.join("a", 1, {b: 1}, [1], function () {})`, 0); err != nil || got != "string,float64,map[string]interface {},[]interface {},string" {
		t.Fatalf("%#v %v", got, err)
	}
}

func TestBindThrowsWhatTheMethodFailsWith(t *testing.T) {
	vm, _ := bound(t)
	got, err := vm.Eval(`var r = []; try { api.fail("boom") } catch (e) { r.push(e.message) } try { api.checked() } catch (e) { r.push(e.message) } r`, 0)
	if err != nil || !reflect.DeepEqual(got, []any{"boom", "checked failed"}) {
		t.Fatalf("%#v %v", got, err)
	}
	if _, err = vm.Eval(`api.fail("uncaught")`, 0); err == nil || !strings.Contains(err.Error(), "uncaught") {
		t.Fatalf("%v", err)
	}
}

func TestBoundNamespaceIsTheOnlyAddition(t *testing.T) {
	plain, err := NewVM(time.Second, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer plain.Close()
	vm, _ := bound(t)
	standard, err := plain.Eval(`Object.getOwnPropertyNames(globalThis)`, 0)
	if err != nil {
		t.Fatal(err)
	}
	got, err := vm.Eval(`Object.getOwnPropertyNames(globalThis)`, 0)
	if err != nil {
		t.Fatal(err)
	}
	added := []any{}
	for _, name := range got.([]any) {
		if !reflect.DeepEqual(name, "api") && !containsAny(standard.([]any), name) {
			added = append(added, name)
		}
	}
	if len(added) > 0 || containsAny(standard.([]any), "__host") {
		t.Fatalf("unexpected globals %v", added)
	}
}

func containsAny(list []any, value any) bool {
	for _, v := range list {
		if v == value {
			return true
		}
	}
	return false
}
