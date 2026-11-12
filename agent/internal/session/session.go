// Package session issues and verifies the web UI's session cookie and limits
// failed logins.
//
// Sessions are stateless: the cookie carries its expiry and an HMAC over it,
// keyed with a key derived from the agent token. Sessions survive agent
// restarts, and changing the token invalidates all of them.
package session

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"strconv"
	"strings"
	"sync"
	"time"
)

// CookieName is the name of the session cookie.
const CookieName = "gantry_session"

// DefaultTTL is how long a session is valid.
const DefaultTTL = 12 * time.Hour

// Signer issues and verifies session cookie values.
type Signer struct {
	key []byte
	ttl time.Duration
	now func() time.Time
}

// NewSigner returns a Signer whose key is derived from token. A zero ttl
// means DefaultTTL.
func NewSigner(token string, ttl time.Duration) *Signer {
	if ttl == 0 {
		ttl = DefaultTTL
	}
	mac := hmac.New(sha256.New, []byte(token))
	mac.Write([]byte("gantry session key v1"))
	return &Signer{key: mac.Sum(nil), ttl: ttl, now: time.Now}
}

// TTL returns how long issued sessions are valid.
func (s *Signer) TTL() time.Duration { return s.ttl }

// Issue returns a new cookie value and its expiry.
func (s *Signer) Issue() (string, time.Time) {
	exp := s.now().Add(s.ttl).Truncate(time.Second)
	payload := "v1." + strconv.FormatInt(exp.Unix(), 10)
	return payload + "." + s.sign(payload), exp
}

// Verify checks a cookie value and returns its expiry.
func (s *Signer) Verify(value string) (time.Time, bool) {
	version, rest, ok := strings.Cut(value, ".")
	if !ok || version != "v1" {
		return time.Time{}, false
	}
	expStr, sig, ok := strings.Cut(rest, ".")
	if !ok {
		return time.Time{}, false
	}
	payload := "v1." + expStr
	if !hmac.Equal([]byte(sig), []byte(s.sign(payload))) {
		return time.Time{}, false
	}
	unix, err := strconv.ParseInt(expStr, 10, 64)
	if err != nil {
		return time.Time{}, false
	}
	exp := time.Unix(unix, 0)
	if !s.now().Before(exp) {
		return time.Time{}, false
	}
	return exp, true
}

func (s *Signer) sign(payload string) string {
	mac := hmac.New(sha256.New, s.key)
	mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// Limiter counts failed attempts per key (e.g. client address) in a sliding
// window.
type Limiter struct {
	max    int
	window time.Duration
	now    func() time.Time

	mu       sync.Mutex
	failures map[string][]time.Time
}

// NewLimiter allows at most max failures per key within window.
func NewLimiter(max int, window time.Duration) *Limiter {
	return &Limiter{max: max, window: window, now: time.Now, failures: map[string][]time.Time{}}
}

// Allowed reports whether key may try again, and if not, how long it has to
// wait.
func (l *Limiter) Allowed(key string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	recent := l.recentLocked(key)
	if len(recent) < l.max {
		return true, 0
	}
	return false, recent[0].Add(l.window).Sub(l.now())
}

// Fail records a failed attempt.
func (l *Limiter) Fail(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.failures[key] = append(l.recentLocked(key), l.now())
	// Drop keys without recent failures, so the map can't grow unbounded.
	for k := range l.failures {
		if len(l.recentLocked(k)) == 0 {
			delete(l.failures, k)
		}
	}
}

// Reset forgets the failures of key, e.g. after a successful login.
func (l *Limiter) Reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.failures, key)
}

// recentLocked returns key's failures within the window, oldest first.
func (l *Limiter) recentLocked(key string) []time.Time {
	cutoff := l.now().Add(-l.window)
	times := l.failures[key]
	i := 0
	for i < len(times) && !times[i].After(cutoff) {
		i++
	}
	return times[i:]
}
