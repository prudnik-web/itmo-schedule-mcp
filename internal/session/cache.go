// Package session holds ITMO tokens only in memory. Entries are keyed by a
// process-random HMAC over both credentials, so a changed password cannot use an
// old authenticated session. Usernames and passwords are never retained here.
package session

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/prudnik-web/itmo-schedule-mcp/internal/itmo"
	"github.com/prudnik-web/itmo-schedule-mcp/internal/limit"
)

type Backend interface {
	Login(context.Context, itmo.Credentials) (itmo.Tokens, error)
	Refresh(context.Context, string) (itmo.Tokens, error)
	Schedule(context.Context, string, itmo.DateRange) (itmo.Schedule, error)
}

type entry struct {
	lock chan struct{}
	// refs and lastUsed are protected by Cache.mu; tokens by lock.
	refs     int
	lastUsed time.Time
	tokens   itmo.Tokens
}

type Cache struct {
	mu         sync.Mutex
	entries    map[string]*entry
	secret     [32]byte
	maximum    int
	idle       time.Duration
	now        func() time.Time
	backend    Backend
	users, ips *limit.Window
}

func New(backend Backend, maximum int, idle time.Duration) *Cache {
	if maximum <= 0 || idle <= 0 {
		panic("invalid session limits")
	}
	c := &Cache{entries: make(map[string]*entry), maximum: maximum, idle: idle,
		now: time.Now, backend: backend,
		users: limit.New(5, 10*time.Minute, 10000), ips: limit.New(20, 10*time.Minute, 10000)}
	_, _ = rand.Read(c.secret[:])
	return c
}

func (c *Cache) fingerprint(parts ...string) string {
	h := hmac.New(sha256.New, c.secret[:])
	for _, part := range parts {
		_ = binary.Write(h, binary.BigEndian, uint64(len(part)))
		_, _ = h.Write([]byte(part))
	}
	return string(h.Sum(nil))
}

func (c *Cache) acquire(ctx context.Context, key string) (*entry, error) {
	c.mu.Lock()
	now := c.now()
	e := c.entries[key]
	if e != nil && e.refs == 0 && !now.Before(e.lastUsed.Add(c.idle)) {
		delete(c.entries, key)
		e = nil
	}
	if e == nil {
		if len(c.entries) >= c.maximum {
			var oldestKey string
			var oldest *entry
			for k, candidate := range c.entries {
				if candidate.refs == 0 && (oldest == nil || candidate.lastUsed.Before(oldest.lastUsed)) {
					oldest, oldestKey = candidate, k
				}
			}
			if oldest == nil {
				c.mu.Unlock()
				return nil, itmo.RateLimited
			}
			delete(c.entries, oldestKey)
		}
		e = &entry{lock: make(chan struct{}, 1), lastUsed: now}
		c.entries[key] = e
	}
	e.refs++
	e.lastUsed = now
	c.mu.Unlock()
	select {
	case e.lock <- struct{}{}:
		return e, nil
	case <-ctx.Done():
		c.unref(e)
		return nil, itmo.Unavailable
	}
}

func (c *Cache) unref(e *entry) {
	c.mu.Lock()
	e.refs--
	e.lastUsed = c.now()
	c.mu.Unlock()
}

func (c *Cache) Sweep() {
	now := c.now()
	c.mu.Lock()
	for key, e := range c.entries {
		if e.refs == 0 && !now.Before(e.lastUsed.Add(c.idle)) {
			delete(c.entries, key)
		}
	}
	c.mu.Unlock()
	c.users.Sweep(now)
	c.ips.Sweep(now)
}

func (c *Cache) login(ctx context.Context, credentials itmo.Credentials, ip string) (itmo.Tokens, error) {
	now := c.now()
	if !c.ips.Allow(c.fingerprint("ip", ip), now) || !c.users.Allow(c.fingerprint("user", strings.ToLower(credentials.Username)), now) {
		return itmo.Tokens{}, itmo.RateLimited
	}
	return c.backend.Login(ctx, credentials)
}

func (c *Cache) ensure(ctx context.Context, e *entry, credentials itmo.Credentials, ip string) (freshLogin bool, err error) {
	now := c.now()
	if e.tokens.Access != "" && now.Add(time.Minute).Before(e.tokens.AccessExpires) {
		return false, nil
	}
	if e.tokens.Refresh != "" && (e.tokens.RefreshExpires.IsZero() || now.Before(e.tokens.RefreshExpires)) {
		tokens, err := c.backend.Refresh(ctx, e.tokens.Refresh)
		if err == nil {
			e.tokens = tokens
			return false, nil
		}
		// A temporary outage must not discard the refresh token or trigger logins.
		if !errors.Is(err, itmo.AuthenticationRequired) {
			return false, err
		}
	}
	e.tokens = itmo.Tokens{}
	tokens, err := c.login(ctx, credentials, ip)
	if err == nil {
		e.tokens = tokens
	}
	return true, err
}

func (c *Cache) Get(ctx context.Context, credentials itmo.Credentials, ip string, dates itmo.DateRange) (itmo.Schedule, error) {
	credentials.Username = strings.TrimSpace(credentials.Username)
	if credentials.Username == "" || credentials.Password == "" {
		return itmo.Schedule{}, itmo.MissingCredentials
	}
	e, err := c.acquire(ctx, c.fingerprint("credentials", credentials.Username, credentials.Password))
	if err != nil {
		return itmo.Schedule{}, err
	}
	defer func() { <-e.lock; c.unref(e) }()
	if ctx.Err() != nil {
		return itmo.Schedule{}, itmo.Unavailable
	}
	fresh, err := c.ensure(ctx, e, credentials, ip)
	if err != nil {
		return itmo.Schedule{}, err
	}
	schedule, err := c.backend.Schedule(ctx, e.tokens.Access, dates)
	if !errors.Is(err, itmo.Unauthorized) {
		return schedule, err
	}
	e.tokens = itmo.Tokens{}
	// A just-issued token already failed. Do not repeat the same login in a loop.
	if fresh {
		return itmo.Schedule{}, itmo.AuthenticationRequired
	}
	tokens, err := c.login(ctx, credentials, ip)
	if err != nil {
		return itmo.Schedule{}, err
	}
	e.tokens = tokens
	schedule, err = c.backend.Schedule(ctx, e.tokens.Access, dates)
	if errors.Is(err, itmo.Unauthorized) {
		e.tokens = itmo.Tokens{}
		return itmo.Schedule{}, itmo.AuthenticationRequired
	}
	return schedule, err
}
