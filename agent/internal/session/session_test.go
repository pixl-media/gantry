package session

import (
	"strings"
	"sync"
	"testing"
	"time"
)

func TestIssueAndVerify(t *testing.T) {
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	s := NewSigner("secret-token", time.Hour)
	s.now = func() time.Time { return now }

	value, exp := s.Issue()
	if !strings.HasPrefix(value, "v1.") || !exp.Equal(now.Add(time.Hour)) {
		t.Fatalf("value = %q, exp = %v", value, exp)
	}
	if got, ok := s.Verify(value); !ok || !got.Equal(exp) {
		t.Errorf("Verify = %v, %v", got, ok)
	}

	// Expired one second after the TTL.
	now = now.Add(time.Hour)
	if _, ok := s.Verify(value); ok {
		t.Error("expired session verified")
	}
}

func TestVerifyRejectsTampering(t *testing.T) {
	s := NewSigner("secret-token", time.Hour)
	value, _ := s.Issue()
	parts := strings.Split(value, ".")

	// Extending the expiry invalidates the signature.
	later := "v1.9999999999." + parts[2]
	for _, v := range []string{"", "garbage", "v2." + parts[1] + "." + parts[2], later, value + "x", "v1." + parts[1]} {
		if _, ok := s.Verify(v); ok {
			t.Errorf("Verify(%q) = ok, want rejected", v)
		}
	}
}

func TestTokenChangeInvalidatesSessions(t *testing.T) {
	value, _ := NewSigner("old-token", time.Hour).Issue()
	if _, ok := NewSigner("new-token", time.Hour).Verify(value); ok {
		t.Error("session from the old token is still valid")
	}
	if _, ok := NewSigner("old-token", time.Hour).Verify(value); !ok {
		t.Error("session should survive a restart with the same token")
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
