// Package docker is a minimal client for the Docker Engine API over a Unix
// socket. It covers only what gantry needs and uses the standard library.
package docker

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
)

// DefaultHost is used when DOCKER_HOST is empty.
const DefaultHost = "unix:///var/run/docker.sock"

// Compose labels that Docker Compose sets on every container it creates.
const (
	LabelProject     = "com.docker.compose.project"
	LabelService     = "com.docker.compose.service"
	LabelWorkingDir  = "com.docker.compose.project.working_dir"
	LabelConfigFiles = "com.docker.compose.project.config_files"
	LabelOneoff      = "com.docker.compose.oneoff"
)

// Client talks to the Docker Engine API.
type Client struct {
	http *http.Client
	// stream has no overall timeout, for log streams that stay open; they
	// end through their context.
	stream *http.Client
}

// New returns a client for host, a DOCKER_HOST value such as
// "unix:///var/run/docker.sock". An empty host means DefaultHost.
func New(host string) (*Client, error) {
	if host == "" {
		host = DefaultHost
	}
	socket, ok := strings.CutPrefix(host, "unix://")
	if !ok || socket == "" {
		return nil, fmt.Errorf("unsupported DOCKER_HOST %q: only unix:// sockets are supported", host)
	}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", socket)
		},
	}
	return &Client{
		http:   &http.Client{Transport: transport, Timeout: 10 * time.Second},
		stream: &http.Client{Transport: transport},
	}, nil
}

// Container is the part of a container summary that gantry uses.
type Container struct {
	ID     string
	Name   string
	Image  string
	State  string // running, exited, restarting, paused, created, dead, ...
	Status string // human-readable, e.g. "Up 2 hours (healthy)"
	Health string // healthy, unhealthy, starting or none
	Labels map[string]string
	Ports  []Port
}

// Port is a container port, published or not.
type Port struct {
	IP          string
	PrivatePort int
	PublicPort  int // 0 if not published
	Type        string
}

// ListComposeContainers returns all containers, running or not, that carry a
// Compose project label.
func (c *Client) ListComposeContainers(ctx context.Context) ([]Container, error) {
	filters := `{"label":["` + LabelProject + `"]}`
	// The host part is ignored: the transport always dials the socket.
	u := "http://docker/containers/json?all=1&filters=" + url.QueryEscape(filters)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		// The cause (e.g. "dial unix ...: no such file or directory") is
		// what matters to users, not the internal request URL.
		return nil, fmt.Errorf("list containers: %w", unwrapURLError(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, statusError("list containers", resp)
	}

	var raw []apiContainer
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("list containers: decode response: %w", err)
	}
	out := make([]Container, 0, len(raw))
	for _, r := range raw {
		out = append(out, r.toContainer())
	}
	return out, nil
}

// ListProjectVolumes returns the names of the volumes Compose created for a
// project, sorted.
func (c *Client) ListProjectVolumes(ctx context.Context, project string) ([]string, error) {
	filters := `{"label":["` + LabelProject + `=` + project + `"]}`
	u := "http://docker/volumes?filters=" + url.QueryEscape(filters)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("list volumes: %w", unwrapURLError(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, statusError("list volumes", resp)
	}
	var body struct {
		Volumes []struct{ Name string }
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("list volumes: decode response: %w", err)
	}
	names := []string{}
	for _, v := range body.Volumes {
		names = append(names, v.Name)
	}
	slices.Sort(names)
	return names, nil
}

// apiContainer mirrors the Engine API's ContainerSummary fields we read.
type apiContainer struct {
	ID     string `json:"Id"`
	Names  []string
	Image  string
	State  string
	Status string
	Labels map[string]string
	Ports  []struct {
		IP          string
		PrivatePort int
		PublicPort  int
		Type        string
	}
	Health *struct {
		Status string
	}
}

func (r apiContainer) toContainer() Container {
	c := Container{
		ID:     r.ID,
		Image:  r.Image,
		State:  r.State,
		Status: r.Status,
		Labels: r.Labels,
		Health: healthFrom(r),
	}
	if len(r.Names) > 0 {
		c.Name = strings.TrimPrefix(r.Names[0], "/")
	}
	for _, p := range r.Ports {
		c.Ports = append(c.Ports, Port{IP: p.IP, PrivatePort: p.PrivatePort, PublicPort: p.PublicPort, Type: p.Type})
	}
	return c
}

// healthFrom uses the Health field of newer Engine API versions and falls
// back to the status text, e.g. "Up 2 hours (healthy)".
func healthFrom(r apiContainer) string {
	if r.Health != nil && r.Health.Status != "" {
		return r.Health.Status
	}
	switch {
	case strings.Contains(r.Status, "(healthy)"):
		return "healthy"
	case strings.Contains(r.Status, "(unhealthy)"):
		return "unhealthy"
	case strings.Contains(r.Status, "(health: starting)"):
		return "starting"
	default:
		return "none"
	}
}
