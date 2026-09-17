package auth

import (
	"sync"
	"time"
)

// LoginLimiter bounds sign-in attempts.
//
// Checked before the password is hashed, because hashing is the expensive half:
// a limiter that runs after the derivation has already paid for the attack it
// is refusing.
//
// In LDAP mode it is not protecting promview at all. Every failed bind promview
// forwards is a failed bind against a directory account with its own lockout
// policy, so without per-username throttling in front of the bind, this login
// endpoint is a way to lock every employee out of everything.
type LoginLimiter struct {
	mu      sync.Mutex
	buckets map[string]*loginBucket
	budgets map[string]Budget
	// now is injected so the tests are tests of the algorithm rather than of
	// how long the machine took to run them.
	now func() time.Time
}

// Budget is a token bucket: Burst attempts at once, then one more every Refill.
type Budget struct {
	Burst  int
	Refill time.Duration
}

type loginBucket struct {
	tokens   float64
	lastSeen time.Time
	budget   Budget
}

// LoginLimiterConfig sets one budget per key prefix. A key whose prefix has no
// budget is not limited, which is deliberate: an unconfigured prefix is a
// mistake to notice, not a reason to refuse every sign-in.
type LoginLimiterConfig struct {
	Budgets map[string]Budget
	Now     func() time.Time
}

// DefaultLoginBudgets throttles per username and per address.
//
// The per-username budget is the one doing the real work. Behind an ingress
// every request arrives from the same address, so the address budget collapses
// to one global bucket - and keying it on X-Forwarded-For instead would make it
// free to bypass, since nothing here knows which proxies to trust.
func DefaultLoginBudgets() map[string]Budget {
	return map[string]Budget{
		LoginKeyUser:    {Burst: 5, Refill: 12 * time.Second},
		LoginKeyAddress: {Burst: 20, Refill: 3 * time.Second},
	}
}

const (
	LoginKeyUser    = "user"
	LoginKeyAddress = "addr"
)

func NewLoginLimiter(config LoginLimiterConfig) *LoginLimiter {
	now := config.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	budgets := config.Budgets
	if budgets == nil {
		budgets = DefaultLoginBudgets()
	}
	return &LoginLimiter{buckets: map[string]*loginBucket{}, budgets: budgets, now: now}
}

// LoginKey identifies one bucket. Prefixed so a username can never collide with
// an address.
func LoginKey(prefix, value string) string { return prefix + ":" + value }

// Allow reports whether an attempt may proceed and, when it may not, how long
// until one may.
//
// Every key must pass, and a refusal by any of them spends nothing: a caller
// already over their username budget should not also be draining the bucket
// shared by everyone behind their proxy.
func (limiter *LoginLimiter) Allow(keys ...string) (bool, time.Duration) {
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	now := limiter.now()

	buckets := make([]*loginBucket, 0, len(keys))
	var longest time.Duration
	allowed := true
	for _, key := range keys {
		bucket := limiter.bucketLocked(key, now)
		if bucket == nil {
			continue
		}
		if bucket.tokens < 1 {
			allowed = false
			if wait := bucket.waitFor(1); wait > longest {
				longest = wait
			}
			continue
		}
		buckets = append(buckets, bucket)
	}
	if !allowed {
		return false, longest
	}
	for _, bucket := range buckets {
		bucket.tokens--
	}
	return true, 0
}

// Reset forgets the attempts recorded against these keys, so a successful
// sign-in does not leave its owner closer to a refusal than someone who has not
// signed in at all.
func (limiter *LoginLimiter) Reset(keys ...string) {
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	for _, key := range keys {
		delete(limiter.buckets, key)
	}
}

// Sweep drops buckets untouched for longer than idle, and reports how many.
//
// Not housekeeping: the keys are attacker-chosen usernames, so a map that is
// never swept is a way to spend this process's memory from outside it.
func (limiter *LoginLimiter) Sweep(idle time.Duration) int {
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	cutoff := limiter.now().Add(-idle)
	removed := 0
	for key, bucket := range limiter.buckets {
		if bucket.lastSeen.Before(cutoff) {
			delete(limiter.buckets, key)
			removed++
		}
	}
	return removed
}

func (limiter *LoginLimiter) bucketLocked(key string, now time.Time) *loginBucket {
	prefix, _, found := cutKeyPrefix(key)
	if !found {
		return nil
	}
	budget, configured := limiter.budgets[prefix]
	if !configured || budget.Burst < 1 || budget.Refill <= 0 {
		return nil
	}
	bucket, exists := limiter.buckets[key]
	if !exists {
		bucket = &loginBucket{tokens: float64(budget.Burst), lastSeen: now, budget: budget}
		limiter.buckets[key] = bucket
		return bucket
	}
	bucket.budget = budget
	bucket.refill(now)
	return bucket
}

func (bucket *loginBucket) refill(now time.Time) {
	elapsed := now.Sub(bucket.lastSeen)
	bucket.lastSeen = now
	if elapsed <= 0 {
		return
	}
	bucket.tokens += float64(elapsed) / float64(bucket.budget.Refill)
	if ceiling := float64(bucket.budget.Burst); bucket.tokens > ceiling {
		bucket.tokens = ceiling
	}
}

func (bucket *loginBucket) waitFor(tokens float64) time.Duration {
	missing := tokens - bucket.tokens
	if missing <= 0 {
		return 0
	}
	return time.Duration(missing * float64(bucket.budget.Refill))
}

func cutKeyPrefix(key string) (prefix, value string, found bool) {
	for index := range len(key) {
		if key[index] == ':' {
			return key[:index], key[index+1:], true
		}
	}
	return "", "", false
}
