package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pixl-media/gantry/agent/internal/tlscert/tlscerttest"
)

func TestCheckToken(t *testing.T) {
	tests := []struct {
		name, token string
		ok          bool
	}{
		{"short", "change-me", false},
		{"23 random characters", "5b2f0c9e81a74d36ef0a1b2", false},
		{"random hex", "9c1f2a7e4b80d36512ef0a8c7d94b6e3a15f2c08d7b9e461", true},
		{"one character", strings.Repeat("a", 30), false},
		{"few characters", "abababababcbcbcbcbcbabab", false},
		{"repeated word", "Passw0rd!Passw0rd!Passw0rd!", false},
		{"repeated piece, cut off", "Summer26!Summer26!Summer2", false},
	}
	for _, tt := range tests {
		err := checkToken("GANTRY_UI_TOKEN", tt.token)
		if (err == nil) != tt.ok {
			t.Errorf("%s: err = %v", tt.name, err)
		}
		if err != nil && strings.Contains(err.Error(), tt.token) {
			t.Errorf("%s: the error contains the token", tt.name)
		}
		if err != nil && !strings.Contains(err.Error(), "GANTRY_UI_TOKEN") {
			t.Errorf("%s: the error doesn't name the variable: %v", tt.name, err)
		}
	}
}

func TestLoadTokenFromFile(t *testing.T) {
	file := filepath.Join(t.TempDir(), "token")
	os.WriteFile(file, []byte("9c1f2a7e4b80d36512ef0a8c7d94b6e3\n"), 0o600)
	t.Setenv("GANTRY_UI_TOKEN", "")
	t.Setenv("GANTRY_UI_TOKEN_FILE", file)
	token, warning, err := loadSecret("GANTRY_UI_TOKEN")
	if err != nil || token != "9c1f2a7e4b80d36512ef0a8c7d94b6e3" || warning != "" {
		t.Errorf("loadSecret = %q, %q, %v", token, warning, err)
	}
	os.Chmod(file, 0o644)
	if _, warning, _ := loadSecret("GANTRY_UI_TOKEN"); !strings.Contains(warning, "GANTRY_UI_TOKEN_FILE") {
		t.Errorf("warning = %q, want one about GANTRY_UI_TOKEN_FILE", warning)
	}
	os.WriteFile(file, []byte("\n"), 0o600)
	if _, _, err := loadSecret("GANTRY_UI_TOKEN"); err == nil {
		t.Error("an empty token file was accepted")
	}
	t.Setenv("GANTRY_UI_TOKEN", "9c1f2a7e4b80d36512ef0a8c7d94b6e3")
	if _, _, err := loadSecret("GANTRY_UI_TOKEN"); err == nil || !strings.Contains(err.Error(), "not both") {
		t.Errorf("both GANTRY_UI_TOKEN and GANTRY_UI_TOKEN_FILE: err = %v", err)
	}
}

func TestLoadTokens(t *testing.T) {
	const a, b = "9c1f2a7e4b80d36512ef0a8c7d94b6e3", "4e7b0c2d9a1f58e6b3d07c4a2e9f1b85"
	tests := []struct {
		name, api, ui string
		err           string // part of the error, "" for none
	}{
		{"both", a, b, ""},
		{"API only", a, "", ""},
		{"web login only", "", b, ""},
		{"none", "", "", "GANTRY_TOKEN (API), GANTRY_UI_TOKEN (web login), GANTRY_OIDC_ISSUER"},
		{"the same", a, a, "are the same"},
		{"weak API token", "change-me", b, "GANTRY_TOKEN is too short"},
		{"weak web login token", a, "change-me", "GANTRY_UI_TOKEN is too short"},
	}
	for _, tt := range tests {
		t.Setenv("GANTRY_TOKEN", tt.api)
		t.Setenv("GANTRY_TOKEN_FILE", "")
		t.Setenv("GANTRY_UI_TOKEN", tt.ui)
		t.Setenv("GANTRY_UI_TOKEN_FILE", "")
		api, ui, _, err := loadTokens(false)
		if tt.err == "" {
			if err != nil || api != tt.api || ui != tt.ui {
				t.Errorf("%s: loadTokens = %q, %q, %v", tt.name, api, ui, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), tt.err) {
			t.Errorf("%s: err = %v, want %q", tt.name, err, tt.err)
		}
		if err != nil && strings.Contains(err.Error(), a) {
			t.Errorf("%s: the error contains a token", tt.name)
		}
	}
}

// With single sign-on, neither token is needed.
func TestLoadTokensWithSSO(t *testing.T) {
	for _, name := range []string{"GANTRY_TOKEN", "GANTRY_TOKEN_FILE", "GANTRY_UI_TOKEN", "GANTRY_UI_TOKEN_FILE"} {
		t.Setenv(name, "")
	}
	if api, ui, _, err := loadTokens(true); err != nil || api != "" || ui != "" {
		t.Errorf("loadTokens = %q, %q, %v", api, ui, err)
	}
}

func TestLoadOIDC(t *testing.T) {
	const public = "https://gantry.example.com"
	full := map[string]string{
		"GANTRY_OIDC_ISSUER":         "https://auth.example.com/application/o/gantry/",
		"GANTRY_OIDC_CLIENT_ID":      "gantry",
		"GANTRY_OIDC_CLIENT_SECRET":  "the-client-secret",
		"GANTRY_OIDC_ALLOWED_GROUPS": "gantry admins, ops",
		"GANTRY_OIDC_ALLOWED_EMAILS": "Alice@Example.com",
	}
	with := func(changes map[string]string) map[string]string {
		env := map[string]string{}
		for k, v := range full {
			env[k] = v
		}
		for k, v := range changes {
			env[k] = v
		}
		return env
	}
	tests := []struct {
		name   string
		env    map[string]string
		public string
		err    string // part of the error, "" for none
		off    bool
	}{
		{name: "off", env: map[string]string{}, public: public, off: true},
		{name: "full", env: full, public: public},
		{name: "groups only", env: with(map[string]string{"GANTRY_OIDC_ALLOWED_EMAILS": ""}), public: public},
		{name: "loopback over http", env: with(map[string]string{"GANTRY_OIDC_ISSUER": "http://127.0.0.1:5556/dex"}), public: "http://localhost:5175"},
		{name: "allow list alone", env: map[string]string{"GANTRY_OIDC_ALLOWED_GROUPS": "admins"}, public: public, err: "need GANTRY_OIDC_ISSUER"},
		{name: "no secret", env: with(map[string]string{"GANTRY_OIDC_CLIENT_SECRET": ""}), public: public, err: "together"},
		{name: "no allow list", env: with(map[string]string{"GANTRY_OIDC_ALLOWED_GROUPS": "", "GANTRY_OIDC_ALLOWED_EMAILS": " , "}), public: public, err: "allow list"},
		{name: "no public URL", env: full, public: "", err: "GANTRY_PUBLIC_URL"},
		{name: "public URL over http", env: full, public: "http://nas.local:8080", err: "needs https"},
		{name: "issuer over http", env: with(map[string]string{"GANTRY_OIDC_ISSUER": "http://auth.example.com"}), public: public, err: "needs https"},
		{name: "issuer not a URL", env: with(map[string]string{"GANTRY_OIDC_ISSUER": "auth.example.com"}), public: public, err: "want a URL"},
		{name: "not an email", env: with(map[string]string{"GANTRY_OIDC_ALLOWED_EMAILS": "alice"}), public: public, err: "no email address"},
	}
	for _, tt := range tests {
		for _, name := range []string{"GANTRY_OIDC_ISSUER", "GANTRY_OIDC_CLIENT_ID", "GANTRY_OIDC_CLIENT_SECRET",
			"GANTRY_OIDC_CLIENT_SECRET_FILE", "GANTRY_OIDC_ALLOWED_GROUPS", "GANTRY_OIDC_ALLOWED_EMAILS", "GANTRY_OIDC_SCOPES"} {
			t.Setenv(name, tt.env[name])
		}
		cfg, _, err := loadOIDC(tt.public)
		switch {
		case tt.err != "":
			if err == nil || !strings.Contains(err.Error(), tt.err) {
				t.Errorf("%s: err = %v, want %q", tt.name, err, tt.err)
			}
		case err != nil:
			t.Errorf("%s: %v", tt.name, err)
		case tt.off != (cfg == nil):
			t.Errorf("%s: config = %+v", tt.name, cfg)
		}
		if err != nil && strings.Contains(err.Error(), "the-client-secret") {
			t.Errorf("%s: the error contains the secret", tt.name)
		}
	}

	for k, v := range full {
		t.Setenv(k, v)
	}
	t.Setenv("GANTRY_OIDC_SCOPES", "")
	cfg, _, err := loadOIDC(public)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.RedirectURL != public+"/api/v1/session/oidc/callback" ||
		strings.Join(cfg.AllowedGroups, "|") != "gantry admins|ops" ||
		strings.Join(cfg.AllowedEmails, "|") != "alice@example.com" ||
		strings.Join(cfg.Scopes, " ") != "openid profile email" {
		t.Errorf("config = %+v", cfg)
	}
}

func TestLoopbackOnly(t *testing.T) {
	for addr, want := range map[string]bool{
		"127.0.0.1:7070": true, "[::1]:7070": true, "localhost:7070": true,
		":7070": false, "0.0.0.0:7070": false, "192.168.1.5:7070": false, "[::]:7070": false,
	} {
		if got := loopbackOnly(addr); got != want {
			t.Errorf("loopbackOnly(%q) = %v, want %v", addr, got, want)
		}
	}
}

func TestTLSConfig(t *testing.T) {
	certFile, keyFile, _ := tlscerttest.Write(t, t.TempDir(), 1, time.Now().AddDate(1, 0, 0))
	for _, tt := range []struct{ cert, key, err string }{
		{"", "", ""},
		{certFile, keyFile, ""},
		{certFile, "", "set both GANTRY_TLS_CERT and GANTRY_TLS_KEY, or neither"},
		{"", keyFile, "set both GANTRY_TLS_CERT and GANTRY_TLS_KEY, or neither"},
	} {
		t.Setenv("GANTRY_TLS_CERT", tt.cert)
		t.Setenv("GANTRY_TLS_KEY", tt.key)
		c, err := loadConfig()
		if tt.err == "" && (err != nil || c.tlsCert != tt.cert || c.tlsKey != tt.key) {
			t.Errorf("%q, %q: %+v, %v", tt.cert, tt.key, c, err)
		}
		if tt.err != "" && (err == nil || err.Error() != tt.err) {
			t.Errorf("%q, %q: err = %v, want %q", tt.cert, tt.key, err, tt.err)
		}
	}

	conf, warning, err := tlsConfig(certFile, keyFile, nil)
	if err != nil || conf.GetCertificate == nil || warning != "" {
		t.Errorf("tlsConfig = %v, %q, %v", conf, warning, err)
	}
	os.Chmod(keyFile, 0o644)
	if _, warning, _ := tlsConfig(certFile, keyFile, nil); !strings.Contains(warning, "GANTRY_TLS_KEY") {
		t.Errorf("warning = %q, want one about GANTRY_TLS_KEY", warning)
	}
	if _, _, err := tlsConfig(keyFile, keyFile, nil); err == nil || !strings.Contains(err.Error(), "GANTRY_TLS_CERT") {
		t.Errorf("broken pair: err = %v", err)
	}
}
