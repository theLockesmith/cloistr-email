package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"go.uber.org/zap"
)

func TestInternalDomainAuthMiddleware(t *testing.T) {
	h := NewInternalDomainHandler(nil, nil, nil, "s3cr3t", zap.NewNop())
	guarded := h.AuthMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	tests := []struct {
		name       string
		authHeader string
		wantStatus int
	}{
		{"correct secret", "Bearer s3cr3t", http.StatusOK},
		{"wrong secret", "Bearer nope", http.StatusUnauthorized},
		{"missing header", "", http.StatusUnauthorized},
		{"no bearer prefix", "s3cr3t", http.StatusUnauthorized},
		{"wrong scheme", "Basic s3cr3t", http.StatusUnauthorized},
		{"case-insensitive scheme ok", "bearer s3cr3t", http.StatusOK},
		{"secret as prefix is rejected", "Bearer s3cr3tX", http.StatusUnauthorized},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/internal/v1/domains", nil)
			if tt.authHeader != "" {
				req.Header.Set("Authorization", tt.authHeader)
			}
			rec := httptest.NewRecorder()
			guarded.ServeHTTP(rec, req)
			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
		})
	}
}

// With no secret configured the API must fail closed, not open.
func TestInternalDomainAuthMiddlewareDisabled(t *testing.T) {
	h := NewInternalDomainHandler(nil, nil, nil, "", zap.NewNop())
	guarded := h.AuthMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler must not run when secret is unset")
	}))

	req := httptest.NewRequest(http.MethodGet, "/internal/v1/domains", nil)
	req.Header.Set("Authorization", "Bearer anything")
	rec := httptest.NewRecorder()
	guarded.ServeHTTP(rec, req)
	if rec.Code == http.StatusOK {
		t.Errorf("disabled API must not return 200")
	}
}
