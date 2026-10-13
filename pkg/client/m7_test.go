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
	"strings"
	"testing"

	b "github.com/SENERGY-Platform/automation-engine/pkg/bpmn/bpmntest"
	"github.com/SENERGY-Platform/automation-engine/pkg/client"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
)

// A model of the Camunda engine is rewritten on request and then deploys; nothing is stored
// by the conversion itself (MIG-04).
func TestCamundaModelIsConvertedAndDeploys(t *testing.T) {
	each(t, func(t *testing.T, c client.Client) {
		old := b.Process(
			b.Start("start"),
			`<bpmn:scriptTask id="log" scriptFormat="groovy"><bpmn:script>println "started"</bpmn:script></bpmn:scriptTask>`,
			`<bpmn:intermediateCatchEvent id="wait"><bpmn:timerEventDefinition><bpmn:timeDuration xsi:type="bpmn:tFormalExpression">PT${seconds}S</bpmn:timeDuration></bpmn:timerEventDefinition></bpmn:intermediateCatchEvent>`,
			`<bpmn:serviceTask id="mail"><bpmn:extensionElements><camunda:connector><camunda:connectorId>mail-send</camunda:connectorId></camunda:connector></bpmn:extensionElements></bpmn:serviceTask>`,
			b.End("end"),
			b.Flow("start", "log"), b.Flow("log", "wait"), b.Flow("wait", "mail"), b.Flow("mail", "end"),
		)
		conversion, err := c.Convert(ctx, alice, ns, old)
		if err != nil {
			t.Fatal(err)
		}
		if len(conversion.Rewritten) != 2 || len(conversion.Unsupported) != 1 || !strings.Contains(conversion.Unsupported[0].Message, "mail-send") {
			t.Fatalf("groovy and timer are rewritten, mail is not: %+v", conversion)
		}
		if len(conversion.Issues) == 0 || conversion.Issues[0].ElementId != "mail" {
			t.Fatalf("the result is validated against the namespace: %+v", conversion.Issues)
		}
		models, err := c.ListModels(ctx, alice, ns, model.ModelListOptions{})
		if err != nil || models.Total != 0 {
			t.Fatalf("nothing is stored: %+v %v", models, err)
		}
		without := strings.Replace(conversion.Xml, `<bpmn:serviceTask id="mail">`, `<bpmn:task id="mail">`, 1)
		without = strings.Replace(without, `</camunda:connector></bpmn:extensionElements></bpmn:serviceTask>`, `</camunda:connector></bpmn:extensionElements></bpmn:task>`, 1)
		again, err := c.Convert(ctx, alice, ns, without)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = c.CreateDeployment(ctx, alice, ns, model.DeploymentInput{Xml: strings.Replace(again.Xml, `<camunda:connector><camunda:connectorId>mail-send</camunda:connectorId></camunda:connector>`, "", 1)}); err != nil {
			t.Fatalf("the rewritten model deploys once the mail task is replaced: %v", err)
		}
	})
}
