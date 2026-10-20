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
	"testing"
	"time"
)

func TestParseDurationAcceptsCalendarAndClockUnits(t *testing.T) {
	from := time.Date(2026, 1, 31, 10, 0, 0, 0, time.UTC)
	cases := map[string]time.Time{
		"PT5S":       from.Add(5 * time.Second),
		"PT1.5S":     from.Add(1500 * time.Millisecond),
		"PT2H30M":    from.Add(150 * time.Minute),
		"P1D":        time.Date(2026, 2, 1, 10, 0, 0, 0, time.UTC),
		"P1W":        time.Date(2026, 2, 7, 10, 0, 0, 0, time.UTC),
		"P1Y2M3DT4H": time.Date(2027, 3, 31+3, 14, 0, 0, 0, time.UTC),
	}
	for value, expected := range cases {
		d, err := ParseDuration(value)
		if err != nil {
			t.Errorf("%s: %v", value, err)
			continue
		}
		if got := d.AddTo(from); !got.Equal(expected) {
			t.Errorf("%s: expected %v, got %v", value, expected, got)
		}
	}
}

func TestParseDurationRejectsMalformedValues(t *testing.T) {
	for _, value := range []string{"", "P", "PT", "5S", "P1H", "PT0S", "${cycle}", "R/P1D"} {
		if _, err := ParseDuration(value); err == nil {
			t.Errorf("expected error for %q", value)
		}
	}
}

func TestNextRejectsRepeatingIntervalAsDuration(t *testing.T) {
	if _, err := Next(KindDuration, "R/P1D", time.Now(), time.UTC); err == nil {
		t.Fatal("expected error")
	}
}

func TestNextCycleWithRepeatingIntervalAddsDuration(t *testing.T) {
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	got, err := Next(KindCycle, "R3/PT10M", from, time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Equal(from.Add(10 * time.Minute)) {
		t.Fatalf("unexpected %v", got)
	}
}

func TestParseDateReadsValuesWithoutOffsetInLocation(t *testing.T) {
	berlin, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Skip("no tzdata available:", err)
	}
	got, err := ParseDate("2026-10-12T10:00:00", berlin)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Equal(time.Date(2026, 10, 12, 8, 0, 0, 0, time.UTC)) {
		t.Fatalf("unexpected %v", got)
	}
	got, err = ParseDate("2026-10-12T10:00:00Z", berlin)
	if err != nil || !got.Equal(time.Date(2026, 10, 12, 10, 0, 0, 0, time.UTC)) {
		t.Fatalf("unexpected %v %v", got, err)
	}
}

func TestCronSupportsQuartzFieldsAndQuestionMark(t *testing.T) {
	from := time.Date(2026, 10, 12, 10, 30, 0, 0, time.UTC) // a Monday
	cases := map[string]time.Time{
		"0 0 11 * * ?":      time.Date(2026, 10, 12, 11, 0, 0, 0, time.UTC),
		"0 0 11 * * * ?":    time.Date(2026, 10, 12, 11, 0, 0, 0, time.UTC),
		"0 0 5 * * ?":       time.Date(2026, 10, 13, 5, 0, 0, 0, time.UTC),
		"*/15 * * * * ?":    time.Date(2026, 10, 12, 10, 30, 15, 0, time.UTC),
		"0 0 12 ? * 1":      time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC), // Quartz 1 = Sunday
		"0 0 12 ? * 2-3":    time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC), // Monday
		"0 0 12 ? * WED":    time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC),
		"0 0 12 1 JUL ?":    time.Date(2027, 7, 1, 12, 0, 0, 0, time.UTC),
		"0 0 12 1 1 ? 2028": time.Date(2028, 1, 1, 12, 0, 0, 0, time.UTC),
	}
	for expr, expected := range cases {
		got, err := Next(KindCycle, expr, from, time.UTC)
		if err != nil {
			t.Errorf("%s: %v", expr, err)
			continue
		}
		if !got.Equal(expected) {
			t.Errorf("%s: expected %v, got %v", expr, expected, got)
		}
	}
}

func TestCronRejectsUnsupportedExpressions(t *testing.T) {
	for _, expr := range []string{"0/1 * * * *", "0 0 12 L * ?", "0 0 12 15W * ?", "0 0 12 ? * 6#3", "0 0 12 ? * 8", "${cron_0}"} {
		if _, err := ParseCron(expr, time.UTC); err == nil {
			t.Errorf("expected error for %q", expr)
		}
	}
}

func TestCronReportsNoOccurrenceForPastYears(t *testing.T) {
	_, err := Next(KindCycle, "0 0 12 1 1 ? 2020", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), time.UTC)
	if err != ErrNoNextOccurrence {
		t.Fatalf("expected ErrNoNextOccurrence, got %v", err)
	}
}

func TestNextNotBeforeLimitsDurationsAndCycles(t *testing.T) {
	from := time.Date(2026, 10, 12, 10, 30, 0, 300_000_000, time.UTC)
	earliest := from.Add(5 * time.Second)
	cases := []struct {
		kind     Kind
		value    string
		expected time.Time
		limited  bool
	}{
		{KindDuration, "PT1S", earliest, true},
		{KindDuration, "PT10S", from.Add(10 * time.Second), false},
		{KindCycle, "R/PT1S", earliest, true},
		{KindCycle, "R5/PT1M", from.Add(time.Minute), false},
		// the first cron time at or after earliest, still on the second
		{KindCycle, "* * * * * ?", time.Date(2026, 10, 12, 10, 30, 6, 0, time.UTC), true},
		{KindCycle, "*/5 * * * * ?", time.Date(2026, 10, 12, 10, 30, 10, 0, time.UTC), true},
		{KindCycle, "0 * * * * ?", time.Date(2026, 10, 12, 10, 31, 0, 0, time.UTC), false},
		{KindDate, "2026-10-12T10:30:01Z", time.Date(2026, 10, 12, 10, 30, 1, 0, time.UTC), false},
	}
	for _, c := range cases {
		got, limited, err := NextNotBefore(c.kind, c.value, from, earliest, time.UTC)
		if err != nil {
			t.Errorf("%v: %v", c.value, err)
			continue
		}
		if !got.Equal(c.expected) || limited != c.limited {
			t.Errorf("%v: expected %v (limited %v), got %v (limited %v)", c.value, c.expected, c.limited, got, limited)
		}
	}
}

func TestNextNotBeforeKeepsACronAtTheBoundary(t *testing.T) {
	previous := time.Date(2026, 10, 12, 10, 30, 0, 0, time.UTC)
	got, _, err := NextNotBefore(KindCycle, "* * * * * ?", previous.Add(300*time.Millisecond), previous.Add(5*time.Second), time.UTC)
	if err != nil || !got.Equal(previous.Add(5*time.Second)) {
		t.Fatalf("expected %v, got %v %v", previous.Add(5*time.Second), got, err)
	}
}

func TestNextNotBeforeWithoutEarliestIsNext(t *testing.T) {
	from := time.Date(2026, 10, 12, 10, 30, 0, 0, time.UTC)
	for kind, value := range map[Kind]string{KindDuration: "PT1S", KindCycle: "* * * * * ?"} {
		expected, _ := Next(kind, value, from, time.UTC)
		got, limited, err := NextNotBefore(kind, value, from, time.Time{}, time.UTC)
		if err != nil || limited || !got.Equal(expected) {
			t.Errorf("%v: expected %v, got %v %v %v", value, expected, got, limited, err)
		}
	}
}

func TestNextScheduledLimitsCronAndEvery(t *testing.T) {
	from := time.Date(2026, 10, 12, 10, 30, 0, 0, time.UTC)
	earliest := from.Add(5 * time.Second)
	cases := map[string]time.Time{
		"* * * * * *":   earliest,
		"*/2 * * * * *": from.Add(6 * time.Second),
		"@every 1s":     earliest,
		"* * * * *":     from.Add(time.Minute),
		"@every 1m":     from.Add(time.Minute),
	}
	for expr, expected := range cases {
		schedule, err := ParseSchedule(expr, time.UTC)
		if err != nil {
			t.Fatal(err)
		}
		if got := NextScheduled(schedule, from, earliest); !got.Equal(expected) {
			t.Errorf("%v: expected %v, got %v", expr, expected, got)
		}
		if got, unlimited := NextScheduled(schedule, from, time.Time{}), schedule.Next(from); !got.Equal(unlimited) {
			t.Errorf("%v without earliest: expected %v, got %v", expr, unlimited, got)
		}
	}
}
