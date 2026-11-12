package api

import (
	"context"
	"errors"

	"github.com/pixl-media/gantry/agent/internal/docker"
	"github.com/pixl-media/gantry/agent/internal/stacks"
)

// handlers implements the generated StrictServerInterface.
type handlers struct {
	cfg Config
}

var _ StrictServerInterface = (*handlers)(nil)

func (h *handlers) GetHealth(context.Context, GetHealthRequestObject) (GetHealthResponseObject, error) {
	return GetHealth200JSONResponse{Status: "ok"}, nil
}

func (h *handlers) GetVersion(context.Context, GetVersionRequestObject) (GetVersionResponseObject, error) {
	return GetVersion200JSONResponse{Version: h.cfg.Version}, nil
}

func (h *handlers) ListStacks(ctx context.Context, _ ListStacksRequestObject) (ListStacksResponseObject, error) {
	l, err := h.cfg.Stacks.List(ctx)
	if err != nil {
		return nil, err
	}
	resp := ListStacks200JSONResponse{
		DockerAvailable: l.DockerAvailable,
		Warnings:        orEmpty(l.Warnings),
		Stacks:          make([]StackSummary, 0, len(l.Stacks)),
	}
	for _, s := range l.Stacks {
		resp.Stacks = append(resp.Stacks, toSummary(s))
	}
	return resp, nil
}

func (h *handlers) GetStack(ctx context.Context, req GetStackRequestObject) (GetStackResponseObject, error) {
	d, err := h.cfg.Stacks.Get(ctx, req.Name)
	switch {
	case errors.Is(err, stacks.ErrInvalidName):
		return GetStack400JSONResponse{Error: err.Error()}, nil
	case errors.Is(err, stacks.ErrNotFound):
		return GetStack404JSONResponse{Error: err.Error()}, nil
	case err != nil:
		return nil, err
	}

	s := toSummary(d.Summary)
	resp := GetStack200JSONResponse{
		Name:            s.Name,
		Managed:         s.Managed,
		Status:          s.Status,
		ServicesTotal:   s.ServicesTotal,
		ServicesRunning: s.ServicesRunning,
		Path:            s.Path,
		DockerAvailable: d.DockerAvailable,
		Warnings:        orEmpty(d.Warnings),
		Services:        make([]Service, 0, len(d.Services)),
	}
	if d.ComposeFile != nil {
		resp.ComposeFile = &ComposeFile{Path: d.ComposeFile.Path, Content: d.ComposeFile.Content}
	}
	for _, svc := range d.Services {
		out := Service{Name: svc.Name, Containers: make([]Container, 0, len(svc.Containers))}
		for _, c := range svc.Containers {
			out.Containers = append(out.Containers, toContainer(c))
		}
		resp.Services = append(resp.Services, out)
	}
	return resp, nil
}

func toSummary(s stacks.Summary) StackSummary {
	return StackSummary{
		Name:            s.Name,
		Managed:         s.Managed,
		Status:          StackStatus(s.Status),
		ServicesTotal:   s.ServicesTotal,
		ServicesRunning: s.ServicesRunning,
		Path:            s.Path,
	}
}

func toContainer(c docker.Container) Container {
	out := Container{
		Id:     c.ID,
		Name:   c.Name,
		Image:  c.Image,
		State:  c.State,
		Status: c.Status,
		Health: ContainerHealth(c.Health),
		Ports:  make([]Port, 0, len(c.Ports)),
	}
	if !out.Health.Valid() {
		out.Health = ContainerHealthNone
	}
	for _, p := range c.Ports {
		port := Port{PrivatePort: p.PrivatePort, Type: p.Type}
		if p.IP != "" {
			port.Ip = &p.IP
		}
		if p.PublicPort != 0 {
			port.PublicPort = &p.PublicPort
		}
		out.Ports = append(out.Ports, port)
	}
	return out
}

// orEmpty makes nil slices encode as [] instead of null.
func orEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
