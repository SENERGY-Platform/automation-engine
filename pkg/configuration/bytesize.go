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

package configuration

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"

	envldr "github.com/SENERGY-Platform/go-env-loader"
)

// ByteSize is a size in bytes, given as a number of bytes or with a unit, e.g. "1GiB", "500MB"
// or "1.5GiB", so that a gateway module can offer it readable.
type ByteSize int64

var byteUnits = map[string]float64{
	"":    1,
	"b":   1,
	"kb":  1e3,
	"mb":  1e6,
	"gb":  1e9,
	"tb":  1e12,
	"kib": 1 << 10,
	"mib": 1 << 20,
	"gib": 1 << 30,
	"tib": 1 << 40,
}

// ParseByteSize reads a number with an optional unit: B, kB, MB, GB, TB (powers of 1000) or
// KiB, MiB, GiB, TiB (powers of 1024), case-insensitive, with or without a space before it.
func ParseByteSize(text string) (ByteSize, error) {
	text = strings.TrimSpace(text)
	end := strings.IndexFunc(text, func(r rune) bool {
		return !(r >= '0' && r <= '9' || r == '.' || r == '-' || r == '+')
	})
	number, unit := text, ""
	if end >= 0 {
		number, unit = text[:end], strings.ToLower(strings.TrimSpace(text[end:]))
	}
	factor, ok := byteUnits[unit]
	if !ok {
		return 0, fmt.Errorf("unknown unit %q in %q, use B, kB, MB, GB, TB, KiB, MiB, GiB or TiB", unit, text)
	}
	if unit == "" || unit == "b" {
		if n, err := strconv.ParseInt(number, 10, 64); err == nil {
			return ByteSize(n), nil
		}
	}
	value, err := strconv.ParseFloat(number, 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, fmt.Errorf("invalid size %q", text)
	}
	bytes := value * factor
	if math.Abs(bytes) > math.MaxInt64 {
		return 0, fmt.Errorf("size %q is too large", text)
	}
	return ByteSize(math.Round(bytes)), nil
}

// UnmarshalJSON takes a number of bytes or a string with a unit.
func (this *ByteSize) UnmarshalJSON(b []byte) error {
	var number int64
	if err := json.Unmarshal(b, &number); err == nil {
		*this = ByteSize(number)
		return nil
	}
	var text string
	if err := json.Unmarshal(b, &text); err != nil {
		return fmt.Errorf("size must be a number of bytes or a string such as \"1GiB\"")
	}
	size, err := ParseByteSize(text)
	if err != nil {
		return err
	}
	*this = size
	return nil
}

func ByteSizeEnvTypeParser() (reflect.Type, envldr.Parser) {
	return reflect.TypeOf(ByteSize(0)), func(_ reflect.Type, val string, _ []string, _ map[string]string) (interface{}, error) {
		return ParseByteSize(val)
	}
}
