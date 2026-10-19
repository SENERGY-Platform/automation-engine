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
	"testing"

	"github.com/SENERGY-Platform/automation-engine/pkg/model"
)

// Deleting a user removes the deployments of that user and keeps those of every other user
// (process-deployment TestUserDelete).
func TestUserDeletionKeepsTheDeploymentsOfOtherUsers(t *testing.T) {
	eachEnv(t, func(t *testing.T, e env) {
		deploy := func(token string) model.Deployment {
			t.Helper()
			d, err := e.CreateDeployment(ctx, token, ns, model.DeploymentInput{Xml: waitingModel()})
			if err != nil {
				t.Fatal(err)
			}
			return d
		}
		alices := []model.Deployment{deploy(alice), deploy(alice)}
		bobs := []model.Deployment{deploy(bob), deploy(bob)}
		e.sync(t)
		if err := e.DeleteUser(ctx, admin, "alice"); err != nil {
			t.Fatal(err)
		}
		for _, d := range alices {
			if _, err := e.GetDeployment(ctx, admin, ns, d.Id); !errors.Is(err, model.ErrNotFound) {
				t.Errorf("a deployment of the deleted user is removed, got %v", err)
			}
		}
		for _, d := range bobs {
			if _, err := e.GetDeployment(ctx, bob, ns, d.Id); err != nil {
				t.Errorf("a deployment of another user stays: %v", err)
			}
		}
	})
}
