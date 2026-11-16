package main

import (
	"strings"
	"testing"
)

func TestCheckToken(t *testing.T) {
	tests := []struct {
		token         string
		wantErr, warn bool
	}{
		{token: "", wantErr: true},
		{token: "change-me", wantErr: true},
		{token: strings.Repeat("a", minTokenLength-1), wantErr: true},
		{token: strings.Repeat("a", minTokenLength), warn: true},
		{token: strings.Repeat("a", goodTokenLength)},
	}
	for _, tt := range tests {
		warning, err := checkToken(tt.token)
		if (err != nil) != tt.wantErr || (warning != "") != tt.warn {
			t.Errorf("checkToken(%d characters) = %q, %v", len(tt.token), warning, err)
		}
		if tt.token != "" && err != nil && strings.Contains(err.Error(), tt.token) {
			t.Errorf("the error contains the token: %v", err)
		}
	}
}
