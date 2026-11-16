// Package session keeps the web UI's sessions and limits failed logins.
//
// Sessions live in the agent's memory: logging out ends a session at once,
// and restarting the agent ends all of them. A session ID is 256 random bits;
// the store keeps only its SHA-256, so the IDs exist only in the browsers'
// cookies.
package session

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"sync"
	"time"
)

// CookieName is the name of the session cookie.
const CookieName = "gantry_session"

// DefaultTTL is how long a session is valid.
const DefaultTTL = 12 * time.Hour

// maxSessions caps the store; beyond it the session expiring first is
// dropped. Only logins with the right token create sessions.
const maxSessions = 1000

// Store issues, verifies and revokes sessions.
type Store struct {
	ttl time.Duration
	now func() time.Time

	mu       sync.Mutex
	sessions map[[sha256.Size]byte]time.Time // hash of the ID -> expiry
}

// NewStore returns an empty Store. A zero ttl means DefaultTTL.
func NewStore(ttl time.Duration) *Store {
	if ttl == 0 {
		ttl = DefaultTTL
	}
	return &Store{ttl: ttl, now: time.Now, sessions: map[[sha256.Size]byte]time.Time{}}
}

// TTL returns how long issued sessions are valid.
func (s *Store) TTL() time.Duration { return s.ttl }

// Issue starts a session and returns its cookie value and expiry.
func (s *Store) Issue() (string, time.Time) {
	var id [32]byte
	rand.Read(id[:]) // never fails (crypto/rand)
	value := base64.RawURLEncoding.EncodeToString(id[:])
	exp := s.now().Add(s.ttl).Truncate(time.Second)

	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	for k, e := range s.sessions {
		if !now.Before(e) {
			delete(s.sessions, k)
		}
	}
	if len(s.sessions) >= maxSessions {
		var first [sha256.Size]byte
		var firstExp time.Time
		for k, e := range s.sessions {
			if firstExp.IsZero() || e.Before(firstExp) {
				first, firstExp = k, e
			}
		}
		delete(s.sessions, first)
	}
	s.sessions[sha256.Sum256([]byte(value))] = exp
	return value, exp
}

// Verify reports whether value is a live session, and its expiry.
func (s *Store) Verify(value string) (time.Time, bool) {
	key := sha256.Sum256([]byte(value))
	s.mu.Lock()
	defer s.mu.Unlock()
	exp, ok := s.sessions[key]
	if !ok {
		return time.Time{}, false
	}
	if !s.now().Before(exp) {
		delete(s.sessions, key)
		return time.Time{}, false
	}
	return exp, true
}

// Revoke ends the session value, if there is one.
func (s *Store) Revoke(value string) {
	key := sha256.Sum256([]byte(value))
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, key)
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

// Attempt records an attempt by key that succeeded or failed (the caller has
// already checked the token) and reports whether it may proceed; if not, how
// long key has to wait. While key has max failures within the window, every
// attempt is refused, a correct one too: otherwise the answer would tell
// which guess was right. Refused attempts aren't counted; a successful one
// clears key's failures. Checking and counting happen under one lock, so
// parallel attempts can't slip past the limit.
func (l *Limiter) Attempt(key string, success bool) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	recent := l.recentLocked(key)
	if len(recent) >= l.max {
		return false, recent[0].Add(l.window).Sub(l.now())
	}
	if success {
		delete(l.failures, key)
		return true, 0
	}
	l.failures[key] = append(recent, l.now())
	// Drop keys without recent failures, so the map can't grow unbounded.
	for k := range l.failures {
		if len(l.recentLocked(k)) == 0 {
			delete(l.failures, k)
		}
	}
	return true, 0
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
