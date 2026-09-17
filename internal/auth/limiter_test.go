package auth

import (
	"fmt"
	"testing"
	"time"
)

type testClock struct{ now time.Time }

func (clock *testClock) Now() time.Time          { return clock.now }
func (clock *testClock) advance(d time.Duration) { clock.now = clock.now.Add(d) }

func newTestLimiter(budgets map[string]Budget) (*LoginLimiter, *testClock) {
	clock := &testClock{now: time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)}
	return NewLoginLimiter(LoginLimiterConfig{Budgets: budgets, Now: clock.Now}), clock
}

func TestLoginLimiterSpendsTheBurstThenRefuses(t *testing.T) {
	limiter, _ := newTestLimiter(map[string]Budget{LoginKeyUser: {Burst: 3, Refill: time.Second}})
	key := LoginKey(LoginKeyUser, "operator")
	for attempt := 1; attempt <= 3; attempt++ {
		if allowed, _ := limiter.Allow(key); !allowed {
			t.Fatalf("attempt %d was refused inside the burst", attempt)
		}
	}
	allowed, retryAfter := limiter.Allow(key)
	if allowed {
		t.Fatal("a fourth attempt was allowed past a burst of three")
	}
	// The caller is told when to come back, so a client retrying blindly is not
	// the reason the bucket never refills.
	if retryAfter <= 0 || retryAfter > time.Second {
		t.Fatalf("retryAfter = %v, want a positive wait within one refill", retryAfter)
	}
}

func TestLoginLimiterRefillsOverTime(t *testing.T) {
	limiter, clock := newTestLimiter(map[string]Budget{LoginKeyUser: {Burst: 2, Refill: 10 * time.Second}})
	key := LoginKey(LoginKeyUser, "operator")
	limiter.Allow(key)
	limiter.Allow(key)
	if allowed, _ := limiter.Allow(key); allowed {
		t.Fatal("the burst did not run out")
	}
	clock.advance(10 * time.Second)
	if allowed, _ := limiter.Allow(key); !allowed {
		t.Fatal("one refill interval did not return one attempt")
	}
	// The bucket must not keep filling past its burst while nobody is trying,
	// or an attacker banks attempts by waiting.
	clock.advance(time.Hour)
	for attempt := 1; attempt <= 2; attempt++ {
		if allowed, _ := limiter.Allow(key); !allowed {
			t.Fatalf("attempt %d was refused after a long idle period", attempt)
		}
	}
	if allowed, _ := limiter.Allow(key); allowed {
		t.Fatal("an hour of idling banked more than the burst")
	}
}

// A signed-in operator should not be closer to a refusal than somebody who has
// never tried.
func TestLoginLimiterResetsOnSuccess(t *testing.T) {
	limiter, _ := newTestLimiter(map[string]Budget{LoginKeyUser: {Burst: 2, Refill: time.Minute}})
	key := LoginKey(LoginKeyUser, "operator")
	limiter.Allow(key)
	limiter.Allow(key)
	limiter.Reset(key)
	for attempt := 1; attempt <= 2; attempt++ {
		if allowed, _ := limiter.Allow(key); !allowed {
			t.Fatalf("attempt %d was refused after a reset", attempt)
		}
	}
}

func TestLoginLimiterKeepsKeysApart(t *testing.T) {
	limiter, _ := newTestLimiter(map[string]Budget{LoginKeyUser: {Burst: 1, Refill: time.Minute}})
	first := LoginKey(LoginKeyUser, "operator")
	second := LoginKey(LoginKeyUser, "administrator")
	limiter.Allow(first)
	if allowed, _ := limiter.Allow(second); !allowed {
		t.Fatal("one username's attempts were charged to another")
	}
	if allowed, _ := limiter.Allow(first); allowed {
		t.Fatal("the first username was not limited")
	}
}

// Both keys have to pass, and the address bucket is what catches somebody
// working through a list of usernames.
func TestLoginLimiterRequiresEveryKey(t *testing.T) {
	limiter, _ := newTestLimiter(map[string]Budget{
		LoginKeyUser:    {Burst: 5, Refill: time.Minute},
		LoginKeyAddress: {Burst: 2, Refill: time.Minute},
	})
	address := LoginKey(LoginKeyAddress, "198.51.100.7")
	limiter.Allow(LoginKey(LoginKeyUser, "one"), address)
	limiter.Allow(LoginKey(LoginKeyUser, "two"), address)
	if allowed, _ := limiter.Allow(LoginKey(LoginKeyUser, "three"), address); allowed {
		t.Fatal("a third username from an exhausted address was allowed")
	}
}

// A refusal must not charge the keys that would have passed, or one exhausted
// username drains the bucket shared by everyone behind the same proxy.
func TestLoginLimiterRefusalSpendsNothing(t *testing.T) {
	limiter, _ := newTestLimiter(map[string]Budget{
		LoginKeyUser:    {Burst: 1, Refill: time.Minute},
		LoginKeyAddress: {Burst: 10, Refill: time.Minute},
	})
	address := LoginKey(LoginKeyAddress, "198.51.100.7")
	exhausted := LoginKey(LoginKeyUser, "operator")
	limiter.Allow(exhausted, address)
	for attempt := 1; attempt <= 5; attempt++ {
		if allowed, _ := limiter.Allow(exhausted, address); allowed {
			t.Fatalf("attempt %d past an exhausted username was allowed", attempt)
		}
	}
	// Nine of the address's ten remain: only the first attempt ever spent one.
	// A fresh username each time, so this measures the address bucket alone.
	for attempt := 1; attempt <= 9; attempt++ {
		username := LoginKey(LoginKeyUser, fmt.Sprintf("other-%d", attempt))
		if allowed, _ := limiter.Allow(username, address); !allowed {
			t.Fatalf("attempt %d from the same address was refused", attempt)
		}
	}
	if allowed, _ := limiter.Allow(LoginKey(LoginKeyUser, "last"), address); allowed {
		t.Fatal("an eleventh attempt from the address was allowed")
	}
}

// The keys are attacker-chosen usernames, so a map that is never swept is a way
// to spend this process's memory from outside it.
func TestLoginLimiterSweepsIdleBuckets(t *testing.T) {
	limiter, clock := newTestLimiter(map[string]Budget{LoginKeyUser: {Burst: 1, Refill: time.Minute}})
	limiter.Allow(LoginKey(LoginKeyUser, "stale"))
	clock.advance(time.Hour)
	fresh := LoginKey(LoginKeyUser, "fresh")
	limiter.Allow(fresh)
	if removed := limiter.Sweep(30 * time.Minute); removed != 1 {
		t.Fatalf("Sweep removed %d buckets, want 1", removed)
	}
	// The bucket still in use keeps its state, or a sweep is a way to reset the
	// limiter by waiting.
	if allowed, _ := limiter.Allow(fresh); allowed {
		t.Fatal("a swept limiter forgot an attempt against a live bucket")
	}
}

// An unprefixed or unbudgeted key is a wiring mistake. Refusing every sign-in
// over one would turn a typo into an outage, so it is not limited and the
// mistake stays visible as an unthrottled endpoint rather than a locked door.
func TestLoginLimiterIgnoresKeysWithoutABudget(t *testing.T) {
	limiter, _ := newTestLimiter(map[string]Budget{LoginKeyUser: {Burst: 1, Refill: time.Minute}})
	for attempt := 1; attempt <= 10; attempt++ {
		if allowed, _ := limiter.Allow("unprefixed"); !allowed {
			t.Fatalf("attempt %d against an unprefixed key was refused", attempt)
		}
		if allowed, _ := limiter.Allow(LoginKey("unbudgeted", "x")); !allowed {
			t.Fatalf("attempt %d against an unbudgeted prefix was refused", attempt)
		}
	}
}
