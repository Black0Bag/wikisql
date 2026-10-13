package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAPIKeyMode(t *testing.T) {
	m := NewAPIKeyManager("test-static-key")
	if !m.IsAPIKeyMode() {
		t.Fatal("expected API key mode")
	}

	handler := m.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	cases := []struct {
		name  string
		token string
		want  int
	}{
		{"correct key", "test-static-key", http.StatusOK},
		{"wrong key", "wrong", http.StatusUnauthorized},
		{"empty", "", http.StatusUnauthorized},
	}
	for _, tc := range cases {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/tables", nil)
		if tc.token != "" {
			req.Header.Set("Authorization", "Bearer "+tc.token)
		}
		handler.ServeHTTP(rec, req)
		if rec.Code != tc.want {
			t.Errorf("%s: got %d, want %d", tc.name, rec.Code, tc.want)
		}
	}
}
