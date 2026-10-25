package calendarsync

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/prudnik-web/itmo-schedule-mcp/internal/itmo"
)

func TestClassBlockReminders(t *testing.T) {
	type slot struct{ start, end string }
	tests := []struct {
		name  string
		slots []slot
		now   string
		// Expected reminder count by input slot; -1 means an already-started
		// class should be excluded from the desired calendar changes.
		want []int
	}{
		{"evening break", []slot{{"2026-10-26T18:50:00+03:00", "2026-10-26T20:20:00+03:00"}, {"2026-10-26T20:30:00+03:00", "2026-10-26T22:00:00+03:00"}}, "", []int{3, 0}},
		{"thirty minute break", []slot{{"2026-10-26T11:30:00+03:00", "2026-10-26T13:00:00+03:00"}, {"2026-10-26T13:30:00+03:00", "2026-10-26T15:00:00+03:00"}}, "", []int{3, 0}},
		{"long gap starts new block", []slot{{"2026-10-26T11:30:00+03:00", "2026-10-26T13:00:00+03:00"}, {"2026-10-26T13:31:00+03:00", "2026-10-26T15:00:00+03:00"}}, "", []int{3, 3}},
		{"overlap uses latest end", []slot{{"2026-10-26T11:30:00+03:00", "2026-10-26T15:00:00+03:00"}, {"2026-10-26T12:30:00+03:00", "2026-10-26T13:00:00+03:00"}, {"2026-10-26T15:30:00+03:00", "2026-10-26T17:00:00+03:00"}}, "", []int{3, 0, 0}},
		{"unsorted lessons", []slot{{"2026-10-26T20:30:00+03:00", "2026-10-26T22:00:00+03:00"}, {"2026-10-26T18:50:00+03:00", "2026-10-26T20:20:00+03:00"}}, "", []int{0, 3}},
		{"new day starts new block", []slot{{"2026-10-26T22:00:00+03:00", "2026-10-26T23:50:00+03:00"}, {"2026-10-27T00:10:00+03:00", "2026-10-27T01:00:00+03:00"}}, "", []int{3, 3}},
		{"sync during first class", []slot{{"2026-10-26T18:50:00+03:00", "2026-10-26T20:20:00+03:00"}, {"2026-10-26T20:30:00+03:00", "2026-10-26T22:00:00+03:00"}}, "2026-10-26T19:00:00+03:00", []int{-1, 0}},
		{"sync during break", []slot{{"2026-10-26T18:50:00+03:00", "2026-10-26T20:20:00+03:00"}, {"2026-10-26T20:30:00+03:00", "2026-10-26T22:00:00+03:00"}}, "2026-10-26T20:25:00+03:00", []int{-1, 0}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
			if tt.now != "" {
				now, _ = time.Parse(time.RFC3339, tt.now)
			}
			s := itmo.Schedule{DateStart: "2026-10-25", DateEnd: "2026-11-23", Timezone: "Europe/Moscow", Days: []itmo.Day{}}
			days := map[string]int{}
			for i, slot := range tt.slots {
				date := slot.start[:10]
				idx, ok := days[date]
				if !ok {
					idx = len(s.Days)
					days[date] = idx
					s.Days = append(s.Days, itmo.Day{Date: date, Lessons: []itmo.Lesson{}})
				}
				s.Days[idx].Lessons = append(s.Days[idx].Lessons, itmo.Lesson{PairID: ptr(int64(i + 1)), Subject: ptr("Class"), Start: ptr(slot.start), End: ptr(slot.end)})
			}
			events, err := BuildEvents(s, "account", now)
			if err != nil {
				t.Fatal(err)
			}
			byID := map[string]Event{}
			for _, e := range events {
				byID[e.ID] = e
			}
			for i, n := range tt.want {
				e, ok := byID[eventID("account", int64(i+1))]
				if n < 0 {
					if ok {
						t.Fatal("already-started class included")
					}
					continue
				}
				if !ok || len(e.Reminders.Overrides) != n || e.Reminders.UseDefault {
					t.Fatalf("slot %d: got %+v, want %d explicit reminders", i, e.Reminders, n)
				}
				if n == 0 {
					data, _ := json.Marshal(e.Reminders)
					if string(data) != `{"useDefault":false,"overrides":[]}` {
						t.Fatalf("must explicitly clear old reminders: %s", data)
					}
				}
			}
		})
	}
}

func TestSimultaneousClassesShareOneReminderSet(t *testing.T) {
	s := itmo.Schedule{DateStart: "2026-10-25", DateEnd: "2026-11-23", Timezone: "Europe/Moscow", Days: []itmo.Day{{Date: "2026-10-26", Lessons: []itmo.Lesson{}}}}
	for i := int64(1); i <= 2; i++ {
		s.Days[0].Lessons = append(s.Days[0].Lessons, itmo.Lesson{PairID: ptr(i), Subject: ptr("Class"), Start: ptr("2026-10-26T11:30:00+03:00"), End: ptr("2026-10-26T13:00:00+03:00")})
	}
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	e, err := BuildEvents(s, "account", now)
	if err != nil {
		t.Fatal(err)
	}
	if len(e) != 2 || len(e[0].Reminders.Overrides) != 3 || len(e[1].Reminders.Overrides) != 0 {
		t.Fatal("simultaneous classes should share reminders")
	}
	s.Days[0].Lessons[0], s.Days[0].Lessons[1] = s.Days[0].Lessons[1], s.Days[0].Lessons[0]
	reordered, err := BuildEvents(s, "account", now)
	if err != nil {
		t.Fatal(err)
	}
	if reordered[0].ID != e[0].ID || !SameManagedFields(reordered[0], e[0]) {
		t.Fatal("reminder owner changed after input reorder")
	}
}

func TestSyncClearsExistingSecondClassReminders(t *testing.T) {
	desired, now := fixture(t)
	f := &fakeCalendar{t: t, events: map[string]Event{}}
	for _, e := range desired {
		e.ETag = "v1"
		e.Reminders = Reminders{Overrides: []Reminder{{"popup", 1440}, {"popup", 180}, {"popup", 15}}}
		f.events[e.ID] = e
	}
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	defer srv.Close()
	g := &Google{HTTP: srv.Client(), BaseURL: srv.URL}
	cfg := Config{CalendarID: "itmo", AccountKey: "account", LookaheadDays: 30}
	stats, err := Sync(context.Background(), g, cfg, t.TempDir(), desired, now, false)
	if err != nil || stats.Updated != 1 || stats.Created != 0 || stats.Unchanged != 1 {
		t.Fatalf("expected one reminder update: %+v %v", stats, err)
	}
	second := f.events[desired[1].ID]
	if second.Reminders.UseDefault || len(second.Reminders.Overrides) != 0 {
		t.Fatal("second class reminders were not cleared")
	}
	stats, err = Sync(context.Background(), g, cfg, t.TempDir(), desired, now, false)
	if err != nil || stats.Updated != 0 || stats.Unchanged != 2 {
		t.Fatalf("repeat sync changed reminders: %+v %v", stats, err)
	}
}
