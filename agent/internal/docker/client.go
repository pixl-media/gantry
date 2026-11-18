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

// Info is the part of Docker's system information that gantry uses.
type Info struct {
	Name            string // the host's name
	ServerVersion   string
	OperatingSystem string
	Architecture    string
	KernelVersion   string
}

// Info returns the Docker host's system information.
func (c *Client) Info(ctx context.Context) (Info, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://docker/info", nil)
	if err != nil {
		return Info{}, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return Info{}, fmt.Errorf("docker info: %w", unwrapURLError(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Info{}, statusError("docker info", resp)
	}
	var info Info
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return Info{}, fmt.Errorf("docker info: decode response: %w", err)
	}
	return info, nil
}

// Volume is a Docker volume with its disk usage.
type Volume struct {
	Name      string
	Driver    string
	Labels    map[string]string
	CreatedAt time.Time // zero if unknown
	Size      int64     // bytes; -1 if Docker doesn't know it
	RefCount  int64     // containers using it; -1 if unknown
}

// dfTimeout limits VolumeUsage: Docker walks every volume, which takes a
// while for large ones, longer than the client's usual timeout.
const dfTimeout = 2 * time.Minute

type apiVolume struct {
	Name      string
	Driver    string
	Labels    map[string]string
	CreatedAt string
	UsageData *struct {
		Size     int64
		RefCount int64
	}
}

func (v apiVolume) toVolume() Volume {
	out := Volume{Name: v.Name, Driver: v.Driver, Labels: v.Labels, Size: -1, RefCount: -1}
	if t, err := time.Parse(time.RFC3339Nano, v.CreatedAt); err == nil {
		out.CreatedAt = t
	}
	if v.UsageData != nil {
		out.Size, out.RefCount = v.UsageData.Size, v.UsageData.RefCount
	}
	return out
}

// VolumeUsage returns all volumes with their sizes, as `docker system df -v`
// measures them. Newer Docker versions answer in VolumeUsage.Items, older
// ones in Volumes; older ones also ignore type=volume and measure images and
// containers too, which only takes longer.
func (c *Client) VolumeUsage(ctx context.Context) ([]Volume, error) {
	ctx, cancel := context.WithTimeout(ctx, dfTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://docker/system/df?type=volume", nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.stream.Do(req)
	if err != nil {
		return nil, fmt.Errorf("volume usage: %w", unwrapURLError(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, statusError("volume usage", resp)
	}
	var body struct {
		Volumes     []apiVolume
		VolumeUsage *struct{ Items []apiVolume }
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("volume usage: decode response: %w", err)
	}
	raw := body.Volumes
	if body.VolumeUsage != nil && body.VolumeUsage.Items != nil {
		raw = body.VolumeUsage.Items
	}
	out := make([]Volume, 0, len(raw))
	for _, v := range raw {
		out = append(out, v.toVolume())
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
