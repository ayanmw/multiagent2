package middleware

import (
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// RateLimiter is an in-memory sliding-window rate limiter keyed by an arbitrary
// string (e.g. client IP or authenticated user id). It is safe for concurrent
// use and scoped to a single process — sufficient for a single-instance
// deployment. A multi-node deployment would need a shared store (e.g. Redis),
// left for later.
//
// The zero-value is unusable; construct via NewRateLimiter. now is injectable
// for deterministic tests.
type RateLimiter struct {
	mu     sync.Mutex
	limit  int
	window time.Duration
	now    func() time.Time
	hits   map[string][]time.Time // key -> in-window hit timestamps
}

// NewRateLimiter constructs a sliding-window limiter allowing limit requests
// per window per key. now may be nil (defaults to time.Now).
func NewRateLimiter(limit int, window time.Duration, now func() time.Time) *RateLimiter {
	if now == nil {
		now = time.Now
	}
	return &RateLimiter{
		limit:  limit,
		window: window,
		now:    now,
		hits:   make(map[string][]time.Time),
	}
}

// Allow records a hit for key and reports whether the request is within the
// limit. It evicts timestamps older than the window before counting (sliding
// window semantics).
func (l *RateLimiter) Allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	cutoff := now.Add(-l.window)
	valid := l.hits[key][:0]
	for _, t := range l.hits[key] {
		if t.After(cutoff) {
			valid = append(valid, t)
		}
	}
	if len(valid) >= l.limit {
		// Refresh the window so stale entries still get pruned over time.
		l.hits[key] = valid
		return false
	}
	valid = append(valid, now)
	l.hits[key] = valid
	return true
}

// RateLimit returns a Gin middleware that limits requests using keyFunc to
// derive the per-client key (e.g. client IP for login, user id for chat). When
// the key exceeds limit within window, it aborts with 429. A limit<=0 disables
// the limiter entirely (always allows), so callers can toggle it via config
// without branching.
func RateLimit(keyFunc func(*gin.Context) string, limit int, window time.Duration) gin.HandlerFunc {
	limiter := NewRateLimiter(limit, window, nil)
	return func(c *gin.Context) {
		if limit <= 0 {
			c.Next()
			return
		}
		if !limiter.Allow(keyFunc(c)) {
			c.AbortWithStatusJSON(429, gin.H{
				"error":       "rate limit exceeded",
				"retry_after": int(window.Seconds()),
			})
			return
		}
		c.Next()
	}
}

// ClientIPKey derives the rate-limit key from the request client IP. Use it for
// unauthenticated endpoints such as login/register (anti-bruteforce).
func ClientIPKey(c *gin.Context) string {
	return "ip:" + c.ClientIP()
}

// UserIDKey derives the rate-limit key from the authenticated user id set by
// AuthMiddleware; it falls back to the client IP when identity is missing (e.g.
// before auth completes). Use it for authenticated, abuse-prone endpoints such
// as chat.
func UserIDKey(c *gin.Context) string {
	if v, ok := c.Get(CtxUserID); ok {
		if id, ok := v.(uint); ok {
			return "u:" + strconv.FormatUint(uint64(id), 10)
		}
	}
	return "ip:" + c.ClientIP()
}
