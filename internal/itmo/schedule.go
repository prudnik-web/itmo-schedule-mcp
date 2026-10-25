package itmo

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"sort"
	"time"
)

type Schedule struct {
	DateStart string `json:"date_start"`
	DateEnd   string `json:"date_end"`
	Timezone  string `json:"timezone"`
	Days      []Day  `json:"days"`
}

type Day struct {
	Date       string   `json:"date"`
	DayNumber  *int     `json:"day_number"`
	WeekNumber *int     `json:"week_number"`
	Note       *string  `json:"note"`
	Lessons    []Lesson `json:"lessons"`
}

// Only these fields cross the MCP boundary. In particular, unknown API fields
// and zoom_password are never forwarded. Missing optional values become null.
type Lesson struct {
	PairID         *int64  `json:"pair_id"`
	SubjectID      *int64  `json:"subject_id"`
	Subject        *string `json:"subject"`
	Type           *string `json:"type"`
	TimeStart      *string `json:"time_start"`
	TimeEnd        *string `json:"time_end"`
	Start          *string `json:"start" jsonschema:"RFC3339 date-time in Europe/Moscow when a start time is provided"`
	End            *string `json:"end" jsonschema:"RFC3339 date-time in Europe/Moscow when an end time is provided"`
	TeacherID      *int64  `json:"teacher_id"`
	TeacherName    *string `json:"teacher_name"`
	Group          *string `json:"group"`
	Room           *string `json:"room"`
	Building       *string `json:"building"`
	BuildingID     *int64  `json:"bld_id"`
	MainBuildingID *int64  `json:"main_bld_id"`
	Format         *string `json:"format"`
	FormatID       *int    `json:"format_id"`
	WorkType       *string `json:"work_type"`
	WorkTypeID     *int    `json:"work_type_id"`
	FlowTypeID     *int    `json:"flow_type_id"`
	FlowID         *int64  `json:"flow_id"`
	OnlineURL      *string `json:"zoom_url"`
	Note           *string `json:"note"`
}

func (c *Client) Schedule(ctx context.Context, access string, dates DateRange) (Schedule, error) {
	query := url.Values{"date_start": {dates.Start}, "date_end": {dates.End}}
	status, _, body, err := request(ctx, c.http, http.MethodGet, c.scheduleURL+"?"+query.Encode(), nil, access, maxScheduleBytes)
	c.diagnostic("schedule", status, "")
	if err != nil {
		return Schedule{}, err
	}
	if status == http.StatusUnauthorized {
		return Schedule{}, Unauthorized
	}
	if status == http.StatusForbidden {
		var failure struct {
			Code int `json:"code"`
		}
		// ITMO code 90 means a missing/rejected token; a generic 403 can be a network block.
		if json.Unmarshal(body, &failure) == nil && failure.Code == 90 {
			return Schedule{}, Unauthorized
		}
	}
	if status != http.StatusOK {
		return Schedule{}, statusError(status)
	}
	return parseSchedule(body, dates)
}

func parseSchedule(body []byte, dates DateRange) (Schedule, error) {
	var wire struct {
		Code *int  `json:"code"`
		Data []Day `json:"data"`
	}
	if json.Unmarshal(body, &wire) != nil || wire.Code == nil || *wire.Code != 0 || wire.Data == nil || len(wire.Data) > MaxRangeDays {
		return Schedule{}, ResponseChanged
	}
	seen := make(map[string]bool)
	for i := range wire.Data {
		day := &wire.Data[i]
		if _, err := time.Parse(time.DateOnly, day.Date); err != nil || day.Date < dates.Start || day.Date > dates.End || seen[day.Date] || day.Lessons == nil || len(day.Lessons) > 200 {
			return Schedule{}, ResponseChanged
		}
		seen[day.Date] = true
		for j := range day.Lessons {
			lesson := &day.Lessons[j]
			// Do not trust upstream-derived fields that belong to our normalized schema.
			lesson.Start, lesson.End = nil, nil
			for _, pair := range []struct {
				local   *string
				instant **string
			}{{lesson.TimeStart, &lesson.Start}, {lesson.TimeEnd, &lesson.End}} {
				if pair.local == nil || *pair.local == "" {
					continue
				}
				if len(*pair.local) != 5 {
					return Schedule{}, ResponseChanged
				}
				stamp, err := time.ParseInLocation("2006-01-02 15:04", day.Date+" "+*pair.local, Moscow)
				if err != nil {
					return Schedule{}, ResponseChanged
				}
				value := stamp.Format(time.RFC3339)
				*pair.instant = &value
			}
			if lesson.Start != nil && lesson.End != nil && *lesson.End <= *lesson.Start {
				return Schedule{}, ResponseChanged
			}
			if lesson.OnlineURL != nil && *lesson.OnlineURL != "" {
				u, err := url.Parse(*lesson.OnlineURL)
				if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil {
					lesson.OnlineURL = nil
				}
			}
		}
	}
	sort.SliceStable(wire.Data, func(i, j int) bool { return wire.Data[i].Date < wire.Data[j].Date })
	return Schedule{DateStart: dates.Start, DateEnd: dates.End, Timezone: "Europe/Moscow", Days: wire.Data}, nil
}
