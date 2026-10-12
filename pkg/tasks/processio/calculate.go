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

package processio

import (
	"fmt"
	"strings"
	"time"
)

const calculatePrefix = "calculate_"

// calculate answers computed keys: calculate_UtcOffset_<IANA zone> is the offset of the zone to
// UTC in minutes, as process-io-api computes it
func calculate(key string, now time.Time) (value any, computed bool, err error) {
	if !strings.HasPrefix(key, calculatePrefix) {
		return nil, false, nil
	}
	name, argument, _ := strings.Cut(strings.TrimPrefix(key, calculatePrefix), "_")
	switch name {
	case "UtcOffset":
		location, err := time.LoadLocation(argument)
		if err != nil {
			return nil, true, fmt.Errorf("unknown time zone %q", argument)
		}
		_, offset := now.In(location).Zone()
		return float64(offset / 60), true, nil
	}
	return nil, true, fmt.Errorf("unknown computed key %q", key)
}
