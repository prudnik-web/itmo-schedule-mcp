package calendarsync

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

type Google struct {
	HTTP    *http.Client
	BaseURL string
}
type APIError struct{ Status int }

func (e *APIError) Error() string {
	return fmt.Sprintf("Google Calendar API returned HTTP %d", e.Status)
}
func IsStatus(err error, status int) bool {
	var api *APIError
	return errors.As(err, &api) && api.Status == status
}

// Retry safe reads and idempotent event writes. Calendar creation is never
// automatically retried because Google assigns its ID server-side.
func (g *Google) request(ctx context.Context, method, path string, query url.Values, body, out any, etag string, retry bool) error {
	base := g.BaseURL
	if base == "" {
		base = "https://www.googleapis.com/calendar/v3"
	}
	var data []byte
	var err error
	if body != nil {
		data, err = json.Marshal(body)
		if err != nil {
			return err
		}
	}
	for attempt := 0; attempt < 4; attempt++ {
		req, err := http.NewRequestWithContext(ctx, method, base+path+"?"+query.Encode(), bytes.NewReader(data))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		if etag != "" {
			req.Header.Set("If-Match", etag)
		}
		resp, err := g.HTTP.Do(req)
		if err != nil {
			return errors.New("Google Calendar request failed; check connectivity and authorization")
		}
		payload, readErr := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
		resp.Body.Close()
		if retry && attempt < 3 && (resp.StatusCode == 429 || resp.StatusCode >= 500) {
			delay := time.Duration(1<<attempt) * time.Second
			if n, e := strconv.Atoi(resp.Header.Get("Retry-After")); e == nil && n > 0 && n <= 60 {
				delay = time.Duration(n) * time.Second
			}
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
			continue
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return &APIError{resp.StatusCode}
		}
		if readErr != nil {
			return errors.New("could not read Google Calendar response")
		}
		if out != nil && json.Unmarshal(payload, out) != nil {
			return errors.New("invalid Google Calendar response")
		}
		return nil
	}
	return errors.New("Google Calendar retry limit reached")
}

type Calendar struct {
	ID          string `json:"id"`
	Summary     string `json:"summary"`
	Description string `json:"description"`
	AccessRole  string `json:"accessRole"`
	Primary     bool   `json:"primary"`
}

func (g *Google) EnsureCalendar(ctx context.Context, cfg *Config) (string, error) {
	if cfg.CalendarID != "" {
		var cal Calendar
		err := g.request(ctx, "GET", "/calendars/"+url.PathEscape(cfg.CalendarID), nil, nil, &cal, "", true)
		if err != nil {
			return "", err
		}
		return cfg.CalendarID, nil
	}
	marker := "Managed by ITMO Calendar Sync. Account: " + cfg.AccountKey
	q := url.Values{"maxResults": {"250"}}
	for {
		var page struct {
			Items         []Calendar `json:"items"`
			NextPageToken string     `json:"nextPageToken"`
		}
		if err := g.request(ctx, "GET", "/users/me/calendarList", q, nil, &page, "", true); err != nil {
			return "", err
		}
		for _, cal := range page.Items {
			if cal.Description == marker && cal.AccessRole == "owner" && !cal.Primary {
				cfg.CalendarID = cal.ID
				return cal.ID, nil
			}
		}
		if page.NextPageToken == "" {
			break
		}
		q.Set("pageToken", page.NextPageToken)
	}
	var cal Calendar
	err := g.request(ctx, "POST", "/calendars", nil, map[string]string{"summary": "ITMO", "timeZone": "Europe/Moscow", "description": marker}, &cal, "", false)
	if err != nil {
		return "", err
	}
	if cal.ID == "" {
		return "", errors.New("Google returned an empty calendar ID")
	}
	cfg.CalendarID = cal.ID
	return cal.ID, nil
}

func eventsPath(calendar string) string { return "/calendars/" + url.PathEscape(calendar) + "/events" }

func (g *Google) Events(ctx context.Context, calendar string, q url.Values) ([]Event, error) {
	if q == nil {
		q = url.Values{}
	}
	q.Set("maxResults", "2500")
	var result []Event
	for {
		var page struct {
			Items         []Event `json:"items"`
			NextPageToken string  `json:"nextPageToken"`
		}
		if err := g.request(ctx, "GET", eventsPath(calendar), q, nil, &page, "", true); err != nil {
			return nil, err
		}
		result = append(result, page.Items...)
		if page.NextPageToken == "" {
			break
		}
		q.Set("pageToken", page.NextPageToken)
	}
	return result, nil
}

func (g *Google) GetEvent(ctx context.Context, calendar, id string) (Event, error) {
	var e Event
	err := g.request(ctx, "GET", eventsPath(calendar)+"/"+url.PathEscape(id), nil, nil, &e, "", true)
	return e, err
}

func (g *Google) Insert(ctx context.Context, calendar string, e Event) error {
	return g.request(ctx, "POST", eventsPath(calendar), url.Values{"sendUpdates": {"none"}}, e, nil, "", true)
}

func (g *Google) Patch(ctx context.Context, calendar string, e Event, etag string) error {
	return g.request(ctx, "PATCH", eventsPath(calendar)+"/"+url.PathEscape(e.ID), url.Values{"sendUpdates": {"none"}}, e, nil, etag, true)
}

func (g *Google) Delete(ctx context.Context, calendar string, e Event) error {
	if e.ETag == "" {
		return errors.New("refusing to delete event without a version tag")
	}
	err := g.request(ctx, "DELETE", eventsPath(calendar)+"/"+url.PathEscape(e.ID), url.Values{"sendUpdates": {"none"}}, nil, nil, e.ETag, true)
	if IsStatus(err, 404) || IsStatus(err, 410) {
		return nil
	}
	return err
}
