package api

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/pixl-media/gantry/agent/internal/docker"
)

type fakeHost struct {
	info       docker.Info
	infoErr    error
	compose    string
	composeErr error
	calls      *atomic.Int32 // ComposeVersion calls
}

func (f fakeHost) Info(context.Context) (docker.Info, error) { return f.info, f.infoErr }

func (f fakeHost) ComposeVersion(context.Context) (string, error) {
	f.calls.Add(1)
	return f.compose, f.composeErr
}

func getInfo(t *testing.T, h http.Handler) AgentInfo {
	t.Helper()
	rec := do(t, h, "/api/v1/info", "t")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
	return decode[AgentInfo](t, rec)
}

func TestGetInfo(t *testing.T) {
	calls := &atomic.Int32{}
	host := fakeHost{
		info:    docker.Info{Name: "nas", ServerVersion: "28.3.1", OperatingSystem: "Ubuntu 24.04.1 LTS", Architecture: "x86_64", KernelVersion: "6.8.0"},
		compose: "2.39.1",
		calls:   calls,
	}
	h := New(Config{Token: "t", UIToken: testUIToken, Version: "0.1.1", Host: host})

	got := getInfo(t, h)
	if got.Version != "0.1.1" || !got.DockerAvailable || *got.Hostname != "nas" || *got.DockerVersion != "28.3.1" ||
		*got.OperatingSystem != "Ubuntu 24.04.1 LTS" || *got.Architecture != "x86_64" || *got.KernelVersion != "6.8.0" ||
		*got.ComposeVersion != "2.39.1" {
		t.Errorf("info = %+v", got)
	}
	// The Compose version is kept for a while: it needs a process.
	getInfo(t, h)
	if n := calls.Load(); n != 1 {
		t.Errorf("Compose asked %d times, want 1", n)
	}

	if rec := do(t, h, "/api/v1/info", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("without a token: status = %d", rec.Code)
	}
}

func TestGetInfoWithoutDocker(t *testing.T) {
	calls := &atomic.Int32{}
	host := fakeHost{infoErr: errors.New("connection refused"), compose: "2.39.1", calls: calls}
	got := getInfo(t, New(Config{Token: "t", UIToken: testUIToken, Version: "0.1.1", Host: host}))
	if got.DockerAvailable || got.Hostname != nil || got.DockerVersion != nil || got.ComposeVersion == nil {
		t.Errorf("info = %+v, want no Docker fields", got)
	}
}

func TestGetInfoWithoutCompose(t *testing.T) {
	calls := &atomic.Int32{}
	host := fakeHost{info: docker.Info{Name: "nas"}, composeErr: errors.New("not a docker command"), calls: calls}
	h := New(Config{Token: "t", UIToken: testUIToken, Version: "0.1.1", Host: host})
	got := getInfo(t, h)
	if !got.DockerAvailable || *got.Hostname != "nas" || got.ComposeVersion != nil || got.DockerVersion != nil {
		t.Errorf("info = %+v", got)
	}
	// Errors aren't kept: the next request asks again.
	getInfo(t, h)
	if n := calls.Load(); n != 2 {
		t.Errorf("Compose asked %d times, want 2", n)
	}
}
