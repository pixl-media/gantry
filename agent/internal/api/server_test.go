package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHealthzIsPublic(t *testing.T) {
	h := New(Config{Token: "secret", Version: "test"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /healthz = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestAPIRequiresToken(t *testing.T) {
	tests := []struct {
		name   string
		token  string
		header string
		want   int
	}{
		{"no header", "secret", "", http.StatusUnauthorized},
		{"wrong token", "secret", "Bearer nope", http.StatusUnauthorized},
		{"missing Bearer prefix", "secret", "secret", http.StatusUnauthorized},
		{"valid token", "secret", "Bearer secret", http.StatusOK},
		{"empty configured token denies all", "", "Bearer ", http.StatusUnauthorized},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := New(Config{Token: tt.token, Version: "test"})
			req := httptest.NewRequest(http.MethodGet, "/api/v1/version", nil)
			if tt.header != "" {
				req.Header.Set("Authorization", tt.header)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != tt.want {
				t.Fatalf("status = %d, want %d", rec.Code, tt.want)
			}
		})
	}
}
