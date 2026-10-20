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

package rewrite

import (
	"fmt"
	"slices"

	"github.com/SENERGY-Platform/automation-engine/pkg/bpmn"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
)

// Checked is a model as the engine runs it: converted where it is written for Camunda 7 or the
// old designer, then validated under the rules of a namespace.
type Checked struct {
	// the BPMN that runs; the given one if it is in the format of the engine already
	Xml     string
	Process *bpmn.Process
	// what keeps the model from deploying: the parts the conversion cannot rewrite, then the
	// issues of the converted model
	Issues []model.Issue
	Report Report
}

// Check converts a model on the fly, as deploy, prepare and the validation of models do, and
// validates the result. Stored models stay as they were submitted, because the designer of the
// web-ui edits only its own format. An error means the XML could not be read.
func Check(xml string, rules bpmn.Rules) (Checked, error) {
	result := Checked{Xml: xml}
	// XML the rewrite cannot read is reported by the parser, as without conversion
	if converted, report, err := Rewrite(xml); err == nil {
		result.Xml, result.Report = converted, report
	}
	process, issues, err := bpmn.ParseAndValidate(result.Xml, rules)
	if err != nil {
		return result, err
	}
	result.Process = process
	result.Issues = merge(result.Report.Unsupported, issues)
	return result, nil
}

// merge puts the parts the conversion cannot rewrite first; an issue of the converted model on
// an element they already name is left out, the conversion names the cause
func merge(unsupported []model.Issue, issues []model.Issue) []model.Issue {
	result := append([]model.Issue{}, unsupported...)
	for _, issue := range issues {
		named := slices.ContainsFunc(unsupported, func(u model.Issue) bool {
			return u == issue || (issue.ElementId != "" && u.ElementId == issue.ElementId)
		})
		if !named {
			result = append(result, issue)
		}
	}
	return result
}

// Hints say on a save or a validation that the model is converted when deployed and name what
// to check then; they do not keep the model from deploying.
func (this Checked) Hints() []model.Issue {
	if this.Report.Empty() {
		return nil
	}
	hints := []model.Issue{{Hint: true, Message: fmt.Sprintf("the model is in the format of Camunda 7 or the old designer; it is stored as it is "+
		"and converted when deployed: %v parts rewritten, %v to check", len(this.Report.Rewritten), len(this.Report.Check))}}
	for _, c := range this.Report.Check {
		hints = append(hints, model.Issue{ElementId: c.ElementId, Message: "check after the conversion: " + c.Message, Hint: true})
	}
	return hints
}
