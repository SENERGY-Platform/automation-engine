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

package fogsync_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/SENERGY-Platform/automation-engine/pkg/configuration"
	"github.com/SENERGY-Platform/automation-engine/pkg/engine"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
)

func rejectedFor(t *testing.T, what string, err error, namespace string) {
	t.Helper()
	if !errors.Is(err, model.ErrBadRequest) || !strings.Contains(err.Error(), namespace) {
		t.Errorf("%v is rejected with 400 naming the namespace %v: %v", what, namespace, err)
	}
}

// Smart services run on the platform only, the processes they deploy may run on gateways (NS-10).
func TestSmartServicesAreNotDeployedToGateways(t *testing.T) {
	e := newEnv(t)
	c := e.cloud.Controller
	_, err := c.CreateDeployment(ctx, alice, "smart-services", model.DeploymentInput{Xml: waitProcess(), NetworkId: network})
	rejectedFor(t, "a smart service for a gateway", err, "smart-services")
	_, err = c.PutDeployment(ctx, alice, "smart-services", "sms-1", model.DeploymentInput{Xml: waitProcess(), NetworkId: network})
	rejectedFor(t, "a smart service for a gateway with an id", err, "smart-services")
	_, err = c.PrepareDeployment(ctx, alice, "smart-services", model.PrepareInput{Xml: waitProcess(), NetworkId: network})
	rejectedFor(t, "preparing a smart service for a gateway", err, "smart-services")
	if _, err = c.CreateDeployment(ctx, alice, "smart-services", model.DeploymentInput{Xml: waitProcess()}); err != nil {
		t.Errorf("in the cloud a smart service deploys: %v", err)
	}

	d, err := c.CreateDeployment(ctx, alice, "processes", model.DeploymentInput{Xml: waitProcess(), NetworkId: network})
	if err != nil {
		t.Fatalf("processes may go to a gateway: %v", err)
	}
	e.waitFor("the process on the gateway", func() bool { _, err := e.gatewayDeployment(d.Id); return err == nil })
	if _, err = c.PrepareDeployment(ctx, alice, "processes", model.PrepareInput{Xml: waitProcess(), NetworkId: network}); err != nil {
		t.Errorf("processes are prepared for a gateway: %v", err)
	}
}

func TestANamespaceIsKeptFromGatewaysOnlyIfConfigured(t *testing.T) {
	lab := configuration.DefaultNamespaces()[0]
	lab.Name, lab.CloudOnly = "lab", true
	open := configuration.DefaultNamespaces()[0]
	open.Name = "open"
	e := newEnv(t, func(c *configuration.Config) { c.Namespaces = append(c.Namespaces, lab, open) })
	c := e.cloud.Controller
	_, err := c.CreateDeployment(ctx, alice, "lab", model.DeploymentInput{Xml: waitProcess(), NetworkId: network})
	rejectedFor(t, "a deployment of a cloud only namespace for a gateway", err, "lab")
	_, err = c.PrepareDeployment(ctx, alice, "lab", model.PrepareInput{Xml: waitProcess(), NetworkId: network})
	rejectedFor(t, "preparing a deployment of a cloud only namespace for a gateway", err, "lab")
	if _, err = c.CreateDeployment(ctx, alice, "open", model.DeploymentInput{Xml: waitProcess(), NetworkId: network}); err != nil {
		t.Errorf("a namespace without cloud_only may go to gateways: %v", err)
	}
}

// A preference for the gateway is no demand: a process_deployment into a namespace that runs
// only in the cloud deploys there (NS-08, NS-10).
func TestAProcessDeploymentStaysInTheCloudIfItsNamespaceRunsOnlyThere(t *testing.T) {
	e, values := deployPreferringTheGateway(t, func(c *configuration.Config) {
		for i := range c.Namespaces {
			if c.Namespaces[i].Name == "processes" {
				c.Namespaces[i].CloudOnly = true
			}
		}
	})
	if values["fog"] != false || values["hub"] != "" {
		t.Fatalf("the module reports the cloud: %v", values)
	}
	deploymentId, _ := values["deployment"].(string)
	d, err := e.cloud.Controller.GetDeployment(ctx, alice, "processes", deploymentId)
	if err != nil || d.NetworkId != "" {
		t.Errorf("the deployment is one of the cloud: %+v %v", d, err)
	}
}

// As with other rules of a namespace (NS-03), a change does not check existing deployments
// again: those on gateways keep running, start, stop and delete, but are not updated.
func TestANamespaceThatBecomesCloudOnlyKeepsItsGatewayDeployments(t *testing.T) {
	e := newEnv(t)
	c := e.cloud.Controller
	d, err := c.CreateDeployment(ctx, alice, "processes", model.DeploymentInput{Xml: waitProcess(), NetworkId: network})
	if err != nil {
		t.Fatal(err)
	}
	e.waitFor("the deployment on the gateway", func() bool { _, err := e.gatewayDeployment(d.Id); return err == nil })

	ns, err := c.GetNamespace(ctx, admin, "processes")
	if err != nil {
		t.Fatal(err)
	}
	ns.CloudOnly = true
	if _, err = c.PutNamespace(ctx, admin, "processes", ns); err != nil {
		t.Fatalf("the change is allowed: %v", err)
	}
	// a gateway that gets such a namespace anyway keeps running its deployments
	if _, _, err = e.gateway.Namespaces.Put(ctx, ns); err != nil {
		t.Fatal(err)
	}

	_, err = c.PutDeployment(ctx, alice, "processes", d.Id, model.DeploymentInput{Xml: waitProcess(), Name: "renamed", UpdateStrategy: model.UpdateDrain})
	rejectedFor(t, "an update of a gateway deployment", err, "processes")
	inst, err := c.StartInstance(ctx, alice, "processes", model.StartInstanceInput{DeploymentId: d.Id})
	if err != nil {
		t.Fatalf("the deployment still starts: %v", err)
	}
	e.waitFor("the running instance", func() bool { return e.cloudInstance(inst.Id).State == model.StateRunning })
	if _, err = e.gateway.Engine.Correlate(ctx, engine.CorrelationRequest{MessageInput: model.MessageInput{Name: "go", InstanceId: inst.Id}}); err != nil {
		t.Fatal(err)
	}
	e.waitFor("the completed instance", func() bool { return e.cloudInstance(inst.Id).State == model.StateCompleted })
	if err = c.DeleteDeployment(ctx, alice, "processes", d.Id); err != nil {
		t.Fatal(err)
	}
	e.waitFor("the deletion on the gateway", func() bool {
		_, err := e.gatewayDeployment(d.Id)
		return errors.Is(err, model.ErrNotFound)
	})
	_, err = c.CreateDeployment(ctx, alice, "processes", model.DeploymentInput{Xml: waitProcess(), NetworkId: network})
	rejectedFor(t, "a new gateway deployment", err, "processes")
	var onGateways []string
	_ = e.cloud.Store.Tx(ctx, func(tx *store.Tx) (err error) { onGateways, err = tx.GatewayDeploymentIds("processes"); return err })
	if len(onGateways) != 0 {
		t.Errorf("nothing is left on the gateway: %v", onGateways)
	}
}
