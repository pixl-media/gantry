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
		{"missing", "", false},
		{"short", "change-me", false},
		{"23 random characters", "5b2f0c9e81a74d36ef0a1b2", false},
		{"random hex", "9c1f2a7e4b80d36512ef0a8c7d94b6e3a15f2c08d7b9e461", true},
		{"one character", strings.Repeat("a", 30), false},
		{"few characters", "abababababcbcbcbcbcbabab", false},
		{"repeated word", "Passw0rd!Passw0rd!Passw0rd!", false},
		{"repeated piece, cut off", "Summer26!Summer26!Summer2", false},
	}
	for _, tt := range tests {
		err := checkToken(tt.token)
		if (err == nil) != tt.ok {
			t.Errorf("%s: err = %v", tt.name, err)
		}
		if err != nil && tt.token != "" && strings.Contains(err.Error(), tt.token) {
			t.Errorf("%s: the error contains the token", tt.name)
		}
	}
}

func TestLoadTokenFromFile(t *testing.T) {
	file := filepath.Join(t.TempDir(), "token")
	os.WriteFile(file, []byte("9c1f2a7e4b80d36512ef0a8c7d94b6e3\n"), 0o600)
	t.Setenv("GANTRY_TOKEN", "")
	t.Setenv("GANTRY_TOKEN_FILE", file)
	token, warning, err := loadToken()
	if err != nil || token != "9c1f2a7e4b80d36512ef0a8c7d94b6e3" || warning != "" {
		t.Errorf("loadToken = %q, %q, %v", token, warning, err)
	}
	os.Chmod(file, 0o644)
	if _, warning, _ := loadToken(); warning == "" {
		t.Error("no warning for a token file others can read")
	}
	t.Setenv("GANTRY_TOKEN", "9c1f2a7e4b80d36512ef0a8c7d94b6e3")
	if _, _, err := loadToken(); err == nil {
		t.Error("both GANTRY_TOKEN and GANTRY_TOKEN_FILE were accepted")
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
