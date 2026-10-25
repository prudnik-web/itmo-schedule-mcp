package calendarsync

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/prudnik-web/itmo-schedule-mcp/internal/itmo"
)

func ptr[T any](v T) *T { return &v }
func fixture(t *testing.T) ([]Event, time.Time) {
	t.Helper()
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	s := itmo.Schedule{DateStart: "2026-10-25", DateEnd: "2026-11-23", Timezone: "Europe/Moscow", Days: []itmo.Day{{Date: "2026-10-26", Lessons: []itmo.Lesson{
		{PairID: ptr(int64(10)), Subject: ptr("Business Planning in FinTech / Бизнес-планирование в сфере финансовых технологий"), Start: ptr("2026-10-26T18:50:00+03:00"), End: ptr("2026-10-26T20:20:00+03:00"), WorkType: ptr("Лекции"), OnlineURL: ptr("https://example.com/classroom")},
		{PairID: ptr(int64(11)), Subject: ptr("Practical class"), Start: ptr("2026-10-26T20:30:00+03:00"), End: ptr("2026-10-26T22:00:00+03:00"), WorkType: ptr("Практические занятия")},
	}}}}
	e, err := BuildEvents(s, "account", now)
	if err != nil {
		t.Fatal(err)
	}
	return e, now
}

func TestBuildEventsTimesRemindersAndIdentity(t *testing.T) {
	e, _ := fixture(t)
	if len(e) != 2 || e[0].ID == e[1].ID {
		t.Fatal("classes must remain separate")
	}
	if e[0].Start.TimeZone != "Europe/Moscow" || e[0].Reminders.UseDefault {
		t.Fatal("timezone or reminder defaults")
	}
	for i, n := range []int{1440, 180, 15} {
		if e[0].Reminders.Overrides[i] != (Reminder{"popup", n}) {
			t.Fatal("wrong reminders")
		}
	}
	if e[0].ID != eventID("account", 10) || e[0].ID == eventID("other-account", 10) {
		t.Fatal("unstable or cross-account identity")
	}
	shift := e[0]
	shift.Start.DateTime = "2026-10-27T18:50:00+03:00"
	if shift.ID != e[0].ID || SameManagedFields(e[0], shift) {
		t.Fatal("a time change must update the same event")
	}
	utc := e[0]
	utc.Start.DateTime = "2026-10-26T15:50:00Z"
	utc.End.DateTime = "2026-10-26T17:20:00Z"
	if !SameManagedFields(utc, e[0]) {
		t.Fatal("timezone representations must compare as instants")
	}
}

func TestInvalidLessonFailsBeforeAnyWrites(t *testing.T) {
	s := itmo.Schedule{DateStart: "2026-10-25", DateEnd: "2026-11-23", Timezone: "Europe/Moscow", Days: []itmo.Day{{Date: "2026-10-26", Lessons: []itmo.Lesson{{Subject: ptr("Missing ID and times")}}}}}
	if _, err := BuildEvents(s, "account", time.Now()); err == nil {
		t.Fatal("invalid lesson accepted")
	}
}

func TestDuplicateMatchingIsConservative(t *testing.T) {
	e, _ := fixture(t)
	want := e[0]
	c := Event{ID: "old", Summary: "Business Planning in FinTech", Start: want.Start, End: want.End}
	c.Organizer.Self = true
	if !IsDuplicate(c, want) {
		t.Fatal("exact title and times should match")
	}
	for name, change := range map[string]func(*Event){
		"partial title": func(e *Event) { e.Summary = "Business" },
		"different end": func(e *Event) { e.End.DateTime = "2026-10-26T22:00:00+03:00" },
		"recurrence":    func(e *Event) { e.RecurringEventID = "series" },
		"guests":        func(e *Event) { e.Attendees = []map[string]any{{"email": "guest@example.test"}} },
		"not owned":     func(e *Event) { e.Organizer.Self = false },
		"cancelled":     func(e *Event) { e.Status = "cancelled" },
	} {
		t.Run(name, func(t *testing.T) {
			copy := c
			change(&copy)
			if IsDuplicate(copy, want) {
				t.Fatal("unsafe duplicate match")
			}
		})
	}
	c.Summary = "Custom course name"
	c.Description = "Join https://example.com/classroom"
	if !IsDuplicate(c, want) {
		t.Fatal("exact classroom URL and times should match")
	}
	c.Description += "123"
	if IsDuplicate(c, want) {
		t.Fatal("URL prefix is not an exact match")
	}
}

type fakeCalendar struct {
	t          *testing.T
	events     map[string]Event
	primary    Event
	writes     int
	hide       bool
	failInsert bool
	deleted    bool
	dir        string
	paginate   bool
}

func (f *fakeCalendar) serve(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	path := r.URL.Path
	if r.Method != "GET" {
		f.writes++
	}
	if path == "/calendars/itmo/events" && r.Method == "GET" {
		var items []Event
		if f.paginate && r.URL.Query().Get("pageToken") == "" {
			json.NewEncoder(w).Encode(map[string]any{"nextPageToken": "next"})
			return
		}
		if !f.hide {
			for _, e := range f.events {
				items = append(items, e)
			}
		}
		json.NewEncoder(w).Encode(map[string]any{"items": items})
		return
	}
	if path == "/calendars/itmo/events" && r.Method == "POST" {
		if f.failInsert {
			w.WriteHeader(403)
			return
		}
		var e Event
		json.NewDecoder(r.Body).Decode(&e)
		if _, ok := f.events[e.ID]; ok {
			w.WriteHeader(409)
			return
		}
		e.ETag = "v1"
		f.events[e.ID] = e
		json.NewEncoder(w).Encode(e)
		return
	}
	if strings.HasPrefix(path, "/calendars/itmo/events/") {
		id := strings.TrimPrefix(path, "/calendars/itmo/events/")
		e, ok := f.events[id]
		if !ok {
			w.WriteHeader(404)
			return
		}
		if r.Method == "PATCH" {
			if r.Header.Get("If-Match") != e.ETag {
				w.WriteHeader(412)
				return
			}
			json.NewDecoder(r.Body).Decode(&e)
			e.ETag = "v2"
			f.events[id] = e
		}
		json.NewEncoder(w).Encode(e)
		return
	}
	if path == "/calendars/primary/events" {
		items := []Event{}
		if f.primary.ID != "" && !f.deleted {
			items = append(items, f.primary)
		}
		json.NewEncoder(w).Encode(map[string]any{"items": items})
		return
	}
	if path == "/calendars/primary/events/old" {
		if r.Method == "DELETE" {
			backups, _ := filepath.Glob(filepath.Join(f.dir, "primary-backups", "*.json"))
			if len(backups) != 1 {
				f.t.Error("deletion occurred without a backup")
			}
			if r.Header.Get("If-Match") != f.primary.ETag {
				f.t.Error("missing version guard")
			}
			if len(f.events) == 0 {
				f.t.Error("deletion occurred before destination creation")
			}
			f.deleted = true
			w.WriteHeader(204)
			return
		}
		json.NewEncoder(w).Encode(f.primary)
		return
	}
	f.t.Errorf("unexpected request %s %s", r.Method, path)
	w.WriteHeader(400)
}

func TestSyncCreatesUpdatesAndIsIdempotent(t *testing.T) {
	e, now := fixture(t)
	dir := t.TempDir()
	f := &fakeCalendar{t: t, events: map[string]Event{}, dir: dir, paginate: true}
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	defer srv.Close()
	g := &Google{HTTP: srv.Client(), BaseURL: srv.URL}
	cfg := Config{CalendarID: "itmo", AccountKey: "account", LookaheadDays: 30}
	stats, err := Sync(context.Background(), g, cfg, dir, e, now, false)
	if err != nil || stats.Created != 2 {
		t.Fatalf("first sync: %+v %v", stats, err)
	}
	writes := f.writes
	stats, err = Sync(context.Background(), g, cfg, dir, e, now, false)
	if err != nil || stats.Unchanged != 2 || f.writes != writes {
		t.Fatalf("repeat sync: %+v %v", stats, err)
	}
	e[0].Start.DateTime = "2026-10-27T18:50:00+03:00"
	e[0].End.DateTime = "2026-10-27T20:20:00+03:00"
	stats, err = Sync(context.Background(), g, cfg, dir, e, now, false)
	if err != nil || stats.Updated != 1 || len(f.events) != 2 {
		t.Fatalf("reschedule: %+v %v", stats, err)
	}
	f.hide = true
	stats, err = Sync(context.Background(), g, cfg, dir, e, now, false)
	if err != nil || stats.Unchanged != 2 || len(f.events) != 2 {
		t.Fatalf("409 recovery: %+v %v", stats, err)
	}
}

func TestMigrationCopiesVerifiesBacksUpThenDeletes(t *testing.T) {
	e, now := fixture(t)
	dir := t.TempDir()
	old := Event{ID: "old", ETag: "old-v1", Summary: "Business Planning in FinTech", Start: e[0].Start, End: e[0].End}
	old.Organizer.Self = true
	f := &fakeCalendar{t: t, events: map[string]Event{}, primary: old, dir: dir}
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	defer srv.Close()
	g := &Google{HTTP: srv.Client(), BaseURL: srv.URL}
	cfg := Config{CalendarID: "itmo", AccountKey: "account", LookaheadDays: 30, MigratePrimary: true}
	stats, err := Sync(context.Background(), g, cfg, dir, e, now, true)
	if err != nil || stats.Created != 2 || stats.DeletedPrimary != 1 || f.writes != 0 {
		t.Fatalf("dry run: %+v %v", stats, err)
	}
	f.failInsert = true
	if _, err := Sync(context.Background(), g, cfg, dir, e, now, false); err == nil || f.deleted {
		t.Fatal("failure must not delete the source")
	}
	f.failInsert = false
	stats, err = Sync(context.Background(), g, cfg, dir, e, now, false)
	if err != nil || stats.DeletedPrimary != 1 || !f.deleted {
		t.Fatalf("migration: %+v %v", stats, err)
	}
}

func TestEmptyScheduleNeverDeletesManagedEvents(t *testing.T) {
	e, now := fixture(t)
	f := &fakeCalendar{t: t, events: map[string]Event{e[0].ID: e[0]}}
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	defer srv.Close()
	_, err := Sync(context.Background(), &Google{HTTP: srv.Client(), BaseURL: srv.URL}, Config{CalendarID: "itmo", AccountKey: "account", MigratePrimary: true}, t.TempDir(), nil, now, false)
	if err != nil || f.writes != 0 || len(f.events) != 1 {
		t.Fatal("empty schedule must preserve events", err)
	}
}
