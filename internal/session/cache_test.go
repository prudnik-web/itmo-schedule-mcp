package session

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/prudnik-web/itmo-schedule-mcp/internal/itmo"
)

type fakeBackend struct {
	mu                           sync.Mutex
	now                          time.Time
	logins, refreshes, schedules int
	loginError, refreshError     error
	rejectNext, rejectAlways     bool
	started, release             chan struct{}
	once                         sync.Once
}

func (f *fakeBackend) token(label string) itmo.Tokens {
	return itmo.Tokens{Access: label, Refresh: "refresh-" + label, AccessExpires: f.now.Add(time.Hour), RefreshExpires: f.now.Add(24 * time.Hour)}
}
func (f *fakeBackend) Login(ctx context.Context, credentials itmo.Credentials) (itmo.Tokens, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.logins++
	return f.token(fmt.Sprintf("login-%d-%s", f.logins, credentials.Username)), f.loginError
}
func (f *fakeBackend) Refresh(ctx context.Context, refresh string) (itmo.Tokens, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.refreshes++
	return f.token(fmt.Sprintf("refreshed-%d", f.refreshes)), f.refreshError
}
func (f *fakeBackend) Schedule(ctx context.Context, access string, dates itmo.DateRange) (itmo.Schedule, error) {
	if f.started != nil {
		f.once.Do(func() { close(f.started) })
		select {
		case <-f.release:
		case <-ctx.Done():
			return itmo.Schedule{}, itmo.Unavailable
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.schedules++
	if f.rejectNext || f.rejectAlways {
		f.rejectNext = false
		return itmo.Schedule{}, itmo.Unauthorized
	}
	return itmo.Schedule{DateStart: dates.Start, DateEnd: dates.End, Timezone: "Europe/Moscow", Days: []itmo.Day{}}, nil
}

var testCredentials = itmo.Credentials{Username: "synthetic-user", Password: "synthetic-password"}
var testDates = itmo.DateRange{Start: "2026-10-26", End: "2026-10-27"}

func newCache(t *testing.T) (*Cache, *fakeBackend) {
	t.Helper()
	f := &fakeBackend{now: time.Date(2026, 10, 25, 0, 0, 0, 0, time.UTC)}
	c := New(f, 10, 2*time.Hour)
	c.now = func() time.Time { return f.now }
	return c, f
}
func fetch(c *Cache, credentials itmo.Credentials) error {
	_, err := c.Get(context.Background(), credentials, "192.0.2.1", testDates)
	return err
}

func TestReuseAndCredentialIsolation(t *testing.T) {
	c, f := newCache(t)
	if err := fetch(c, testCredentials); err != nil {
		t.Fatal(err)
	}
	if err := fetch(c, testCredentials); err != nil {
		t.Fatal(err)
	}
	if f.logins != 1 {
		t.Fatal("did not reuse valid session")
	}
	f.loginError = itmo.AuthenticationRequired
	if err := fetch(c, itmo.Credentials{Username: testCredentials.Username, Password: "changed-password"}); err != itmo.AuthenticationRequired {
		t.Fatal("changed password reused cached authentication")
	}
	if f.schedules != 2 {
		t.Fatal("failed login reached schedule")
	}
	f.loginError = nil
	if err := fetch(c, itmo.Credentials{Username: "another-user", Password: testCredentials.Password}); err != nil {
		t.Fatal(err)
	}
	if f.logins != 3 {
		t.Fatal("users shared authentication")
	}
}

func TestConcurrentRefreshRotatesOnce(t *testing.T) {
	c, f := newCache(t)
	if err := fetch(c, testCredentials); err != nil {
		t.Fatal(err)
	}
	f.now = f.now.Add(time.Hour)
	var group sync.WaitGroup
	for range 20 {
		group.Go(func() {
			if err := fetch(c, testCredentials); err != nil {
				t.Error(err)
			}
		})
	}
	group.Wait()
	if f.logins != 1 || f.refreshes != 1 || f.schedules != 21 {
		t.Fatalf("rotation was not single-flight: logins=%d refreshes=%d schedules=%d", f.logins, f.refreshes, f.schedules)
	}
}

func TestRefreshFallbackAndTransientFailure(t *testing.T) {
	for _, refreshError := range []error{itmo.AuthenticationRequired, itmo.Unavailable, itmo.RateLimited, itmo.ResponseChanged} {
		t.Run(refreshError.Error(), func(t *testing.T) {
			c, f := newCache(t)
			if err := fetch(c, testCredentials); err != nil {
				t.Fatal(err)
			}
			f.now = f.now.Add(time.Hour)
			f.refreshError = refreshError
			err := fetch(c, testCredentials)
			if refreshError == itmo.AuthenticationRequired {
				if err != nil || f.logins != 2 {
					t.Fatal("rejected refresh should log in once")
				}
			} else {
				if err != refreshError || f.logins != 1 {
					t.Fatal("transient refresh failure triggered password login")
				}
				f.refreshError = nil
				if err := fetch(c, testCredentials); err != nil || f.logins != 1 || f.refreshes != 2 {
					t.Fatal("temporary failure discarded refresh token")
				}
			}
		})
	}
}

func TestUnauthorizedRetriesAreBounded(t *testing.T) {
	c, f := newCache(t)
	if err := fetch(c, testCredentials); err != nil {
		t.Fatal(err)
	}
	f.rejectNext = true
	if err := fetch(c, testCredentials); err != nil {
		t.Fatal(err)
	}
	if f.logins != 2 || f.schedules != 3 {
		t.Fatal("cached rejected access did not reauthenticate and retry once")
	}
	f.rejectAlways = true
	if err := fetch(c, testCredentials); err != itmo.AuthenticationRequired {
		t.Fatal("persistent 401 not classified")
	}
	if f.logins != 3 || f.schedules != 5 {
		t.Fatal("persistent 401 retried too many times")
	}
	c2, f2 := newCache(t)
	f2.rejectAlways = true
	if err := fetch(c2, testCredentials); err != itmo.AuthenticationRequired || f2.logins != 1 || f2.schedules != 1 {
		t.Fatal("fresh login retried unnecessarily")
	}
}

func TestLoginRateLimitAcrossPasswordChanges(t *testing.T) {
	c, f := newCache(t)
	f.loginError = itmo.AuthenticationRequired
	for i := range 7 {
		err := fetch(c, itmo.Credentials{Username: testCredentials.Username, Password: fmt.Sprintf("wrong-%d", i)})
		if i < 5 && err != itmo.AuthenticationRequired {
			t.Fatal("unexpected early rate limit")
		}
		if i >= 5 && err != itmo.RateLimited {
			t.Fatal("password variants bypassed login rate limit")
		}
	}
	if f.logins != 5 {
		t.Fatal("excessive login attempts")
	}
}

func TestExpiryAndEviction(t *testing.T) {
	c, f := newCache(t)
	c.maximum = 1
	if err := fetch(c, testCredentials); err != nil {
		t.Fatal(err)
	}
	f.now = f.now.Add(3 * time.Hour)
	c.Sweep()
	if len(c.entries) != 0 {
		t.Fatal("idle session retained")
	}
	if err := fetch(c, testCredentials); err != nil {
		t.Fatal(err)
	}
	if err := fetch(c, itmo.Credentials{Username: "second-user", Password: "test-password"}); err != nil {
		t.Fatal(err)
	}
	if len(c.entries) != 1 {
		t.Fatal("cache exceeded capacity")
	}
	if err := fetch(c, testCredentials); err != nil {
		t.Fatal(err)
	}
	if f.logins != 4 {
		t.Fatal("evicted session unexpectedly reused")
	}
}

func TestActiveEntriesCannotBeEvictedAndWaitsCancel(t *testing.T) {
	c, f := newCache(t)
	c.maximum = 1
	f.started = make(chan struct{})
	f.release = make(chan struct{})
	finished := make(chan error, 1)
	go func() { finished <- fetch(c, testCredentials) }()
	<-f.started
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.Get(ctx, testCredentials, "192.0.2.1", testDates); err != itmo.Unavailable {
		t.Fatal("canceled waiter did not return")
	}
	if err := fetch(c, itmo.Credentials{Username: "second-user", Password: "test-password"}); err != itmo.RateLimited {
		t.Fatal("active session was evicted")
	}
	close(f.release)
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, e := range c.entries {
		if e.refs != 0 {
			t.Fatal("canceled waiter leaked reference")
		}
	}
}
