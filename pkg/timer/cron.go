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
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
)

// CronSchedule is a Quartz cron expression with 6 or 7 fields:
// seconds minutes hours day-of-month month day-of-week [year].
type CronSchedule struct {
	schedule cron.Schedule
	years    []yearRange
}

type yearRange struct{ from, to int }

var quartzParser = cron.NewParser(cron.Second | cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)

// ParseCron translates the Quartz dialect into robfig/cron: Quartz counts day-of-week from
// 1 (Sunday) to 7, robfig from 0 (Sunday) to 6. L, W and # are not supported.
func ParseCron(expr string, loc *time.Location) (*CronSchedule, error) {
	fields := strings.Fields(expr)
	if len(fields) != 6 && len(fields) != 7 {
		return nil, fmt.Errorf("cron expression %q must have 6 or 7 fields", expr)
	}
	if strings.ContainsAny(strings.ToUpper(fields[3]), "LW") || strings.ContainsAny(stripDayNames(fields[5]), "L#") {
		return nil, fmt.Errorf("cron expression %q uses L, W or #, which is not supported", expr)
	}
	dow, err := convertDayOfWeek(fields[5])
	if err != nil {
		return nil, fmt.Errorf("cron expression %q: %w", expr, err)
	}
	result := &CronSchedule{}
	if len(fields) == 7 {
		result.years, err = parseYears(fields[6])
		if err != nil {
			return nil, fmt.Errorf("cron expression %q: %w", expr, err)
		}
	}
	spec := strings.Join(append(slices.Clone(fields[:5]), dow), " ")
	if loc != nil {
		spec = "CRON_TZ=" + loc.String() + " " + spec
	}
	result.schedule, err = quartzParser.Parse(spec)
	if err != nil {
		return nil, fmt.Errorf("cron expression %q: %w", expr, err)
	}
	return result, nil
}

var dayNames = []string{"SUN", "MON", "TUE", "WED", "THU", "FRI", "SAT"}

func stripDayNames(field string) string {
	field = strings.ToUpper(field)
	for _, name := range dayNames {
		field = strings.ReplaceAll(field, name, "")
	}
	return field
}

func convertDayOfWeek(field string) (string, error) {
	if field == "*" || field == "?" {
		return field, nil
	}
	parts := strings.Split(field, ",")
	for i, part := range parts {
		rangePart, step, hasStep := strings.Cut(part, "/")
		bounds := strings.Split(rangePart, "-")
		for j, b := range bounds {
			if b == "*" || slices.Contains(dayNames, strings.ToUpper(b)) {
				continue
			}
			n, err := strconv.Atoi(b)
			if err != nil || n < 1 || n > 7 {
				return "", fmt.Errorf("invalid day of week %q", b)
			}
			bounds[j] = strconv.Itoa(n - 1)
		}
		parts[i] = strings.Join(bounds, "-")
		if hasStep {
			parts[i] += "/" + step
		}
	}
	return strings.Join(parts, ","), nil
}

func parseYears(field string) ([]yearRange, error) {
	if field == "*" || field == "?" {
		return nil, nil
	}
	var result []yearRange
	for _, part := range strings.Split(field, ",") {
		from, to, isRange := strings.Cut(part, "-")
		f, err := strconv.Atoi(from)
		if err != nil {
			return nil, fmt.Errorf("invalid year %q", part)
		}
		t := f
		if isRange {
			if t, err = strconv.Atoi(to); err != nil || t < f {
				return nil, fmt.Errorf("invalid year range %q", part)
			}
		}
		result = append(result, yearRange{from: f, to: t})
	}
	return result, nil
}

func (this *CronSchedule) yearAllowed(year int) bool {
	if len(this.years) == 0 {
		return true
	}
	for _, r := range this.years {
		if year >= r.from && year <= r.to {
			return true
		}
	}
	return false
}

func (this *CronSchedule) Next(from time.Time) (time.Time, error) {
	t := from
	for range 200 {
		next := this.schedule.Next(t)
		if next.IsZero() {
			return time.Time{}, ErrNoNextOccurrence
		}
		if this.yearAllowed(next.Year()) {
			return next, nil
		}
		// skip to the end of the rejected year
		t = time.Date(next.Year()+1, 1, 1, 0, 0, 0, 0, next.Location()).Add(-time.Second)
	}
	return time.Time{}, ErrNoNextOccurrence
}

// scheduleParser reads cron as the process-scheduler did: 5 fields, or 6 with seconds first,
// and descriptors such as @daily or @every 1h (RUN-07)
var scheduleParser = cron.NewParser(cron.SecondOptional | cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)

// ParseSchedule parses a cron of a time schedule in loc.
func ParseSchedule(expr string, loc *time.Location) (cron.Schedule, error) {
	spec := strings.TrimSpace(expr)
	if loc != nil && !strings.HasPrefix(spec, "@every") {
		spec = "CRON_TZ=" + loc.String() + " " + spec
	}
	schedule, err := scheduleParser.Parse(spec)
	if err != nil {
		return nil, fmt.Errorf("invalid cron %q: %w", expr, err)
	}
	return schedule, nil
}

// NextScheduled returns the first start of a schedule after from and not before earliest
// (MIN_TIMER_DURATION): a cron at its first time at or after earliest, @every at earliest.
// Zero if there is none.
func NextScheduled(schedule cron.Schedule, from, earliest time.Time) time.Time {
	next := schedule.Next(from)
	if next.IsZero() || !next.Before(earliest) {
		return next
	}
	if _, ok := schedule.(cron.ConstantDelaySchedule); ok {
		return earliest
	}
	// cron finds times strictly after its argument
	return schedule.Next(earliest.Add(-time.Nanosecond))
}
