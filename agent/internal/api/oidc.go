package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/pixl-media/gantry/agent/internal/oidc"
)

// flowCookieName carries a single sign-on login from its start to the
// callback (see oidc.Client.Start).
const flowCookieName = "gantry_oidc"

// Errors of the single sign-on, for the login page as /login?error=<code>:
// the routes are browser navigations, so they redirect instead of
// answering JSON.
const (
	ssoDisabled        = "oidc_disabled"
	ssoDenied          = "oidc_denied"
	ssoUnavailable     = "oidc_unavailable"
	ssoFailed          = "oidc_failed"
	ssoHTTPSRequired   = "https_required"
	ssoTooManyAttempts = "too_many_attempts"
)

const loginPageWithError = "/login?error="

func (h *handlers) GetLoginMethods(context.Context, GetLoginMethodsRequestObject) (GetLoginMethodsResponseObject, error) {
	return GetLoginMethods200JSONResponse(LoginMethods{Token: h.auth.uiToken != "", Oidc: h.auth.sso != nil}), nil
}

// StartOidcLogin sends the browser to the identity provider, with the flow
// cookie that the callback checks.
func (h *handlers) StartOidcLogin(ctx context.Context, req StartOidcLoginRequestObject) (StartOidcLoginResponseObject, error) {
	r := requestFrom(ctx)
	a := h.auth
	if a.sso == nil {
		return loginFailed(ssoDisabled), nil
	}
	if a.net.insecure(r) {
		return loginFailed(ssoHTTPSRequired), nil
	}
	returnTo := "/"
	if req.Params.ReturnTo != nil {
		returnTo = oidc.SafeReturn(*req.Params.ReturnTo)
	}
	authURL, flow, err := a.sso.Start(ctx, returnTo)
	if err != nil {
		// Through the failure log, which a flood of requests can't flood.
		a.failures.record(a.limitKey(r), "oidc", "err", err)
		return loginFailed(ssoErrorCode(err)), nil
	}
	return redirect{location: authURL, cookies: []*http.Cookie{a.flowCookie(r, flow, int(oidc.FlowTTL/time.Second))}}, nil
}

// FinishOidcLogin is where the identity provider sends the browser back.
// A finished login gets an ordinary session; its user is in the audit log.
func (h *handlers) FinishOidcLogin(ctx context.Context, _ FinishOidcLoginRequestObject) (FinishOidcLoginResponseObject, error) {
	r := requestFrom(ctx)
	a := h.auth
	if a.sso == nil {
		return loginFailed(ssoDisabled), nil
	}
	if a.net.insecure(r) {
		return loginFailed(ssoHTTPSRequired), nil
	}
	clear := a.flowCookie(r, "", -1) // a flow cookie works once
	client := a.limitKey(r)
	// Checked before the code exchange: a client with too many failures
	// can't keep the agent calling the identity provider.
	if blocked, _ := a.limiter.Blocked(client); blocked {
		return loginFailed(ssoTooManyAttempts, clear), nil
	}

	var flow string
	if c, err := r.Cookie(a.flowCookieName); err == nil {
		flow = c.Value
	}
	user, returnTo, err := a.sso.Finish(ctx, flow, r.URL.Query())
	switch {
	case errors.Is(err, oidc.ErrDenied):
		// A real login at the provider, just not allowed here: no guess
		// and no extra call to count. Behind a shared proxy address,
		// counting it would lock everyone out after someone's ten tries.
		a.failures.record(client, "oidc", "err", "not on the allow list (GANTRY_OIDC_ALLOWED_GROUPS, GANTRY_OIDC_ALLOWED_EMAILS)",
			"user", user.Name, "subject", user.Subject)
		return loginFailed(ssoDenied, clear), nil
	case errors.Is(err, oidc.ErrNoLogin), errors.Is(err, oidc.ErrRefused):
		// Not started in this browser, too long ago, or ended at the
		// provider (e.g. canceled): nothing was asked of the provider.
		a.failures.record(client, "oidc", "err", err)
		return loginFailed(ssoFailed, clear), nil
	}
	// Codes and ID tokens that don't hold up count like wrong tokens.
	if ok, _ := a.limiter.Attempt(client, err == nil); !ok {
		return loginFailed(ssoTooManyAttempts, clear), nil
	}
	if err != nil {
		a.failures.record(client, "oidc", "err", err)
		return loginFailed(ssoErrorCode(err), clear), nil
	}

	value, exp := a.sessions.Issue(user.Name)
	session := a.cookie(r, value, int(a.sessions.TTL().Seconds()))
	session.Expires = exp
	// With the subject, which the user can't change at the provider: the
	// audit log's user= is the name, which may be.
	a.logger.Info("single sign-on login", "client", a.net.client(r).String(), "user", user.Name, "subject", user.Subject)
	return redirect{location: returnTo, cookies: []*http.Cookie{session, clear}}, nil
}

// flowCookie is the cookie that carries a single sign-on login, with the
// session cookie's attributes but SameSite=Lax: the provider's redirect
// back is a navigation from another site, which never carries Strict
// cookies. It is only a login in progress, not a credential.
func (a *authenticator) flowCookie(r *http.Request, value string, maxAge int) *http.Cookie {
	c := a.cookie(r, value, maxAge)
	c.Name = a.flowCookieName
	c.SameSite = http.SameSiteLaxMode
	return c
}

func ssoErrorCode(err error) string {
	switch {
	case errors.Is(err, oidc.ErrDenied):
		return ssoDenied
	case errors.Is(err, oidc.ErrUnavailable):
		return ssoUnavailable
	default:
		return ssoFailed
	}
}

// redirect answers 302 with cookies; there can be two (the session and the
// cleared flow cookie), which generated header fields can't carry.
type redirect struct {
	location string
	cookies  []*http.Cookie
}

func loginFailed(code string, cookies ...*http.Cookie) redirect {
	return redirect{location: loginPageWithError + code, cookies: cookies}
}

func (d redirect) visit(w http.ResponseWriter) error {
	for _, c := range d.cookies {
		http.SetCookie(w, c)
	}
	// The callback's URL holds the code and state; don't pass it on.
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Location", d.location)
	w.WriteHeader(http.StatusFound)
	return nil
}

func (d redirect) VisitStartOidcLoginResponse(w http.ResponseWriter) error  { return d.visit(w) }
func (d redirect) VisitFinishOidcLoginResponse(w http.ResponseWriter) error { return d.visit(w) }
