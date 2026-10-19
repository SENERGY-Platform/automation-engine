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

package deprecated

import (
	"github.com/beevik/etree"
)

// emptyTimerPlaceholder stands in for a timer the old deploy dialog fills in, so that the
// rest of the model can be prepared
const emptyTimerPlaceholder = "PT1H"

// fillEmptyTimers gives every timer the old designer left without definition a duration:
// the value of its element in values, else fallback; without either, it stays empty. The ids
// of the timers it found empty are returned. process-deployment offered such a timer as a
// duration to fill in and wrote the value of the dialog into the model on deploy.
func fillEmptyTimers(xml string, values map[string]string, fallback string) (string, []string, error) {
	doc := etree.NewDocument()
	if err := doc.ReadFromString(xml); err != nil {
		return xml, nil, err
	}
	var empty []string
	for _, definition := range doc.FindElements("//timerEventDefinition") {
		if len(definition.ChildElements()) > 0 || definition.Parent() == nil {
			continue
		}
		id := definition.Parent().SelectAttrValue("id", "")
		empty = append(empty, id)
		value := values[id]
		if value == "" {
			value = fallback
		}
		if value == "" {
			continue
		}
		duration := definition.CreateElement("timeDuration")
		duration.Space = definition.Space
		duration.SetText(value)
	}
	if len(empty) == 0 {
		return xml, nil, nil
	}
	result, err := doc.WriteToString()
	return result, empty, err
}
