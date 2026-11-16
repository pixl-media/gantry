package api

import (
	"log/slog"
	"sync"
	"time"
)

// failureLog logs failed authentication attempts without letting an
// attacker flood the log: up to maxFailureLines a minute individually, the
// rest as one summary per minute.
type failureLog struct {
	logger *slog.Logger

	mu          sync.Mutex
	windowStart time.Time
	logged      int
	suppressed  int
	clients     map[string]struct{} // distinct clients among the suppressed
	timer       *time.Timer
}

const (
	maxFailureLines = 10
	// maxSummaryClients bounds the set counted for the summary.
	maxSummaryClients = 10_000
)

func newFailureLog(logger *slog.Logger) *failureLog {
	return &failureLog{logger: logger, clients: map[string]struct{}{}}
}

// record logs a failed attempt by client (never the value it sent).
func (f *failureLog) record(client string, method string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := time.Now()
	if now.Sub(f.windowStart) >= time.Minute {
		f.windowStart, f.logged = now, 0
	}
	if f.logged < maxFailureLines {
		f.logged++
		f.logger.Warn("failed authentication", "client", client, "method", method)
		return
	}
	f.suppressed++
	if len(f.clients) < maxSummaryClients {
		f.clients[client] = struct{}{}
	}
	if f.timer == nil {
		f.timer = time.AfterFunc(time.Until(f.windowStart.Add(time.Minute)), f.flush)
	}
}

// flush logs the summary of the suppressed attempts, if any.
func (f *failureLog) flush() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.timer = nil
	if f.suppressed == 0 {
		return
	}
	f.logger.Warn("more failed authentication attempts, not logged one by one",
		"attempts", f.suppressed, "clients", len(f.clients))
	f.suppressed = 0
	f.clients = map[string]struct{}{}
}
