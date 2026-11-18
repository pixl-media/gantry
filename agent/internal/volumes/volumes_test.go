package volumes

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pixl-media/gantry/agent/internal/docker"
)

type fakeSource struct {
	calls   atomic.Int32
	volumes []docker.Volume
	err     error
	release chan struct{} // if set, VolumeUsage waits for it
}

func (f *fakeSource) VolumeUsage(context.Context) ([]docker.Volume, error) {
	f.calls.Add(1)
	if f.release != nil {
		<-f.release
	}
	return f.volumes, f.err
}

func project(name string) map[string]string { return map[string]string{docker.LabelProject: name} }

func TestListComposeVolumesSorted(t *testing.T) {
	src := &fakeSource{volumes: []docker.Volume{
		{Name: "web_data", Labels: project("web"), Size: 10, RefCount: 1},
		{Name: "loose", Size: 5}, // not from Compose
		{Name: "app_db", Labels: project("app"), Size: -1, RefCount: -1},
		{Name: "app_cache", Labels: project("app"), Size: 3, RefCount: 0},
	}}
	u, err := New(src).List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, v := range u.Volumes {
		got = append(got, v.Stack+"/"+v.Name)
	}
	if want := "app/app_cache app/app_db web/web_data"; strings.Join(got, " ") != want {
		t.Errorf("volumes = %v, want %s", got, want)
	}
	if u.Volumes[1].Size != -1 || u.MeasuredAt.IsZero() {
		t.Errorf("usage = %+v", u)
	}
}

func TestMeasurementIsKeptForAMinute(t *testing.T) {
	src := &fakeSource{}
	m := New(src)
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	m.now = func() time.Time { return now }

	for range 3 {
		if _, err := m.List(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if n := src.calls.Load(); n != 1 {
		t.Errorf("measured %d times within a minute, want 1", n)
	}
	now = now.Add(TTL)
	if _, err := m.List(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := src.calls.Load(); n != 2 {
		t.Errorf("measured %d times after a minute, want 2", n)
	}
}

func TestErrorsAreNotKept(t *testing.T) {
	src := &fakeSource{err: errors.New("docker unavailable")}
	m := New(src)
	for range 2 {
		if _, err := m.List(context.Background()); err == nil {
			t.Fatal("expected an error")
		}
	}
	if n := src.calls.Load(); n != 2 {
		t.Errorf("measured %d times, want 2", n)
	}
}

// Concurrent requests share one measurement, and a caller that gives up
// doesn't cancel it for the others.
func TestConcurrentCallsShareOneMeasurement(t *testing.T) {
	src := &fakeSource{release: make(chan struct{}), volumes: []docker.Volume{{Name: "a_x", Labels: project("a"), Size: 1}}}
	m := New(src)

	gone, cancel := context.WithCancel(context.Background())
	quit := make(chan error, 1)
	go func() { _, err := m.List(gone); quit <- err }()

	var wg sync.WaitGroup
	results := make(chan int, 5)
	for range 5 {
		wg.Go(func() {
			u, err := m.List(context.Background())
			if err == nil {
				results <- len(u.Volumes)
			}
		})
	}
	// Wait until the measurement runs, then let the first caller give up.
	for src.calls.Load() == 0 {
		time.Sleep(time.Millisecond)
	}
	cancel()
	if err := <-quit; !errors.Is(err, context.Canceled) {
		t.Errorf("canceled caller: err = %v", err)
	}
	close(src.release)
	wg.Wait()
	close(results)
	n := 0
	for got := range results {
		n++
		if got != 1 {
			t.Errorf("volumes = %d, want 1", got)
		}
	}
	if n != 5 || src.calls.Load() != 1 {
		t.Errorf("%d results from %d measurements, want 5 from 1", n, src.calls.Load())
	}
}
