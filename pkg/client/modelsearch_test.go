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
	"reflect"
	"testing"

	"github.com/SENERGY-Platform/automation-engine/pkg/client"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
)

// The search finds the term in the name or the description, as /v2/processes of
// process-model-repository did (MOD-01).
func TestModelSearchFindsTheDescription(t *testing.T) {
	each(t, func(t *testing.T, c client.Client) {
		namedModels(t, c, [2]string{"a 1", "x"}, [2]string{"b 1", "y"}, [2]string{"a 2", "y"}, [2]string{"b 2", "x"})
		page, err := c.ListModels(ctx, alice, ns, model.ModelListOptions{Search: "x"})
		if got := modelNames(page); err != nil || !reflect.DeepEqual(got, []string{"a 1", "b 2"}) {
			t.Fatalf("expected a 1 and b 2: %v %v", got, err)
		}
	})
}
