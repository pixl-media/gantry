// Command gantry-agent is the headless gantry agent: it runs on a Docker host
// and exposes an HTTP API for managing the Compose stacks on it.
package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/pixl-media/gantry/agent/internal/api"
	"github.com/pixl-media/gantry/agent/internal/compose"
	"github.com/pixl-media/gantry/agent/internal/docker"
	"github.com/pixl-media/gantry/agent/internal/live"
	"github.com/pixl-media/gantry/agent/internal/logs"
	"github.com/pixl-media/gantry/agent/internal/oidc"
	"github.com/pixl-media/gantry/agent/internal/operations"
	"github.com/pixl-media/gantry/agent/internal/stackfiles"
	"github.com/pixl-media/gantry/agent/internal/stacks"
	"github.com/pixl-media/gantry/agent/internal/tlscert"
	"github.com/pixl-media/gantry/agent/internal/volumes"
)

// version is set at build time via -ldflags "-X main.version=...".
var version = "dev"

// minTokenLength is the shortest token the agent accepts. The login limit
// slows guessing down, but against many addresses only a long random token
// makes it hopeless; the token is root on the host.
const minTokenLength = 24

// minTokenChars is how many different characters a token needs at least. A
// random token of 24 hex digits has fewer with a chance of about 1 in 30,000.
const minTokenChars = 8

const tokenAdvice = "use at least 24 random characters, e.g. from `openssl rand -hex 24`"

// checkToken rejects tokens that are obviously weak: short, made of few
// characters, or one piece repeated. It can't tell a random token from a
// chosen one, so the advice stays: generate it. name is the variable, for
// the error. Errors never contain the token.
func checkToken(name, token string) error {
	if len(token) < minTokenLength {
		return fmt.Errorf("%s is too short (%d characters, at least %d); %s", name, len(token), minTokenLength, tokenAdvice)
	}
	chars := map[rune]bool{}
	for _, r := range token {
		chars[r] = true
	}
	if len(chars) < minTokenChars {
		return fmt.Errorf("%s uses only %d different characters; %s", name, len(chars), tokenAdvice)
	}
	for period := 1; period <= len(token)/2; period++ {
		if repeats(token, period) {
			return fmt.Errorf("%s repeats a piece of %d characters; %s", name, period, tokenAdvice)
		}
	}
	return nil
}

// repeats reports whether s consists of its first n bytes over and over.
func repeats(s string, n int) bool {
	for i := n; i < len(s); i++ {
		if s[i] != s[i-n] {
			return false
		}
	}
	return true
}

// loadTokens returns the API token (GANTRY_TOKEN, bearer only) and the web
// login token (GANTRY_UI_TOKEN, login only), and warnings. Each is optional,
// which turns its way in off, but at least one way in is needed: one of
// them, or single sign-on (sso). Both are checked, and they must differ:
// the point of two is replacing one without the other (docs/decisions.md,
// decision 26).
func loadTokens(sso bool) (apiToken, uiToken string, warnings []string, err error) {
	apiToken, warning, err := loadSecret("GANTRY_TOKEN")
	if err != nil {
		return "", "", nil, err
	}
	if warning != "" {
		warnings = append(warnings, warning)
	}
	uiToken, warning, err = loadSecret("GANTRY_UI_TOKEN")
	if err != nil {
		return "", "", nil, err
	}
	if warning != "" {
		warnings = append(warnings, warning)
	}
	if apiToken == "" && uiToken == "" && !sso {
		return "", "", nil, fmt.Errorf("set GANTRY_TOKEN (API), GANTRY_UI_TOKEN (web login), GANTRY_OIDC_ISSUER and the rest of single sign-on, or several; %s", tokenAdvice)
	}
	for name, token := range map[string]string{"GANTRY_TOKEN": apiToken, "GANTRY_UI_TOKEN": uiToken} {
		if token == "" {
			continue
		}
		if err := checkToken(name, token); err != nil {
			return "", "", nil, err
		}
	}
	if apiToken != "" && apiToken == uiToken {
		return "", "", nil, errors.New("GANTRY_TOKEN and GANTRY_UI_TOKEN are the same; generate a separate one for each")
	}
	return apiToken, uiToken, warnings, nil
}

// loadSecret returns the secret from the variable name, or from the file
// that name_FILE names (for systemd credentials or Docker secrets, which
// keep it out of the process environment), and a warning, if any. Neither
// set is "".
func loadSecret(name string) (token, warning string, err error) {
	fileVar := name + "_FILE"
	value, file := os.Getenv(name), os.Getenv(fileVar)
	switch {
	case value != "" && file != "":
		return "", "", fmt.Errorf("set %s or %s, not both", name, fileVar)
	case file == "":
		return value, "", nil
	}
	info, err := os.Stat(file)
	if err != nil {
		return "", "", fmt.Errorf("%s: %w", fileVar, err)
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return "", "", fmt.Errorf("%s: %w", fileVar, err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		warning = fmt.Sprintf("%s %s can be read by other users; make it readable only by the agent (chmod 600)", fileVar, file)
	}
	token = strings.TrimSpace(string(data))
	if token == "" {
		return "", "", fmt.Errorf("%s: %s is empty", fileVar, file)
	}
	return token, warning, nil
}

// callbackPath is where the identity provider sends browsers back to,
// after GANTRY_PUBLIC_URL.
const callbackPath = "/api/v1/session/oidc/callback"

// loadOIDC returns the single sign-on setup from GANTRY_OIDC_*, or nil if
// none of it is set, and warnings. Issuer, client and secret come together,
// an allow list is required (otherwise every account at the provider could
// manage the host), and the provider sends browsers back to
// GANTRY_PUBLIC_URL, so that is required too. Both use https, except on
// this host (local tests).
func loadOIDC(publicOrigin string) (*oidc.Config, []string, error) {
	issuer, clientID := os.Getenv("GANTRY_OIDC_ISSUER"), os.Getenv("GANTRY_OIDC_CLIENT_ID")
	secret, warning, err := loadSecret("GANTRY_OIDC_CLIENT_SECRET")
	if err != nil {
		return nil, nil, err
	}
	var warnings []string
	if warning != "" {
		warnings = append(warnings, warning)
	}
	groups := splitList(os.Getenv("GANTRY_OIDC_ALLOWED_GROUPS"))
	emails := splitList(os.Getenv("GANTRY_OIDC_ALLOWED_EMAILS"))
	scopes := strings.Fields(strings.ReplaceAll(os.Getenv("GANTRY_OIDC_SCOPES"), ",", " "))

	const all = "GANTRY_OIDC_ISSUER, GANTRY_OIDC_CLIENT_ID and GANTRY_OIDC_CLIENT_SECRET (or _FILE)"
	switch {
	case issuer == "" && clientID == "" && secret == "":
		if len(groups) > 0 || len(emails) > 0 || len(scopes) > 0 {
			return nil, nil, fmt.Errorf("the GANTRY_OIDC_* settings need %s", all)
		}
		return nil, nil, nil
	case issuer == "" || clientID == "" || secret == "":
		return nil, nil, fmt.Errorf("single sign-on needs %s together", all)
	case len(groups) == 0 && len(emails) == 0:
		return nil, nil, errors.New("single sign-on needs an allow list, GANTRY_OIDC_ALLOWED_GROUPS or GANTRY_OIDC_ALLOWED_EMAILS: without one, every account at the identity provider could manage this host")
	case publicOrigin == "":
		return nil, nil, errors.New("single sign-on needs GANTRY_PUBLIC_URL, the web UI's address: the identity provider sends browsers back to it")
	}
	if err := checkSSOURL("GANTRY_OIDC_ISSUER", issuer); err != nil {
		return nil, nil, err
	}
	if err := checkSSOURL("GANTRY_PUBLIC_URL", publicOrigin); err != nil {
		return nil, nil, err
	}
	for i, e := range emails {
		if !strings.Contains(e, "@") {
			return nil, nil, fmt.Errorf("GANTRY_OIDC_ALLOWED_EMAILS: %q is no email address", e)
		}
		emails[i] = strings.ToLower(e)
	}
	if len(scopes) == 0 {
		scopes = []string{"openid", "profile", "email"}
	}
	return &oidc.Config{
		Issuer:        issuer,
		ClientID:      clientID,
		ClientSecret:  secret,
		RedirectURL:   publicOrigin + callbackPath,
		Scopes:        scopes,
		AllowedGroups: groups,
		AllowedEmails: emails,
	}, warnings, nil
}

// splitList splits a comma-separated list, trimming spaces; group names
// may contain spaces themselves.
func splitList(s string) []string {
	var list []string
	for item := range strings.SplitSeq(s, ",") {
		if item = strings.TrimSpace(item); item != "" {
			list = append(list, item)
		}
	}
	return list
}

// checkSSOURL wants an https URL, or http on this host (for tests with a
// local provider): codes, tokens and cookies travel over it.
func checkSSOURL(name, s string) error {
	u, err := url.Parse(s)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("%s %q: want a URL like https://login.example.com", name, s)
	}
	if u.Scheme == "https" || (u.Scheme == "http" && loopbackHost(u.Hostname())) {
		return nil
	}
	return fmt.Errorf("%s %q: single sign-on needs https (http only on this host, for tests)", name, s)
}

// config is the agent's network-facing configuration from the environment.
type config struct {
	listen            string
	publicOrigin      string
	trustedProxies    []netip.Prefix
	allowInsecureHTTP bool
	// tlsCert and tlsKey are PEM files; both empty means plain HTTP.
	tlsCert, tlsKey string
}

func loadConfig() (config, error) {
	var c config
	var err error
	// Only this host by default: reaching the agent from elsewhere is a
	// decision (the container image sets :7070, where published ports
	// decide).
	c.listen = envOr("GANTRY_LISTEN", "127.0.0.1:7070")
	if c.publicOrigin, err = api.ParsePublicURL(os.Getenv("GANTRY_PUBLIC_URL")); err != nil {
		return c, err
	}
	if c.trustedProxies, err = api.ParseTrustedProxies(os.Getenv("GANTRY_TRUSTED_PROXIES")); err != nil {
		return c, err
	}
	if v := os.Getenv("GANTRY_ALLOW_INSECURE_HTTP"); v != "" {
		if c.allowInsecureHTTP, err = strconv.ParseBool(v); err != nil {
			return c, fmt.Errorf("GANTRY_ALLOW_INSECURE_HTTP: want true or false, not %q", v)
		}
	}
	c.tlsCert, c.tlsKey = os.Getenv("GANTRY_TLS_CERT"), os.Getenv("GANTRY_TLS_KEY")
	if (c.tlsCert == "") != (c.tlsKey == "") {
		return c, errors.New("set both GANTRY_TLS_CERT and GANTRY_TLS_KEY, or neither")
	}
	return c, nil
}

// tlsConfig returns the TLS configuration for the certificate and key files,
// reloaded when they change, and a warning, if any.
func tlsConfig(certFile, keyFile string, logger *slog.Logger) (*tls.Config, string, error) {
	certs, err := tlscert.New(certFile, keyFile, logger)
	if err != nil {
		return nil, "", fmt.Errorf("GANTRY_TLS_CERT, GANTRY_TLS_KEY: %w", err)
	}
	var warning string
	if info, err := os.Stat(keyFile); err == nil && info.Mode().Perm()&0o077 != 0 {
		warning = fmt.Sprintf("GANTRY_TLS_KEY %s can be read by other users; make it readable only by the agent (chmod 600)", keyFile)
	}
	return &tls.Config{MinVersion: tls.VersionTLS12, GetCertificate: certs.GetCertificate}, warning, nil
}

// loopbackOnly reports whether listen only accepts connections from this
// host.
func loopbackOnly(listen string) bool {
	host, _, err := net.SplitHostPort(listen)
	return err == nil && loopbackHost(host)
}

// loopbackHost reports whether host names this host only.
func loopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	a, err := netip.ParseAddr(host)
	return err == nil && a.IsLoopback()
}

func main() {
	// Configuration comes from GANTRY_* variables; the only argument is
	// --version, e.g. to check an installed package.
	if len(os.Args) > 1 {
		if os.Args[1] == "--version" || os.Args[1] == "version" {
			fmt.Println(version)
			return
		}
		fmt.Fprintf(os.Stderr, "gantry-agent: unknown argument %q; it is configured with GANTRY_* variables (see the README), and --version prints its version\n", os.Args[1])
		os.Exit(2)
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	cfg, err := loadConfig()
	if err != nil {
		logger.Error(err.Error())
		os.Exit(1)
	}
	ssoConfig, warnings, err := loadOIDC(cfg.publicOrigin)
	if err != nil {
		logger.Error(err.Error())
		os.Exit(1)
	}
	apiToken, uiToken, tokenWarnings, err := loadTokens(ssoConfig != nil)
	if err != nil {
		logger.Error(err.Error())
		os.Exit(1)
	}
	for _, w := range append(warnings, tokenWarnings...) {
		logger.Warn(w)
	}
	if apiToken == "" {
		logger.Info("API tokens are off: GANTRY_TOKEN is not set")
	}
	if uiToken == "" {
		logger.Info("web login with a token is off: GANTRY_UI_TOKEN is not set")
	}
	var sso *oidc.Client
	if ssoConfig != nil {
		sso = oidc.New(*ssoConfig)
		logger.Info("single sign-on is on", "issuer", ssoConfig.Issuer, "redirect_uri", ssoConfig.RedirectURL)
	}
	if cfg.allowInsecureHTTP {
		logger.Warn("GANTRY_ALLOW_INSECURE_HTTP is set: tokens and session cookies from internet addresses are accepted over plain HTTP, where anyone on the way can read them")
	}
	var tlsConf *tls.Config
	if cfg.tlsCert != "" {
		var warning string
		if tlsConf, warning, err = tlsConfig(cfg.tlsCert, cfg.tlsKey, logger); err != nil {
			logger.Error(err.Error())
			os.Exit(1)
		}
		if warning != "" {
			logger.Warn(warning)
		}
	} else if !loopbackOnly(cfg.listen) && !strings.HasPrefix(cfg.publicOrigin, "https://") {
		logger.Info("reachable from other hosts over plain HTTP; logins and tokens from internet addresses are refused, use HTTPS in front or GANTRY_TLS_CERT (see README)",
			"addr", cfg.listen)
	}

	if _, err := exec.LookPath("docker"); err != nil {
		logger.Warn("the docker CLI (with the compose plugin) is not in PATH; stacks are listed, but actions and Compose checks need it")
	}

	dockerClient, err := docker.New(os.Getenv("DOCKER_HOST"))
	if err != nil {
		logger.Error("docker client", "err", err)
		os.Exit(1)
	}
	manager, err := stacks.New(envOr("GANTRY_STACKS_DIR", stacks.DefaultDir), dockerClient, compose.CLI{})
	if err != nil {
		logger.Error("stacks", "err", err)
		os.Exit(1)
	}
	if !manager.DirExists() {
		logger.Warn("stacks directory does not exist; no managed stacks until it is created (set GANTRY_STACKS_DIR to use another one)",
			"stacks_dir", manager.Dir())
	}

	// Canceled on SIGINT/SIGTERM. Running operations end with it: Compose
	// gets an interrupt, and open event streams finish.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	// Tells web UIs which stacks changed: from Docker's events and from
	// operations and file changes made through the API.
	hub := live.New(dockerClient, live.Options{Logger: logger})
	ops := operations.NewManager(ctx, compose.CLI{}, operations.Options{OnChange: hub.Notify})

	addr := cfg.listen
	srv := api.NewServer(addr, api.New(api.Config{
		Token:             apiToken,
		UIToken:           uiToken,
		OIDC:              sso,
		Version:           version,
		Stacks:            manager,
		Operations:        ops,
		Logs:              logs.New(dockerClient),
		Files:             stackfiles.New(compose.CLI{}),
		Host:              hostInfo{docker: dockerClient},
		Volumes:           volumes.New(dockerClient),
		Live:              hub,
		Context:           ctx,
		Logger:            logger,
		PublicOrigin:      cfg.publicOrigin,
		TrustedProxies:    cfg.trustedProxies,
		AllowInsecureHTTP: cfg.allowInsecureHTTP,
	}))

	srv.TLSConfig = tlsConf

	errc := make(chan error, 1)
	go func() {
		logger.Info("gantry agent listening", "addr", addr, "tls", tlsConf != nil, "version", version, "stacks_dir", manager.Dir())
		ln, err := api.Listen(addr, cfg.trustedProxies)
		if err != nil {
			errc <- err
			return
		}
		if tlsConf != nil {
			// The certificate comes from TLSConfig.GetCertificate. ServeTLS
			// also sets up HTTP/2.
			errc <- srv.ServeTLS(ln, "", "")
			return
		}
		errc <- srv.Serve(ln)
	}()

	select {
	case err := <-errc:
		if !errors.Is(err, http.ErrServerClosed) {
			logger.Error("server failed", "err", err)
			os.Exit(1)
		}
	case <-ctx.Done():
		logger.Info("shutting down; running operations are canceled")
		// Long enough for Compose to react to the interrupt (see
		// compose.CLI.CancelGrace), so event streams can send their end.
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			logger.Error("shutdown failed", "err", err)
		}
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// hostInfo tells the API about the Docker host, from Docker and Compose.
type hostInfo struct {
	docker  *docker.Client
	compose compose.CLI
}

func (h hostInfo) Info(ctx context.Context) (docker.Info, error) { return h.docker.Info(ctx) }

func (h hostInfo) ComposeVersion(ctx context.Context) (string, error) {
	return h.compose.Version(ctx)
}
