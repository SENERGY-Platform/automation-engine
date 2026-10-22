/*
 * Copyright (c) 2026 InfAI (CC SES)
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

package deprecated_test

import (
	"net/http"
	"testing"

	"github.com/SENERGY-Platform/automation-engine/pkg/client"
	"github.com/SENERGY-Platform/automation-engine/pkg/deprecated"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
)

var ops = client.TestToken("ops", "admin")

// An admin moves a process model to another owner, as with process-model-repository v1.3.0
// (SNRGY-4983): the id stays and the answer is the model in the old format. Only admins may,
// with an owner without surrounding whitespace. A GET of the path answers 405, which the
// transfer of platform-content takes as the endpoint being there (SNRGY-5010).
func TestAnAdminMovesAProcessModelToAnotherOwner(t *testing.T) {
	e := newEnv(t)
	var created struct {
		Id    string `json:"_id"`
		Owner string `json:"owner"`
	}
	e.call(alice, http.MethodPost, "/repository/processes", map[string]any{"name": "Heating", "bpmn_xml": waitingProcess(), "svgXML": "<svg/>"}, &created)
	path := "/repository/processes/" + created.Id + "/owner"

	if status, _ := e.call(ops, http.MethodGet, path, nil, nil); status != http.StatusMethodNotAllowed {
		t.Errorf("GET of the owner path: %v", status)
	}
	if status, _ := e.call(alice, http.MethodPut, path, map[string]string{"owner": "bob"}, nil); status != http.StatusForbidden {
		t.Errorf("the owner herself is no admin: %v", status)
	}
	if status, _ := e.call(ops, http.MethodPut, path, map[string]string{"owner": " bob"}, nil); status != http.StatusBadRequest {
		t.Errorf("owner with whitespace: %v", status)
	}
	if status, _ := e.call(ops, http.MethodPut, "/repository/processes/unknown/owner", map[string]string{"owner": "bob"}, nil); status != http.StatusNotFound {
		t.Errorf("unknown model: %v", status)
	}
	var moved struct {
		Id    string `json:"_id"`
		Owner string `json:"owner"`
		Name  string `json:"name"`
	}
	if status, _ := e.call(ops, http.MethodPut, path, map[string]string{"owner": "bob"}, &moved); status != http.StatusOK || moved.Id != created.Id || moved.Owner != "bob" || moved.Name != "Heating" {
		t.Fatalf("%v %+v", status, moved)
	}
	if status, _ := e.call(bob, http.MethodGet, "/repository/processes/"+created.Id, nil, &moved); status != http.StatusOK || moved.Owner != "bob" {
		t.Errorf("the new owner reads the model: %v %+v", status, moved)
	}
	if status, _ := e.call(ops, http.MethodPut, path, map[string]string{"owner": "bob"}, &moved); status != http.StatusOK || moved.Owner != "bob" {
		t.Errorf("a repeat changes nothing: %v %+v", status, moved)
	}
}

// An admin moves a design to another owner, as with smart-service-repository v0.7.0
// (SNRGY-4982): every release goes along with its id, the old one keeps its new_release_id, the
// instances stay with their owner.
func TestAnAdminMovesADesignWithItsReleasesToAnotherOwner(t *testing.T) {
	e := newEnv(t)
	design := e.createDesign(alice, "Room info", smartServiceDesign("widget"))
	e.createRelease(alice, design.Id, "first")
	// the current release has the id of the deployment, the older one <id>:v1
	current := e.createRelease(alice, design.Id, "second")
	instance := e.createInstance(alice, current.Id, "running")
	path := "/smart-services/designs/" + design.Id + "/owner"

	if status, _ := e.call(ops, http.MethodGet, path, nil, nil); status != http.StatusMethodNotAllowed {
		t.Errorf("GET of the owner path: %v", status)
	}
	if status, _ := e.call(alice, http.MethodPut, path, map[string]string{"owner": "bob"}, nil); status != http.StatusForbidden {
		t.Errorf("the owner herself is no admin: %v", status)
	}
	if status, _ := e.call(ops, http.MethodPut, "/smart-services/designs/unknown/owner", map[string]string{"owner": "bob"}, nil); status != http.StatusNotFound {
		t.Errorf("unknown design: %v", status)
	}
	var moved deprecated.SmartServiceDesign
	if status, _ := e.call(ops, http.MethodPut, path, map[string]string{"owner": "bob"}, &moved); status != http.StatusOK || moved.Id != design.Id || moved.UserId != "bob" {
		t.Fatalf("%v %+v", status, moved)
	}
	var releases []deprecated.SmartServiceRelease
	e.call(bob, http.MethodGet, "/smart-services/releases", nil, &releases)
	byId := map[string]deprecated.SmartServiceRelease{}
	for _, r := range releases {
		byId[r.Id] = r
	}
	latest, old := byId[current.Id], byId[current.Id+":v1"]
	if len(releases) != 2 || latest.Creator != "bob" || old.Creator != "bob" || latest.NewReleaseId != "" || old.NewReleaseId != current.Id {
		t.Errorf("both releases move with their ids, the old one points to the current one: %+v", releases)
	}
	// without permissions-v2 in the test, alice no longer reads the release; on the platform her
	// entry there stays, as with smart-service-repository
	stays, err := e.srv.Controller.GetInstance(ctx, ops, "smart-services", instance.Id)
	if err != nil || stays.Owner != "alice" {
		t.Errorf("the instance keeps its owner: %+v %v", stays, err)
	}
}

// A release of a third user under the design id refuses the move with 409 and moves nothing, as
// smart-service-repository did.
func TestADesignWithAReleaseOfAThirdUserIsNotMoved(t *testing.T) {
	e := newEnv(t)
	design := e.createDesign(alice, "Room info", smartServiceDesign("widget"))
	release := e.createRelease(alice, design.Id, "first")
	if err := e.srv.Controller.SetOwner(ctx, ops, "smart-services", model.KindDeployment, release.Id, model.OwnerInput{Owner: "carol"}); err != nil {
		t.Fatal(err)
	}
	if status, _ := e.call(ops, http.MethodPut, "/smart-services/designs/"+design.Id+"/owner", map[string]string{"owner": "bob"}, nil); status != http.StatusConflict {
		t.Fatalf("expected 409, got %v", status)
	}
	var unchanged deprecated.SmartServiceDesign
	if e.call(alice, http.MethodGet, "/smart-services/designs/"+design.Id, nil, &unchanged); unchanged.UserId != "alice" {
		t.Errorf("the design stays with its owner: %+v", unchanged)
	}
}
