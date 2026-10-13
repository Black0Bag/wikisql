package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func testManager(t *testing.T) *Manager {
	t.Helper()
	return NewManager("test-secret-for-unit-tests", "test-password", 1)
}

func TestIssueAndVerify(t *testing.T) {
	m := testManager(t)
	token, exp, err := m.Issue(nil)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if token == "" || exp.IsZero() {
		t.Fatal("expected non-empty token and expiry")
	}
	if err := m.Verify(token); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if err := m.Verify(token + "tampered"); err == nil {
		t.Fatal("expected tampered token to fail")
	}
}

func TestWrongPassword(t *testing.T) {
	m := testManager(t)
	mw := m.LoginHandler()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login",
		strings.NewReader(`{"password":"wrong"}`))
	mw.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestLoginFlow(t *testing.T) {
	m := testManager(t)
	mw := m.LoginHandler()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login",
		strings.NewReader(`{"password":"test-password"}`))
	mw.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp loginResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Token == "" || resp.TokenType != "Bearer" {
		t.Fatalf("unexpected login response: %+v", resp)
	}
	if err := m.Verify(resp.Token); err != nil {
		t.Fatalf("issued token failed verification: %v", err)
	}
}

func TestMiddlewareBlocksAndAllows(t *testing.T) {
	mgr := testManager(t)
	token, _, err := mgr.Issue(nil)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	protected := mgr.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// 无 token → 401
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/tables", nil)
	protected.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 without token, got %d", rec.Code)
	}

	// 有效 token → 200
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/v1/tables", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	protected.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 with valid token, got %d", rec.Code)
	}

	// 篡改 token → 401
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/v1/tables", nil)
	req.Header.Set("Authorization", "Bearer "+token[:10]+"x")
	protected.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 with tampered token, got %d", rec.Code)
	}
}

func TestGenerateSecret(t *testing.T) {
	s1, err := GenerateSecret()
	if err != nil {
		t.Fatalf("GenerateSecret: %v", err)
	}
	s2, _ := GenerateSecret()
	if s1 == s2 || len(s1) < 40 {
		t.Fatal("expected unique long secrets")
	}
}
