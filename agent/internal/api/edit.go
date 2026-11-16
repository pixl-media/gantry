package api

import (
	"context"
	"errors"
	"io/fs"
	"net/http"
	"slices"

	"github.com/pixl-media/gantry/agent/internal/compose"
	"github.com/pixl-media/gantry/agent/internal/stackfiles"
	"github.com/pixl-media/gantry/agent/internal/stacks"
)

// FileService is what the API needs from the stackfiles package.
type FileService interface {
	ReplaceCompose(ctx context.Context, p compose.Project, version string, content []byte) (string, error)
	Env(p compose.Project) (stackfiles.EnvInfo, error)
	UpdateEnv(p compose.Project, version string, set map[string]string, del []string) (stackfiles.EnvInfo, error)
	RevealEnv(p compose.Project, key string) (string, error)
	Create(ctx context.Context, stacksDir, name string, compose []byte, env map[string]string) (string, error)
}

// failure is an error answer: an HTTP status and the body.
type failure struct {
	status int
	body   Error
}

func fail(status int, code ErrorCode, err error) *failure {
	return &failure{status, apiError(err, code)}
}

// managed resolves a managed stack for changing it.
func (h *handlers) managed(ctx context.Context, name string) (compose.Project, *failure, error) {
	p, err := h.cfg.Stacks.Target(ctx, name)
	switch {
	case errors.Is(err, stacks.ErrInvalidName):
		return p, fail(http.StatusBadRequest, ErrorCodeInvalidName, err), nil
	case errors.Is(err, stacks.ErrNotFound):
		return p, fail(http.StatusNotFound, ErrorCodeNotFound, err), nil
	case errors.Is(err, stacks.ErrUnmanaged):
		return p, fail(http.StatusConflict, ErrorCodeStackUnmanaged, err), nil
	case err != nil:
		return p, nil, err
	}
	return p, nil, nil
}

// fileFailure maps stackfiles and compose errors to answers; nil means an
// unexpected error (500).
func fileFailure(err error) *failure {
	var invalidCompose *compose.InvalidError
	var invalidVar *stackfiles.InvalidVariableError
	switch {
	case errors.Is(err, stackfiles.ErrConflict):
		return fail(http.StatusConflict, ErrorCodeVersionConflict, err)
	case errors.As(err, &invalidCompose):
		return &failure{http.StatusBadRequest, Error{Error: invalidCompose.Message, Code: ptr(ErrorCodeInvalidCompose)}}
	case errors.As(err, &invalidVar):
		return fail(http.StatusBadRequest, ErrorCodeInvalidVariable, err)
	case errors.Is(err, stackfiles.ErrTooLarge), errors.Is(err, stackfiles.ErrSymlink):
		return fail(http.StatusBadRequest, ErrorCodeInvalidRequest, err)
	case errors.Is(err, stackfiles.ErrVariableNotFound), errors.Is(err, fs.ErrNotExist):
		return fail(http.StatusNotFound, ErrorCodeNotFound, err)
	case errors.Is(err, stackfiles.ErrExists):
		return fail(http.StatusConflict, ErrorCodeStackExists, err)
	}
	return nil
}

func ptr[T any](v T) *T { return &v }

func (h *handlers) UpdateCompose(ctx context.Context, req UpdateComposeRequestObject) (UpdateComposeResponseObject, error) {
	if req.Body == nil {
		return UpdateCompose400JSONResponse(apiError(errors.New("missing request body"), ErrorCodeInvalidRequest)), nil
	}
	p, f, err := h.managed(ctx, req.Name)
	if err == nil && f == nil {
		var version string
		version, err = h.cfg.Files.ReplaceCompose(ctx, p, req.Body.Version, []byte(req.Body.Content))
		if err == nil {
			h.cfg.Logger.Info("compose file saved", "stack", req.Name, "bytes", len(req.Body.Content))
			return UpdateCompose200JSONResponse{Version: version}, nil
		}
		f = fileFailure(err)
	}
	if f == nil {
		return nil, err
	}
	switch f.status {
	case http.StatusNotFound:
		return UpdateCompose404JSONResponse{NotFoundJSONResponse(f.body)}, nil
	case http.StatusConflict:
		return UpdateCompose409JSONResponse(f.body), nil
	default:
		return UpdateCompose400JSONResponse(f.body), nil
	}
}

func toEnvFile(info stackfiles.EnvInfo) EnvFile {
	vars := make([]EnvVariable, 0, len(info.Keys))
	for _, k := range info.Keys {
		vars = append(vars, EnvVariable{Key: k})
	}
	return EnvFile{Exists: info.Exists, Version: info.Version, Variables: vars}
}

func (h *handlers) GetEnv(ctx context.Context, req GetEnvRequestObject) (GetEnvResponseObject, error) {
	p, f, err := h.managed(ctx, req.Name)
	if err == nil && f == nil {
		var info stackfiles.EnvInfo
		if info, err = h.cfg.Files.Env(p); err == nil {
			return GetEnv200JSONResponse(toEnvFile(info)), nil
		}
		f = fileFailure(err)
	}
	if f == nil {
		return nil, err
	}
	switch f.status {
	case http.StatusNotFound:
		return GetEnv404JSONResponse{NotFoundJSONResponse(f.body)}, nil
	case http.StatusConflict:
		return GetEnv409JSONResponse(f.body), nil
	default:
		return GetEnv400JSONResponse{InvalidNameJSONResponse(f.body)}, nil
	}
}

func (h *handlers) UpdateEnv(ctx context.Context, req UpdateEnvRequestObject) (UpdateEnvResponseObject, error) {
	if req.Body == nil {
		return UpdateEnv400JSONResponse(apiError(errors.New("missing request body"), ErrorCodeInvalidRequest)), nil
	}
	set := map[string]string{}
	if req.Body.Set != nil {
		set = *req.Body.Set
	}
	var del []string
	if req.Body.Delete != nil {
		del = *req.Body.Delete
	}
	p, f, err := h.managed(ctx, req.Name)
	if err == nil && f == nil {
		var info stackfiles.EnvInfo
		if info, err = h.cfg.Files.UpdateEnv(p, req.Body.Version, set, del); err == nil {
			keys := make([]string, 0, len(set))
			for k := range set {
				keys = append(keys, k)
			}
			slices.Sort(keys)
			// Names only, never values.
			h.cfg.Logger.Info("env updated", "stack", req.Name, "set", keys, "deleted", del)
			return UpdateEnv200JSONResponse(toEnvFile(info)), nil
		}
		f = fileFailure(err)
	}
	if f == nil {
		return nil, err
	}
	switch f.status {
	case http.StatusNotFound:
		return UpdateEnv404JSONResponse{NotFoundJSONResponse(f.body)}, nil
	case http.StatusConflict:
		return UpdateEnv409JSONResponse(f.body), nil
	default:
		return UpdateEnv400JSONResponse(f.body), nil
	}
}

func (h *handlers) RevealEnvValue(ctx context.Context, req RevealEnvValueRequestObject) (RevealEnvValueResponseObject, error) {
	if req.Body == nil {
		return RevealEnvValue400JSONResponse(apiError(errors.New("missing request body"), ErrorCodeInvalidRequest)), nil
	}
	key := req.Body.Key
	if !stackfiles.ValidKey(key) {
		return RevealEnvValue400JSONResponse(apiError(errors.New("not a valid variable name"), ErrorCodeInvalidVariable)), nil
	}
	p, f, err := h.managed(ctx, req.Name)
	if err == nil && f == nil {
		var value string
		if value, err = h.cfg.Files.RevealEnv(p, key); err == nil {
			// Who saw which variable, never the value.
			h.cfg.Logger.Info("env value revealed", "stack", req.Name, "key", key,
				"client", clientAddr(requestFrom(ctx)))
			return RevealEnvValue200JSONResponse{Key: key, Value: value}, nil
		}
		f = fileFailure(err)
	}
	if f == nil {
		return nil, err
	}
	switch f.status {
	case http.StatusNotFound:
		return RevealEnvValue404JSONResponse(f.body), nil
	case http.StatusConflict:
		return RevealEnvValue409JSONResponse(f.body), nil
	default:
		return RevealEnvValue400JSONResponse(f.body), nil
	}
}

func (h *handlers) CreateStack(ctx context.Context, req CreateStackRequestObject) (CreateStackResponseObject, error) {
	if req.Body == nil {
		return CreateStack400JSONResponse(apiError(errors.New("missing request body"), ErrorCodeInvalidRequest)), nil
	}
	name := req.Body.Name
	if !stacks.ValidName(name) {
		return CreateStack400JSONResponse(apiError(stacks.ErrInvalidName, ErrorCodeInvalidName)), nil
	}
	// The name must be free as a directory and as a Compose project, or the
	// new stack would take over an existing project's containers.
	switch _, err := h.cfg.Stacks.Target(ctx, name); {
	case err == nil, errors.Is(err, stacks.ErrUnmanaged):
		return CreateStack409JSONResponse(apiError(stackfiles.ErrExists, ErrorCodeStackExists)), nil
	case errors.Is(err, stacks.ErrDockerUnavailable):
		// Can't check for an existing project of that name, whose
		// containers the new stack would take over.
		return CreateStack503JSONResponse(apiError(errors.New("docker is unavailable, so it can't be checked whether the name is in use"), ErrorCodeDockerUnavailable)), nil
	case !errors.Is(err, stacks.ErrNotFound):
		return nil, err
	}

	var env map[string]string
	if req.Body.Env != nil {
		env = *req.Body.Env
	}
	dir, err := h.cfg.Files.Create(ctx, h.cfg.Stacks.Dir(), name, []byte(req.Body.Compose), env)
	if err != nil {
		f := fileFailure(err)
		switch {
		case f == nil:
			return nil, err
		case f.status == http.StatusConflict:
			return CreateStack409JSONResponse(f.body), nil
		default:
			return CreateStack400JSONResponse(f.body), nil
		}
	}
	h.cfg.Logger.Info("stack created", "stack", name, "dir", dir)
	location := "/api/v1/stacks/" + name
	return CreateStack201JSONResponse{
		Body:    StackCreated{Name: name, Path: dir},
		Headers: CreateStack201ResponseHeaders{Location: &location},
	}, nil
}

func (h *handlers) DeleteStack(ctx context.Context, req DeleteStackRequestObject) (DeleteStackResponseObject, error) {
	if req.Params.Confirm != req.Name {
		return DeleteStack400JSONResponse(apiError(errors.New("confirm must repeat the stack name"), ErrorCodeInvalidRequest)), nil
	}
	volumes := req.Params.Volumes != nil && *req.Params.Volumes
	p, f, err := h.managed(ctx, req.Name)
	if err != nil {
		return nil, err
	}
	if f != nil {
		switch f.status {
		case http.StatusNotFound:
			return DeleteStack404JSONResponse{NotFoundJSONResponse(f.body)}, nil
		case http.StatusConflict:
			return DeleteStack409JSONResponse(f.body), nil
		default:
			return DeleteStack400JSONResponse(f.body), nil
		}
	}
	name := req.Name
	op, err := h.cfg.Operations.Delete(p, volumes, func() error { return h.cfg.Stacks.Remove(name) })
	if err != nil {
		return nil, err
	}
	h.cfg.Logger.Info("stack delete started", "stack", name, "volumes", volumes, "operation", op.ID())
	location := "/api/v1/operations/" + op.ID()
	return DeleteStack202JSONResponse{
		Body:    toOperation(op.Snapshot()),
		Headers: DeleteStack202ResponseHeaders{Location: &location},
	}, nil
}
