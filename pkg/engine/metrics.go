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

package engine

import (
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/model"
)

// Metrics receives events of the engine (OPS-05); the labels follow "per deployment and tenant".
type Metrics interface {
	InstanceStarted(namespace, deploymentId, owner string)
	InstanceEnded(namespace, deploymentId, owner string, state model.InstanceState)
	Incident(namespace, deploymentId, owner string)
	Restart(namespace, deploymentId string, performed bool)
	// result is matched, unmatched or duplicate (EVT-08)
	Correlation(result string)
	Task(taskType string, result string, duration time.Duration)
	// delay between due time and firing of a timer
	TimerDelay(delay time.Duration)
}

type NoMetrics struct{}

func (NoMetrics) InstanceStarted(string, string, string)                    {}
func (NoMetrics) InstanceEnded(string, string, string, model.InstanceState) {}
func (NoMetrics) Incident(string, string, string)                           {}
func (NoMetrics) Restart(string, string, bool)                              {}
func (NoMetrics) Correlation(string)                                        {}
func (NoMetrics) Task(string, string, time.Duration)                        {}
func (NoMetrics) TimerDelay(time.Duration)                                  {}
