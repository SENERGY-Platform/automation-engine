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

package client

import (
	"iter"

	"github.com/SENERGY-Platform/automation-engine/pkg/model"
)

// All iterates over all items of a paged list by requesting page after page (CLI-04):
//
//	for m, err := range client.All(func(page model.PageOptions) (model.Page[model.Model], error) {
//		return c.ListModels(ctx, token, "processes", model.ModelListOptions{PageOptions: page})
//	}) { ... }
//
// Iteration stops after the first error, which is yielded with a zero item.
func All[T any](list func(page model.PageOptions) (model.Page[T], error)) iter.Seq2[T, error] {
	return func(yield func(T, error) bool) {
		page := model.PageOptions{Limit: model.DefaultPageLimit}
		for {
			result, err := list(page)
			if err != nil {
				var zero T
				yield(zero, err)
				return
			}
			for _, item := range result.Items {
				if !yield(item, nil) {
					return
				}
			}
			page.Offset += len(result.Items)
			if len(result.Items) == 0 || page.Offset >= result.Total {
				return
			}
		}
	}
}
