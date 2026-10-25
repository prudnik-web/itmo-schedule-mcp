package calendarsync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"time"
)

type Stats struct {
	Created          int  `json:"created"`
	Updated          int  `json:"updated"`
	Unchanged        int  `json:"unchanged"`
	DeletedPrimary   int  `json:"deleted_primary"`
	SkippedCancelled int  `json:"skipped_cancelled"`
	DryRun           bool `json:"dry_run"`
}

func owned(e Event, account string) bool {
	return e.Extended.Private["itmo_sync"] == owner && e.Extended.Private["itmo_account"] == account
}

// Sync only inserts/updates authoritative lessons. Absence from ITMO is never
// treated as cancellation: this source cannot distinguish a removed class from
// an incomplete day. Explicitly deleted Google events remain deleted.
func Sync(ctx context.Context, g *Google, cfg Config, dir string, desired []Event, now time.Time, dry bool) (Stats, error) {
	stats := Stats{DryRun: dry}
	current := map[string]Event{}
	if cfg.CalendarID != "" {
		events, err := g.Events(ctx, cfg.CalendarID, url.Values{"privateExtendedProperty": {"itmo_sync=" + owner, "itmo_account=" + cfg.AccountKey}, "timeMin": {now.Format(time.RFC3339)}})
		if err != nil {
			return stats, err
		}
		for _, e := range events {
			current[e.ID] = e
		}
	} else if !dry {
		return stats, errors.New("ITMO calendar has not been created")
	}
	for _, want := range desired {
		have, exists := current[want.ID]
		if !exists {
			if dry {
				stats.Created++
				continue
			}
			err := g.Insert(ctx, cfg.CalendarID, want)
			if err == nil {
				stats.Created++
				continue
			}
			if !IsStatus(err, 409) {
				return stats, err
			}
			// A previous insert may have succeeded just before a network failure,
			// or the existing event may have moved outside the listing window.
			have, err = g.GetEvent(ctx, cfg.CalendarID, want.ID)
			if IsStatus(err, 410) {
				stats.SkippedCancelled++
				continue
			}
			if err != nil {
				return stats, err
			}
		}
		if have.Status == "cancelled" {
			stats.SkippedCancelled++
			continue
		}
		if !owned(have, cfg.AccountKey) {
			return stats, errors.New("event ID belongs to an unmanaged event; refusing to overwrite")
		}
		if SameManagedFields(have, want) {
			stats.Unchanged++
			continue
		}
		if !dry {
			if have.ETag == "" {
				return stats, errors.New("event has no version tag; refusing to overwrite")
			}
			if err := g.Patch(ctx, cfg.CalendarID, want, have.ETag); err != nil {
				return stats, err
			}
		}
		stats.Updated++
	}
	if !cfg.MigratePrimary || len(desired) == 0 {
		return stats, nil
	}
	end := now.In(time.FixedZone("MSK", 3*3600)).AddDate(0, 0, cfg.LookaheadDays)
	candidates, err := g.Events(ctx, "primary", url.Values{"timeMin": {now.Format(time.RFC3339)}, "timeMax": {end.Format(time.RFC3339)}, "singleEvents": {"true"}})
	if err != nil {
		return stats, err
	}
	for _, candidate := range candidates {
		for _, want := range desired {
			if !IsDuplicate(candidate, want) {
				continue
			}
			if dry {
				stats.DeletedPrimary++
				break
			}
			// Read back the destination before deleting anything from primary.
			copy, err := g.GetEvent(ctx, cfg.CalendarID, want.ID)
			if err != nil {
				return stats, err
			}
			if copy.Status == "cancelled" || !SameManagedFields(copy, want) {
				return stats, errors.New("ITMO copy could not be verified; primary event retained")
			}
			var raw map[string]any
			if err := g.request(ctx, "GET", eventsPath("primary")+"/"+url.PathEscape(candidate.ID), nil, nil, &raw, "", true); err != nil {
				return stats, err
			}
			data, _ := json.Marshal(raw)
			var fresh Event
			if json.Unmarshal(data, &fresh) != nil || !IsDuplicate(fresh, want) {
				break
			}
			// Keep the complete original resource for recovery and an audit trail.
			backup := filepath.Join(dir, "primary-backups", fmt.Sprintf("%s-%s.json", now.UTC().Format("20060102T150405"), eventID("backup", int64Hash(fresh.ID))))
			if err := WriteJSON(backup, raw); err != nil {
				return stats, errors.New("cannot back up primary event; deletion skipped")
			}
			if err := g.Delete(ctx, "primary", fresh); err != nil {
				return stats, err
			}
			stats.DeletedPrimary++
			break
		}
	}
	return stats, nil
}

// Used only for an opaque backup filename; event matching uses Google's ID.
func int64Hash(s string) int64 {
	var h int64 = 5381
	for _, r := range s {
		h = h*33 + int64(r)
	}
	return h
}
