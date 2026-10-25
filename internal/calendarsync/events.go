package calendarsync

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"html"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/prudnik-web/itmo-schedule-mcp/internal/itmo"
)

const owner = "itmo-calendar-sync-v1"

type DateTime struct {
	DateTime string `json:"dateTime,omitempty"`
	TimeZone string `json:"timeZone,omitempty"`
	Date     string `json:"date,omitempty"`
}
type Reminder struct {
	Method  string `json:"method"`
	Minutes int    `json:"minutes"`
}
type Reminders struct {
	UseDefault bool       `json:"useDefault"`
	Overrides  []Reminder `json:"overrides"`
}
type Properties struct {
	Private map[string]string `json:"private"`
}
type Event struct {
	ID               string           `json:"id,omitempty"`
	ETag             string           `json:"etag,omitempty"`
	Status           string           `json:"status,omitempty"`
	Summary          string           `json:"summary"`
	Description      string           `json:"description"`
	Location         string           `json:"location"`
	Start            DateTime         `json:"start"`
	End              DateTime         `json:"end"`
	Reminders        Reminders        `json:"reminders"`
	Extended         Properties       `json:"extendedProperties"`
	RecurringEventID string           `json:"recurringEventId,omitempty"`
	Recurrence       []string         `json:"recurrence,omitempty"`
	Attendees        []map[string]any `json:"attendees,omitempty"`
	Organizer        struct {
		Self bool `json:"self,omitempty"`
	} `json:"organizer,omitzero"`
}

func value(s *string) string {
	if s == nil {
		return ""
	}
	return strings.TrimSpace(*s)
}
func eventID(account string, id int64) string {
	hash := sha256.Sum256(fmt.Appendf(nil, "%s:%d", account, id))
	return "itmo" + hex.EncodeToString(hash[:])
}

func BuildEvents(s itmo.Schedule, account string, now time.Time) ([]Event, error) {
	if s.Timezone != "Europe/Moscow" || s.Days == nil {
		return nil, errors.New("invalid schedule")
	}
	seen := map[string]bool{}
	seenDays := map[string]bool{}
	var events []Event
	for _, day := range s.Days {
		if _, err := time.Parse(time.DateOnly, day.Date); err != nil || day.Date < s.DateStart || day.Date > s.DateEnd || seenDays[day.Date] || day.Lessons == nil {
			return nil, errors.New("invalid schedule day; calendar was left unchanged")
		}
		seenDays[day.Date] = true
		for _, l := range day.Lessons {
			start, err1 := time.Parse(time.RFC3339, value(l.Start))
			end, err2 := time.Parse(time.RFC3339, value(l.End))
			if err1 != nil || err2 != nil || !end.After(start) || start.In(itmo.Moscow).Format(time.DateOnly) != day.Date || value(l.Subject) == "" || l.PairID == nil || *l.PairID <= 0 {
				return nil, errors.New("a lesson has no reliable ID, title, or time; calendar was left unchanged")
			}
			id := eventID(account, *l.PairID)
			if seen[id] {
				return nil, errors.New("duplicate ITMO lesson ID; calendar was left unchanged")
			}
			seen[id] = true
			kind := value(l.WorkType)
			if kind == "" {
				kind = value(l.Type)
			}
			description := []string{"Synced from your personal ITMO schedule."}
			for _, field := range []struct{ k, v string }{{"Type", kind}, {"Teacher", value(l.TeacherName)}, {"Group", value(l.Group)}, {"Format", value(l.Format)}, {"Notes", value(l.Note)}} {
				if field.v != "" {
					description = append(description, field.k+": "+field.v)
				}
			}
			link := value(l.OnlineURL)
			if u, err := url.Parse(link); err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
				link = ""
			}
			if link != "" {
				description = append(description, "Join: "+link)
			}
			location := value(l.Building)
			if room := value(l.Room); room != "" {
				if location != "" {
					location += ", "
				}
				location += "Room " + room
			}
			if location == "" {
				location = link
			}
			summary := value(l.Subject)
			if kind != "" {
				summary += " — " + kind
			}
			events = append(events, Event{ID: id, Summary: summary, Description: html.EscapeString(strings.Join(description, "\n")), Location: location,
				Start: DateTime{DateTime: start.In(itmo.Moscow).Format(time.RFC3339), TimeZone: s.Timezone}, End: DateTime{DateTime: end.In(itmo.Moscow).Format(time.RFC3339), TimeZone: s.Timezone},
				Extended: Properties{Private: map[string]string{"itmo_sync": owner, "itmo_account": account, "itmo_pair": fmt.Sprint(*l.PairID), "itmo_subject": value(l.Subject), "itmo_join": link}}})
		}
	}
	sort.Slice(events, func(i, j int) bool {
		if events[i].Start.DateTime == events[j].Start.DateTime {
			return events[i].ID < events[j].ID
		}
		return events[i].Start.DateTime < events[j].Start.DateTime
	})
	applyBlockReminders(events)
	// Keep today's earlier lessons as grouping context. Otherwise an hourly
	// sync during a class or its break would give the next class fresh reminders.
	future := events[:0]
	for _, event := range events {
		start, _ := time.Parse(time.RFC3339, event.Start.DateTime)
		if start.After(now) {
			future = append(future, event)
		}
	}
	return future, nil
}

// A block consists of same-day classes separated by at most 30 minutes,
// including overlaps. Only its first event carries reminders; an empty array
// explicitly removes existing overrides when the event is patched in Google.
// Input is validated and sorted by Moscow start time, then stable event ID.
func applyBlockReminders(events []Event) {
	var blockEnd time.Time
	var blockDay string
	for i := range events {
		event := &events[i]
		start, _ := time.Parse(time.RFC3339, event.Start.DateTime)
		end, _ := time.Parse(time.RFC3339, event.End.DateTime)
		day := start.In(itmo.Moscow).Format(time.DateOnly)
		event.Reminders = Reminders{Overrides: []Reminder{}}
		if blockEnd.IsZero() || day != blockDay || start.After(blockEnd.Add(30*time.Minute)) {
			event.Reminders.Overrides = []Reminder{{"popup", 1440}, {"popup", 180}, {"popup", 15}}
			blockDay, blockEnd = day, end
		} else if end.After(blockEnd) {
			blockEnd = end
		}
	}
}

func sameInstant(a, b DateTime) bool {
	x, e1 := time.Parse(time.RFC3339, a.DateTime)
	y, e2 := time.Parse(time.RFC3339, b.DateTime)
	return e1 == nil && e2 == nil && x.Equal(y)
}
func SameManagedFields(a, b Event) bool {
	if a.Summary != b.Summary || a.Description != b.Description || a.Location != b.Location || !sameInstant(a.Start, b.Start) || !sameInstant(a.End, b.End) || a.Reminders.UseDefault != b.Reminders.UseDefault || len(a.Reminders.Overrides) != len(b.Reminders.Overrides) {
		return false
	}
	counts := map[Reminder]int{}
	for _, r := range a.Reminders.Overrides {
		counts[r]++
	}
	for _, r := range b.Reminders.Overrides {
		counts[r]--
	}
	for _, n := range counts {
		if n != 0 {
			return false
		}
	}
	for k, v := range b.Extended.Private {
		if a.Extended.Private[k] != v {
			return false
		}
	}
	return true
}

var words = regexp.MustCompile(`[\pL\pN]+`)

func normalized(s string) string {
	return strings.Join(words.FindAllString(strings.ToLower(s), -1), " ")
}

// A primary-calendar event must match both times and a complete subject alias,
// or an exact ITMO classroom URL. Partial titles and recurring series are not
// deletion evidence. Only events owned by the user and without guests qualify.
func IsDuplicate(candidate, desired Event) bool {
	if candidate.Status == "cancelled" || candidate.RecurringEventID != "" || len(candidate.Recurrence) > 0 || len(candidate.Attendees) > 0 || !candidate.Organizer.Self || !sameInstant(candidate.Start, desired.Start) || !sameInstant(candidate.End, desired.End) {
		return false
	}
	if candidate.Extended.Private["itmo_sync"] == owner && candidate.Extended.Private["itmo_pair"] == desired.Extended.Private["itmo_pair"] && candidate.Extended.Private["itmo_account"] == desired.Extended.Private["itmo_account"] {
		return true
	}
	summary := normalized(candidate.Summary)
	aliases := append([]string{desired.Summary, desired.Extended.Private["itmo_subject"]}, strings.Split(desired.Extended.Private["itmo_subject"], " / ")...)
	for _, alias := range aliases {
		a := normalized(alias)
		if a != "" && (summary == a || summary == "itmo "+a || summary == "итмо "+a || summary == a+" lecture" || summary == a+" лекция") {
			return true
		}
	}
	join := desired.Extended.Private["itmo_join"]
	if join != "" {
		for _, part := range strings.FieldsFunc(html.UnescapeString(candidate.Description+" "+candidate.Location), func(r rune) bool { return unicode.IsSpace(r) || strings.ContainsRune(`<>"'()`, r) }) {
			if strings.TrimRight(part, ".,;") == join {
				return true
			}
		}
	}
	return false
}
