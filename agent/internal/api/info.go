package api

import (
	"context"
	"sync"
	"time"

	"github.com/pixl-media/gantry/agent/internal/docker"
)

// HostService tells about the Docker host the agent manages.
type HostService interface {
	Info(ctx context.Context) (docker.Info, error)
	ComposeVersion(ctx context.Context) (string, error)
}

// composeVersionTTL: reading the Compose version starts a process, and it
// rarely changes.
const composeVersionTTL = time.Minute

// versionCache keeps the Compose version for composeVersionTTL. Errors aren't
// kept. Concurrent requests wait for one fetch.
type versionCache struct {
	mu      sync.Mutex
	value   string
	expires time.Time
}

func (c *versionCache) get(ctx context.Context, fetch func(context.Context) (string, error)) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.value != "" && time.Now().Before(c.expires) {
		return c.value, nil
	}
	v, err := fetch(ctx)
	if err != nil {
		return "", err
	}
	c.value, c.expires = v, time.Now().Add(composeVersionTTL)
	return v, nil
}

func (h *handlers) GetInfo(ctx context.Context, _ GetInfoRequestObject) (GetInfoResponseObject, error) {
	resp := GetInfo200JSONResponse{Version: h.cfg.Version}
	if h.cfg.Host == nil {
		return resp, nil
	}
	var (
		wg         sync.WaitGroup
		info       docker.Info
		infoErr    error
		composeV   string
		composeErr error
	)
	wg.Go(func() { info, infoErr = h.cfg.Host.Info(ctx) })
	wg.Go(func() { composeV, composeErr = h.composeVersion.get(ctx, h.cfg.Host.ComposeVersion) })
	wg.Wait()

	if infoErr == nil {
		resp.DockerAvailable = true
		resp.Hostname = nonEmpty(info.Name)
		resp.OperatingSystem = nonEmpty(info.OperatingSystem)
		resp.Architecture = nonEmpty(info.Architecture)
		resp.KernelVersion = nonEmpty(info.KernelVersion)
		resp.DockerVersion = nonEmpty(info.ServerVersion)
	}
	if composeErr == nil {
		resp.ComposeVersion = nonEmpty(composeV)
	}
	return resp, nil
}

// nonEmpty returns nil for "", so that the field is left out.
func nonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
