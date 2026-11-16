package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	token, warning, err := loadToken("GANTRY_UI_TOKEN")
	if err != nil || token != "9c1f2a7e4b80d36512ef0a8c7d94b6e3" || warning != "" {
		t.Errorf("loadToken = %q, %q, %v", token, warning, err)
	}
	os.Chmod(file, 0o644)
	if _, warning, _ := loadToken("GANTRY_UI_TOKEN"); !strings.Contains(warning, "GANTRY_UI_TOKEN_FILE") {
		t.Errorf("warning = %q, want one about GANTRY_UI_TOKEN_FILE", warning)
	}
	os.WriteFile(file, []byte("\n"), 0o600)
	if _, _, err := loadToken("GANTRY_UI_TOKEN"); err == nil {
		t.Error("an empty token file was accepted")
	}
	t.Setenv("GANTRY_UI_TOKEN", "9c1f2a7e4b80d36512ef0a8c7d94b6e3")
	if _, _, err := loadToken("GANTRY_UI_TOKEN"); err == nil || !strings.Contains(err.Error(), "not both") {
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
		{"none", "", "", "GANTRY_TOKEN (API), GANTRY_UI_TOKEN (web login), or both"},
		{"the same", a, a, "are the same"},
		{"weak API token", "change-me", b, "GANTRY_TOKEN is too short"},
		{"weak web login token", a, "change-me", "GANTRY_UI_TOKEN is too short"},
	}
	for _, tt := range tests {
		t.Setenv("GANTRY_TOKEN", tt.api)
		t.Setenv("GANTRY_TOKEN_FILE", "")
		t.Setenv("GANTRY_UI_TOKEN", tt.ui)
		t.Setenv("GANTRY_UI_TOKEN_FILE", "")
		api, ui, _, err := loadTokens()
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
