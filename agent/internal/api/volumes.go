package api

import (
	"context"
	"errors"
	"fmt"

	"github.com/pixl-media/gantry/agent/internal/stacks"
	"github.com/pixl-media/gantry/agent/internal/volumes"
)

// VolumeService measures the volumes of Compose projects.
type VolumeService interface {
	List(ctx context.Context) (volumes.Usage, error)
}

var errNoVolumes = errors.New("volume sizes aren't available")

func (h *handlers) volumeUsage(ctx context.Context) (volumes.Usage, error) {
	if h.cfg.Volumes == nil {
		return volumes.Usage{}, errNoVolumes
	}
	u, err := h.cfg.Volumes.List(ctx)
	if err != nil {
		return u, fmt.Errorf("docker didn't measure the volumes: %w", err)
	}
	return u, nil
}

func (h *handlers) ListVolumes(ctx context.Context, _ ListVolumesRequestObject) (ListVolumesResponseObject, error) {
	u, err := h.volumeUsage(ctx)
	if err != nil {
		return ListVolumes503JSONResponse(apiError(err, ErrorCodeDockerUnavailable)), nil
	}
	resp := ListVolumes200JSONResponse{Volumes: make([]Volume, 0, len(u.Volumes)), MeasuredAt: u.MeasuredAt}
	for _, v := range u.Volumes {
		resp.Volumes = append(resp.Volumes, toVolume(v))
	}
	return resp, nil
}

func (h *handlers) GetStackVolumes(ctx context.Context, req GetStackVolumesRequestObject) (GetStackVolumesResponseObject, error) {
	if !stacks.ValidName(req.Name) {
		return GetStackVolumes400JSONResponse{InvalidNameJSONResponse(apiError(stacks.ErrInvalidName, ErrorCodeInvalidName))}, nil
	}
	u, err := h.volumeUsage(ctx)
	if err != nil {
		return GetStackVolumes503JSONResponse(apiError(err, ErrorCodeDockerUnavailable)), nil
	}
	resp := GetStackVolumes200JSONResponse{Volumes: []Volume{}, MeasuredAt: u.MeasuredAt}
	for _, v := range u.Volumes {
		if v.Stack != req.Name {
			continue
		}
		resp.Volumes = append(resp.Volumes, toVolume(v))
		if v.Size > 0 {
			resp.TotalSize += v.Size
		}
	}
	return resp, nil
}

func toVolume(v volumes.Volume) Volume {
	out := Volume{Name: v.Name, Stack: v.Stack, Driver: v.Driver}
	if v.Size >= 0 {
		out.Size = ptr(v.Size)
	}
	if v.Containers >= 0 {
		out.Containers = ptr(int(v.Containers))
	}
	if !v.CreatedAt.IsZero() {
		out.CreatedAt = ptr(v.CreatedAt)
	}
	return out
}
