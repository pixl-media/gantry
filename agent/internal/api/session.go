package api

import (
	"context"
	"errors"
	"math"
	"time"
)

func (h *handlers) GetSession(ctx context.Context, _ GetSessionRequestObject) (GetSessionResponseObject, error) {
	info, _ := ctx.Value(authKey{}).(authInfo)
	s := Session{Method: SessionMethod(info.method)}
	if info.method == authCookie {
		s.ExpiresAt = &info.expires
	}
	return GetSession200JSONResponse(s), nil
}

func (h *handlers) CreateSession(ctx context.Context, req CreateSessionRequestObject) (CreateSessionResponseObject, error) {
	r := requestFrom(ctx)
	// A login from another site is never legitimate (login CSRF).
	if r.Header.Get("Origin") != "" && !h.auth.sameOrigin(r) {
		return CreateSession403JSONResponse(apiError(errors.New("cross-origin request rejected"), ErrorCodeOriginRejected)), nil
	}

	if h.auth.net.insecure(r) {
		return CreateSession403JSONResponse(apiError(errInsecure, ErrorCodeHttpsRequired)), nil
	}
	if h.auth.uiToken == "" {
		return CreateSession403JSONResponse(apiError(errLoginDisabled, ErrorCodeLoginDisabled)), nil
	}

	// Only the web login token: the API token never gets a session.
	client := h.auth.limitKey(r)
	correct := req.Body != nil && h.auth.validUIToken(req.Body.Token)
	if ok, wait := h.auth.limiter.Attempt(client, correct); !ok {
		secs := retryAfter(wait)
		return CreateSession429JSONResponse{
			Body:    apiError(errors.New("too many failed attempts"), ErrorCodeTooManyAttempts),
			Headers: CreateSession429ResponseHeaders{RetryAfter: &secs},
		}, nil
	}
	if !correct {
		h.auth.failures.record(client, "login")
		return CreateSession401JSONResponse(apiError(errors.New("invalid token"), ErrorCodeInvalidToken)), nil
	}

	value, exp := h.auth.sessions.Issue()
	cookie := h.auth.cookie(r, value, int(h.auth.sessions.TTL().Seconds()))
	cookie.Expires = exp
	c := cookie.String()
	return CreateSession204Response{Headers: CreateSession204ResponseHeaders{SetCookie: &c}}, nil
}

var errLoginDisabled = errors.New("the web login is off: the agent has no GANTRY_UI_TOKEN")

// DeleteSession ends the session (it is public, so this also works with an
// expired one) and clears the cookie.
func (h *handlers) DeleteSession(ctx context.Context, _ DeleteSessionRequestObject) (DeleteSessionResponseObject, error) {
	r := requestFrom(ctx)
	if old, err := r.Cookie(h.auth.cookieName); err == nil {
		h.auth.sessions.Revoke(old.Value)
	}
	c := h.auth.cookie(r, "", -1).String()
	return DeleteSession204Response{Headers: DeleteSession204ResponseHeaders{SetCookie: &c}}, nil
}

// retryAfter is the Retry-After value for wait, in whole seconds.
func retryAfter(wait time.Duration) int {
	return max(int(math.Ceil(wait.Seconds())), 1)
}
