package api

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/pixl-media/gantry/agent/internal/volumes"
)

type fakeVolumes struct {
	usage volumes.Usage
	err   error
}

func (f fakeVolumes) List(context.Context) (volumes.Usage, error) { return f.usage, f.err }

func volumeServer(f fakeVolumes) http.Handler {
	return New(Config{Token: "t", UIToken: testUIToken, Volumes: f})
}

var measured = time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)

func TestVolumes(t *testing.T) {
	h := volumeServer(fakeVolumes{usage: volumes.Usage{MeasuredAt: measured, Volumes: []volumes.Volume{
		{Name: "app_data", Stack: "app", Driver: "local", Size: 1000, Containers: 1, CreatedAt: measured},
		{Name: "app_nfs", Stack: "app", Driver: "nfs", Size: -1, Containers: -1},
		{Name: "old_data", Stack: "old", Driver: "local", Size: 500, Containers: 0},
	}}})

	list := decode[VolumeList](t, do(t, h, "/api/v1/volumes", "t"))
	if len(list.Volumes) != 3 || !list.MeasuredAt.Equal(measured) || list.Volumes[1].Size != nil || list.Volumes[1].Containers != nil ||
		*list.Volumes[2].Containers != 0 || list.Volumes[1].CreatedAt != nil {
		t.Errorf("list = %+v", list)
	}

	rec := do(t, h, "/api/v1/stacks/app/volumes", "t")
	app := decode[StackVolumes](t, rec)
	if rec.Code != http.StatusOK || len(app.Volumes) != 2 || app.TotalSize != 1000 || *app.Volumes[0].Size != 1000 {
		t.Errorf("app = %d %+v", rec.Code, app)
	}
	// A valid name without volumes: an empty list.
	rec = do(t, h, "/api/v1/stacks/none/volumes", "t")
	if none := decode[StackVolumes](t, rec); rec.Code != http.StatusOK || len(none.Volumes) != 0 || none.TotalSize != 0 {
		t.Errorf("none = %d %+v", rec.Code, none)
	}

	if rec := do(t, h, "/api/v1/stacks/Bad%20Name/volumes", "t"); rec.Code != http.StatusBadRequest {
		t.Errorf("invalid name: status = %d", rec.Code)
	}
	for _, path := range []string{"/api/v1/volumes", "/api/v1/stacks/app/volumes"} {
		if rec := do(t, h, path, ""); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s without a token: status = %d", path, rec.Code)
		}
	}
}

func TestVolumesWithoutDocker(t *testing.T) {
	h := volumeServer(fakeVolumes{err: errors.New("connection refused")})
	for _, path := range []string{"/api/v1/volumes", "/api/v1/stacks/app/volumes"} {
		rec := do(t, h, path, "t")
		if e := decode[Error](t, rec); rec.Code != http.StatusServiceUnavailable || *e.Code != ErrorCodeDockerUnavailable {
			t.Errorf("%s = %d %+v", path, rec.Code, e)
		}
	}
}
