package api

import (
	"context"
	"errors"
	"math"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/pixl-media/gantry/agent/internal/session"
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
	if r.Header.Get("Origin") != "" && !sameOrigin(r) {
		return CreateSession403JSONResponse(apiError(errors.New("cross-origin request rejected"), ErrorCodeOriginRejected)), nil
	}

	client := clientAddr(r)
	correct := req.Body != nil && h.auth.validToken(req.Body.Token)
	if ok, wait := h.auth.limiter.Attempt(client, correct); !ok {
		secs := retryAfter(wait)
		return CreateSession429JSONResponse{
			Body:    apiError(errors.New("too many failed attempts"), ErrorCodeTooManyAttempts),
			Headers: CreateSession429ResponseHeaders{RetryAfter: &secs},
		}, nil
	}
	if !correct {
		h.cfg.Logger.Warn("failed login", "client", client)
		return CreateSession401JSONResponse(apiError(errors.New("invalid token"), ErrorCodeInvalidToken)), nil
	}

	value, exp := h.auth.signer.Issue()
	cookie := sessionCookie(r, value, int(h.auth.signer.TTL().Seconds()))
	cookie.Expires = exp
	c := cookie.String()
	return CreateSession204Response{Headers: CreateSession204ResponseHeaders{SetCookie: &c}}, nil
}

func (h *handlers) DeleteSession(ctx context.Context, _ DeleteSessionRequestObject) (DeleteSessionResponseObject, error) {
	c := sessionCookie(requestFrom(ctx), "", -1).String()
	return DeleteSession204Response{Headers: DeleteSession204ResponseHeaders{SetCookie: &c}}, nil
}

// sessionCookie returns the session cookie with its security attributes.
// maxAge -1 deletes it.
func sessionCookie(r *http.Request, value string, maxAge int) *http.Cookie {
	return &http.Cookie{
		Name:     session.CookieName,
		Value:    value,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,                    // not readable by JavaScript
		SameSite: http.SameSiteStrictMode, // not sent with requests from other sites
		Secure:   isHTTPS(r),
	}
}

// isHTTPS reports whether the client connected over HTTPS, directly or
// through a reverse proxy that terminates TLS.
func isHTTPS(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

// retryAfter is the Retry-After value for wait, in whole seconds.
func retryAfter(wait time.Duration) int {
	return max(int(math.Ceil(wait.Seconds())), 1)
}

// clientAddr is the address failed logins are counted by. Behind a reverse
// proxy this is the proxy's address, so the limit applies to all clients
// together, which is acceptable for a single shared token.
func clientAddr(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
