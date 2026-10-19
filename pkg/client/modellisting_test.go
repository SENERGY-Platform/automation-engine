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

package client_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/SENERGY-Platform/automation-engine/pkg/client"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
)

// namedModels creates models of alice with the given names and descriptions, in this order
func namedModels(t *testing.T, c client.Client, namesAndDescriptions ...[2]string) []model.Model {
	t.Helper()
	result := []model.Model{}
	for _, nd := range namesAndDescriptions {
		m, err := c.CreateModel(ctx, alice, ns, model.ModelInput{Name: nd[0], Description: nd[1], Xml: waitingModel()})
		if err != nil {
			t.Fatal(err)
		}
		result = append(result, m)
	}
	return result
}

func modelNames(page model.Page[model.Model]) []string {
	names := []string{}
	for _, m := range page.Items {
		names = append(names, m.Name)
	}
	return names
}

func TestModelsAreListedByIds(t *testing.T) {
	each(t, func(t *testing.T, c client.Client) {
		m := namedModels(t, c, [2]string{"a 1", "x"}, [2]string{"b 1", "y"}, [2]string{"a 2", "y"})
		page, err := c.ListModels(ctx, alice, ns, model.ModelListOptions{Ids: []string{m[1].Id, m[2].Id}})
		if got := modelNames(page); err != nil || page.Total != 2 || !reflect.DeepEqual(got, []string{"a 2", "b 1"}) {
			t.Fatalf("expected a 2 and b 1: %v %v %v", got, page.Total, err)
		}
	})
}

func TestModelsSortedByNameDescendingArePagedWithTotal(t *testing.T) {
	each(t, func(t *testing.T, c client.Client) {
		namedModels(t, c, [2]string{"a 1", "x"}, [2]string{"b 1", "y"}, [2]string{"a 2", "y"}, [2]string{"b 2", "x"})
		page, err := c.ListModels(ctx, alice, ns, model.ModelListOptions{Sort: "name.desc", PageOptions: model.PageOptions{Limit: 2, Offset: 1}})
		if got := modelNames(page); err != nil || page.Total != 4 || !reflect.DeepEqual(got, []string{"b 1", "a 2"}) {
			t.Fatalf("expected b 1 and a 2 of 4: %v %v %v", got, page.Total, err)
		}
	})
}

// Deleting a user removes the user's models; models of other users stay (MOD-08, SEC-08).
func TestDeletedUserTakesTheirModelsAlong(t *testing.T) {
	eachEnv(t, func(t *testing.T, e env) {
		alices, err := e.CreateModel(ctx, alice, ns, model.ModelInput{Xml: waitingModel()})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = e.CreateModel(ctx, bob, ns, model.ModelInput{Xml: waitingModel()}); err != nil {
			t.Fatal(err)
		}
		e.sync(t)
		if err = e.DeleteUser(ctx, admin, "alice"); err != nil {
			t.Fatal(err)
		}
		if _, err = e.GetModel(ctx, admin, ns, alices.Id); !errors.Is(err, model.ErrNotFound) {
			t.Errorf("expected alice's model to be deleted, got %v", err)
		}
		if page, err := e.ListModels(ctx, bob, ns, model.ModelListOptions{}); err != nil || page.Total != 1 {
			t.Errorf("expected bob's model to stay: %+v %v", page, err)
		}
	})
}
