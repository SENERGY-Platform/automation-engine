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

package timer

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Duration is an ISO 8601 duration. Years, months, weeks and days are calendar units,
// hours, minutes and seconds are fixed.
// Implementation follows ISO 8601-1:2019 section 5.5.2.4 (format with designators).
type Duration struct {
	Years, Months, Weeks, Days int
	Clock                      time.Duration
}

var durationPattern = regexp.MustCompile(`^P(?:(\d+)Y)?(?:(\d+)M)?(?:(\d+)W)?(?:(\d+)D)?(?:T(?:(\d+)H)?(?:(\d+)M)?(?:(\d+(?:[.,]\d+)?)S)?)?$`)

func ParseDuration(value string) (result Duration, err error) {
	value = strings.TrimSpace(value)
	match := durationPattern.FindStringSubmatch(value)
	if match == nil || value == "P" || strings.HasSuffix(value, "T") {
		return result, fmt.Errorf("invalid ISO 8601 duration %q", value)
	}
	atoi := func(s string) int {
		if s == "" {
			return 0
		}
		i, _ := strconv.Atoi(s)
		return i
	}
	result.Years = atoi(match[1])
	result.Months = atoi(match[2])
	result.Weeks = atoi(match[3])
	result.Days = atoi(match[4])
	result.Clock = time.Duration(atoi(match[5]))*time.Hour + time.Duration(atoi(match[6]))*time.Minute
	if match[7] != "" {
		seconds, err := strconv.ParseFloat(strings.ReplaceAll(match[7], ",", "."), 64)
		if err != nil {
			return result, fmt.Errorf("invalid ISO 8601 duration %q: %w", value, err)
		}
		result.Clock += time.Duration(seconds * float64(time.Second))
	}
	if result.IsZero() {
		return result, fmt.Errorf("ISO 8601 duration %q is zero", value)
	}
	return result, nil
}

func (this Duration) IsZero() bool {
	return this.Years == 0 && this.Months == 0 && this.Weeks == 0 && this.Days == 0 && this.Clock == 0
}

func (this Duration) AddTo(t time.Time) time.Time {
	return t.AddDate(this.Years, this.Months, this.Weeks*7+this.Days).Add(this.Clock)
}

var dateLayouts = []string{
	time.RFC3339Nano,
	"2006-01-02T15:04:05.999999999",
	"2006-01-02T15:04",
	"2006-01-02",
}

// ParseDate parses an ISO 8601 date-time; values without offset are read in loc.
func ParseDate(value string, loc *time.Location) (time.Time, error) {
	value = strings.TrimSpace(value)
	for _, layout := range dateLayouts {
		if t, err := time.ParseInLocation(layout, value, loc); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("invalid ISO 8601 date %q", value)
}

type Kind string

const (
	KindDuration Kind = "duration"
	KindDate     Kind = "date"
	KindCycle    Kind = "cycle"
)

var ErrNoNextOccurrence = errors.New("timer has no further occurrence")

// Next returns the first due time of a timer definition after from.
// A cycle is either a Quartz cron expression or an ISO 8601 repeating interval R[n]/<duration>.
func Next(kind Kind, value string, from time.Time, loc *time.Location) (time.Time, error) {
	switch kind {
	case KindDuration:
		if strings.HasPrefix(strings.TrimSpace(value), "R") {
			return time.Time{}, fmt.Errorf("repeating interval %q is not allowed as duration, use a cycle", value)
		}
		d, err := ParseDuration(value)
		if err != nil {
			return time.Time{}, err
		}
		return d.AddTo(from), nil
	case KindDate:
		return ParseDate(value, loc)
	case KindCycle:
		value = strings.TrimSpace(value)
		if strings.HasPrefix(value, "R") {
			d, err := parseRepeatingInterval(value)
			if err != nil {
				return time.Time{}, err
			}
			return d.AddTo(from), nil
		}
		schedule, err := ParseCron(value, loc)
		if err != nil {
			return time.Time{}, err
		}
		return schedule.Next(from)
	default:
		return time.Time{}, fmt.Errorf("unknown timer kind %q", kind)
	}
}

// NextNotBefore is Next for a timer that must not be due before earliest (MIN_TIMER_DURATION).
// A duration or repeating interval due earlier is due at earliest, a cron expression at its
// first time at or after earliest; a date is never moved. limited reports a moved due time.
func NextNotBefore(kind Kind, value string, from, earliest time.Time, loc *time.Location) (due time.Time, limited bool, err error) {
	due, err = Next(kind, value, from, loc)
	if err != nil || kind == KindDate || !due.Before(earliest) {
		return due, false, err
	}
	value = strings.TrimSpace(value)
	if kind == KindCycle && !strings.HasPrefix(value, "R") {
		schedule, err := ParseCron(value, loc)
		if err != nil {
			return time.Time{}, false, err
		}
		// cron finds times strictly after its argument
		due, err = schedule.Next(earliest.Add(-time.Nanosecond))
		return due, err == nil, err
	}
	return earliest, true, nil
}

// Validate checks a literal timer value without computing a due time.
func Validate(kind Kind, value string) error {
	_, err := Next(kind, value, time.Now(), time.UTC)
	if errors.Is(err, ErrNoNextOccurrence) {
		return nil
	}
	return err
}

// R[n]/<duration>; a start date or an end date is not supported, the repetition count is
// ignored because a timer event fires once per activation
func parseRepeatingInterval(value string) (Duration, error) {
	parts := strings.Split(value, "/")
	if len(parts) != 2 {
		return Duration{}, fmt.Errorf("unsupported repeating interval %q, expected R[n]/<duration>", value)
	}
	if parts[0] != "R" {
		if _, err := strconv.Atoi(strings.TrimPrefix(parts[0], "R")); err != nil {
			return Duration{}, fmt.Errorf("invalid repetition in %q", value)
		}
	}
	return ParseDuration(parts[1])
}
