package session

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestIssueAndVerify(t *testing.T) {
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	s := NewStore(time.Hour)
	s.now = func() time.Time { return now }

	value, exp := s.Issue()
	if len(value) < 40 || !exp.Equal(now.Add(time.Hour)) {
		t.Fatalf("value = %q, exp = %v", value, exp)
	}
	if got, ok := s.Verify(value); !ok || !got.Equal(exp) {
		t.Errorf("Verify = %v, %v", got, ok)
	}
	if other, _ := s.Issue(); other == value {
		t.Error("two sessions got the same ID")
	}

	// Expired at the TTL.
	now = now.Add(time.Hour)
	if _, ok := s.Verify(value); ok {
		t.Error("expired session verified")
	}
}

func TestVerifyRejectsUnknownValues(t *testing.T) {
	s := NewStore(time.Hour)
	value, _ := s.Issue()
	for _, v := range []string{"", "garbage", value + "x", value[:len(value)-1]} {
		if _, ok := s.Verify(v); ok {
			t.Errorf("Verify(%q) = ok, want rejected", v)
		}
	}
	// Another store (an agent restart) knows none of them.
	if _, ok := NewStore(time.Hour).Verify(value); ok {
		t.Error("a session survived the restart")
	}
}

func TestRevoke(t *testing.T) {
	s := NewStore(time.Hour)
	a, _ := s.Issue()
	b, _ := s.Issue()
	s.Revoke(a)
	if _, ok := s.Verify(a); ok {
		t.Error("revoked session still valid")
	}
	if _, ok := s.Verify(b); !ok {
		t.Error("revoking one session ended another")
	}
	s.Revoke("unknown") // no-op
}

func TestStoreIsBounded(t *testing.T) {
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	s := NewStore(time.Hour)
	s.now = func() time.Time { return now }
	first, _ := s.Issue()
	for range maxSessions {
		now = now.Add(time.Second) // later sessions expire later
		s.Issue()
	}
	if len(s.sessions) != maxSessions {
		t.Errorf("%d sessions stored, want at most %d", len(s.sessions), maxSessions)
	}
	if _, ok := s.Verify(first); ok {
		t.Error("the oldest session should have been dropped")
	}
}

func TestLimiter(t *testing.T) {
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	l := NewLimiter(3, time.Minute)
	l.now = func() time.Time { return now }

	for i := range 3 {
		if ok, _ := l.Attempt("a", false); !ok {
			t.Fatalf("attempt %d blocked", i)
		}
		now = now.Add(time.Second)
	}
	ok, wait := l.Attempt("a", false)
	if ok || wait <= 0 || wait > time.Minute {
		t.Errorf("after 3 failures: ok = %v, wait = %v", ok, wait)
	}
	// While blocked, a correct attempt is refused too, so a guesser can't
	// tell a right guess from a wrong one.
	if ok, _ := l.Attempt("a", true); ok {
		t.Error("a correct attempt got through while blocked")
	}
	if ok, _ := l.Attempt("b", false); !ok {
		t.Error("other keys must not be affected")
	}

	// The window slides: after a minute the oldest failure no longer counts.
	now = now.Add(time.Minute - 2*time.Second)
	if ok, _ := l.Attempt("a", true); !ok {
		t.Error("still blocked after the window")
	}
	// The success cleared the failures.
	for i := range 3 {
		if ok, _ := l.Attempt("a", false); !ok {
			t.Fatalf("attempt %d after a success blocked", i)
		}
	}
}

func TestLimiterParallel(t *testing.T) {
	l := NewLimiter(10, time.Minute)
	var wg sync.WaitGroup
	var mu sync.Mutex
	passed := 0
	for range 100 {
		wg.Go(func() {
			if ok, _ := l.Attempt("a", false); ok {
				mu.Lock()
				passed++
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	if passed != 10 {
		t.Errorf("%d parallel failures got through, want 10", passed)
	}
}

func TestDone(t *testing.T) {
	s := NewStore(time.Hour)
	value, _ := s.Issue()
	done, exp, ok := s.Done(value)
	if !ok || exp.IsZero() {
		t.Fatal("Done of a live session failed")
	}
	select {
	case <-done:
		t.Fatal("done before the session ended")
	default:
	}
	s.Revoke(value)
	select {
	case <-done:
	default:
		t.Error("done not closed by Revoke")
	}
	if _, _, ok := s.Done(value); ok {
		t.Error("Done of a revoked session succeeded")
	}
}

// Many addresses can't grow the limiter without bound; beyond its capacity
// they share one bucket, so guessing stays limited.
func TestLimiterIsBounded(t *testing.T) {
	l := NewLimiter(3, time.Minute)
	l.maxKeys = 100
	for i := range 100 {
		l.Attempt(fmt.Sprint("key-", i), false)
	}
	blocked := 0
	for i := range 50 {
		if ok, _ := l.Attempt(fmt.Sprint("new-", i), false); !ok {
			blocked++
		}
	}
	if len(l.failures) > l.maxKeys+1 {
		t.Errorf("%d keys tracked, capacity %d", len(l.failures), l.maxKeys)
	}
	if blocked != 47 {
		t.Errorf("%d of 50 untracked attempts blocked, want all but the shared bucket's 3", blocked)
	}
	// Keys already tracked keep their own bucket.
	if ok, _ := l.Attempt("key-1", false); !ok {
		t.Error("a tracked key was blocked by the overflow")
	}
}
