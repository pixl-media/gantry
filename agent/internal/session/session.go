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
	sessions map[[sha256.Size]byte]*entry // hash of the ID -> session
}

type entry struct {
	exp  time.Time
	user string
	done chan struct{} // closed when the session is revoked or dropped
}

// Info describes a live session.
type Info struct {
	Expires time.Time
	// User is who logged in through single sign-on; empty for a login
	// with the web login token.
	User string
}

// NewStore returns an empty Store. A zero ttl means DefaultTTL.
func NewStore(ttl time.Duration) *Store {
	if ttl == 0 {
		ttl = DefaultTTL
	}
	return &Store{ttl: ttl, now: time.Now, sessions: map[[sha256.Size]byte]*entry{}}
}

// TTL returns how long issued sessions are valid.
func (s *Store) TTL() time.Duration { return s.ttl }

// Issue starts a session for user (empty for the web login token) and
// returns its cookie value and expiry.
func (s *Store) Issue(user string) (string, time.Time) {
	var id [32]byte
	rand.Read(id[:]) // never fails (crypto/rand)
	value := base64.RawURLEncoding.EncodeToString(id[:])
	exp := s.now().Add(s.ttl).Truncate(time.Second)

	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	for k, e := range s.sessions {
		if !now.Before(e.exp) {
			s.endLocked(k, e)
		}
	}
	if len(s.sessions) >= maxSessions {
		var first [sha256.Size]byte
		var firstEntry *entry
		for k, e := range s.sessions {
			if firstEntry == nil || e.exp.Before(firstEntry.exp) {
				first, firstEntry = k, e
			}
		}
		s.endLocked(first, firstEntry)
	}
	s.sessions[sha256.Sum256([]byte(value))] = &entry{exp: exp, user: user, done: make(chan struct{})}
	return value, exp
}

// Verify reports whether value is a live session, and what it is.
func (s *Store) Verify(value string) (Info, bool) {
	e, ok := s.live(value)
	if !ok {
		return Info{}, false
	}
	return Info{Expires: e.exp, User: e.user}, true
}

// Done returns a channel that is closed when the session value is revoked
// or dropped, and its expiry, for work that must end with the session (such
// as event streams). ok is false if value isn't a live session.
func (s *Store) Done(value string) (done <-chan struct{}, exp time.Time, ok bool) {
	e, ok := s.live(value)
	if !ok {
		return nil, time.Time{}, false
	}
	return e.done, e.exp, true
}

func (s *Store) live(value string) (*entry, bool) {
	key := sha256.Sum256([]byte(value))
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.sessions[key]
	if !ok {
		return nil, false
	}
	if !s.now().Before(e.exp) {
		s.endLocked(key, e)
		return nil, false
	}
	return e, true
}

// Revoke ends the session value, if there is one.
func (s *Store) Revoke(value string) {
	key := sha256.Sum256([]byte(value))
	s.mu.Lock()
	defer s.mu.Unlock()
	if e, ok := s.sessions[key]; ok {
		s.endLocked(key, e)
	}
}

func (s *Store) endLocked(key [sha256.Size]byte, e *entry) {
	close(e.done)
	delete(s.sessions, key)
}

// Limiter counts failed attempts per key (e.g. client address) in a sliding
// window. It tracks at most maxKeys keys, so many addresses can't make it
// grow without bound; once full, failures from new keys share one bucket.
type Limiter struct {
	max     int
	window  time.Duration
	now     func() time.Time
	maxKeys int

	mu        sync.Mutex
	failures  map[string][]time.Time
	lastPrune time.Time
}

// DefaultMaxKeys is how many keys a Limiter tracks.
const DefaultMaxKeys = 10_000

// overflowKey is the shared bucket for keys a full Limiter can't track.
const overflowKey = "\x00overflow"

// NewLimiter allows at most max failures per key within window.
func NewLimiter(max int, window time.Duration) *Limiter {
	return &Limiter{max: max, window: window, now: time.Now, maxKeys: DefaultMaxKeys, failures: map[string][]time.Time{}}
}

// Attempt records an attempt by key that succeeded or failed (the caller has
// already checked the token) and reports whether it may proceed; if not, how
// long key has to wait. While key has max failures within the window, every
// attempt is refused, a correct one too: otherwise the answer would tell
// which guess was right. Refused attempts aren't counted; a successful one
// clears key's failures. Checking and counting happen under one lock, so
// parallel attempts can't slip past the limit.
//
// When the Limiter is full, keys it doesn't track share one bucket: during
// an attack from very many addresses, new clients may then have to wait,
// but guessing stays limited.
func (l *Limiter) Attempt(key string, success bool) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	// Pruning costs a pass over all keys; do it now and then, not on every
	// attempt.
	if now.Sub(l.lastPrune) >= l.window/10 {
		l.pruneLocked()
		l.lastPrune = now
	}
	if _, tracked := l.failures[key]; !tracked && len(l.failures) >= l.maxKeys {
		key = overflowKey
	}
	recent := l.recentLocked(key)
	if len(recent) >= l.max {
		return false, recent[0].Add(l.window).Sub(now)
	}
	if success {
		if key != overflowKey {
			delete(l.failures, key)
		}
		return true, 0
	}
	l.failures[key] = append(recent, now)
	return true, 0
}

// Blocked reports whether key has to wait, and how long, without counting
// anything: for work that must not start while key is blocked, before its
// result is known. Attempt records the result afterwards.
func (l *Limiter) Blocked(key string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, tracked := l.failures[key]; !tracked && len(l.failures) >= l.maxKeys {
		key = overflowKey
	}
	recent := l.recentLocked(key)
	if len(recent) >= l.max {
		return true, recent[0].Add(l.window).Sub(l.now())
	}
	return false, 0
}

// pruneLocked drops keys without recent failures.
func (l *Limiter) pruneLocked() {
	for k := range l.failures {
		if len(l.recentLocked(k)) == 0 {
			delete(l.failures, k)
		}
	}
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
