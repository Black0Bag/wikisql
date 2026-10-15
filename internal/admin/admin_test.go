package admin

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Black0Bag/wikisql/internal/config"
	"github.com/Black0Bag/wikisql/internal/store"
	"github.com/go-chi/chi/v5"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func testManager(t *testing.T) (*Manager, *config.Config) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	st, err := store.New(dbPath, testLogger())
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	cfg := config.Default()
	hash, err := HashPassword("test-admin-password")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	cfg.Admin.PasswordHash = hash
	cfgPath := filepath.Join(dir, "config.yaml")

	m := NewManager(cfg, cfgPath, st, "test", testLogger())
	return m, cfg
}

func TestHashAndVerify(t *testing.T) {
	hash, err := HashPassword("hunter2-hunter2")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if !strings.HasPrefix(hash, "pbkdf2-sha256$") {
		t.Errorf("unexpected hash format: %s", hash)
	}
	if !VerifyPassword("hunter2-hunter2", hash) {
		t.Error("correct password should verify")
	}
	if VerifyPassword("wrong-password", hash) {
		t.Error("wrong password must not verify")
	}
	if VerifyPassword("", hash) {
		t.Error("empty password must not verify")
	}
}

func TestLoginAndSession(t *testing.T) {
	m, _ := testManager(t)

	// 错误密码
	if _, err := m.Login("wrong"); err == nil {
		t.Fatal("expected login failure")
	}
	// 正确密码
	token, err := m.Login("test-admin-password")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if !m.Authenticated(token) {
		t.Error("token should be authenticated")
	}
	if m.Authenticated("bogus-token") {
		t.Error("bogus token must not pass")
	}
	// 登出
	m.Logout(token)
	if m.Authenticated(token) {
		t.Error("token should be invalid after logout")
	}
}

func TestApplyConfigMasksSecrets(t *testing.T) {
	m, _ := testManager(t)
	ctx := context.Background()

	// 先通过 ApplyConfig 设置一个密钥
	if err := m.ApplyConfig(map[string]any{
		"auth.api_key": "secret-key-value",
		"server.port":  float64(9999),
	}); err != nil {
		t.Fatalf("ApplyConfig: %v", err)
	}

	// 配置视图不应包含明文密钥
	view := m.configView()
	for _, sec := range view {
		for _, f := range sec.Fields {
			if f.Type == "secret" && f.Value != nil {
				t.Errorf("secret field %s leaked a value: %v", f.Key, f.Value)
			}
			if f.Key == "auth.api_key" && !f.HasValue {
				t.Error("auth.api_key should be marked has_value=true")
			}
			if f.Key == "server.port" && f.Value != 9999 {
				t.Errorf("server.port should be 9999, got %v", f.Value)
			}
		}
	}

	// 空密钥字段 = 保持原值
	if err := m.ApplyConfig(map[string]any{"auth.api_key": ""}); err != nil {
		t.Fatalf("ApplyConfig keep: %v", err)
	}
	m.mu.Lock()
	apiKey := m.cfg.Auth.APIKey
	m.mu.Unlock()
	if apiKey != "secret-key-value" {
		t.Errorf("empty secret patch must keep old value, got %q", apiKey)
	}

	// 配置文件应该已写入
	if _, err := os.Stat(m.cfgPath); err != nil {
		t.Errorf("config file not written: %v", err)
	}
	_ = ctx
}

func TestChangePassword(t *testing.T) {
	m, _ := testManager(t)

	// 旧密码错误
	if err := m.ChangePassword("wrong", "newpassword123"); err == nil {
		t.Fatal("expected failure with wrong current password")
	}
	// 新密码太短
	if err := m.ChangePassword("test-admin-password", "short"); err == nil {
		t.Fatal("expected failure with short new password")
	}
	// 正常修改
	if err := m.ChangePassword("test-admin-password", "newpassword123"); err != nil {
		t.Fatalf("ChangePassword: %v", err)
	}
	// 旧密码不能再登录
	if _, err := m.Login("test-admin-password"); err == nil {
		t.Error("old password should no longer work")
	}
	// 新密码可以登录
	if _, err := m.Login("newpassword123"); err != nil {
		t.Errorf("new password should work: %v", err)
	}
}

// ---- 二次后缀路由测试 ----

func TestSecretPathRouting(t *testing.T) {
	m, _ := testManager(t)
	secret := "test-secret-path-xyz"
	m.secretPath = secret

	r := chi.NewRouter()
	m.Routes(r, secret, nil)
	srv := httptest.NewServer(r)
	defer srv.Close()

	cases := []struct {
		name     string
		method   string
		path     string
		wantCode int
		wantBody string // 期望包含的特征（空=不检查）
	}{
		{"伪装路径 /admin", "GET", "/admin", 404, "404 Not Found"},
		{"伪装路径 /admin/（尾斜杠）", "GET", "/admin/", 404, "404 Not Found"},
		{"伪装路径 /admin/wrong-suffix", "GET", "/admin/wrong-suffix", 404, "404 Not Found"},
		{"伪装路径 /admin/api/login", "POST", "/admin/api/login", 404, "404 Not Found"},
		{"正确后缀页面", "GET", "/admin/" + secret, 200, "wikisql 管理面板"},
		{"正确后缀尾斜杠", "GET", "/admin/" + secret + "/", 200, "wikisql 管理面板"},
		{"正确前缀 API（无密码应 401 说明到达处理）", "POST", "/admin/" + secret + "/api/login", 401, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := ""
			code, b := doRequest(t, srv.URL+tc.path, tc.method, `{"password":"x"}`)
			body = b
			if code != tc.wantCode {
				t.Errorf("%s %s = %d, want %d (body: %s)", tc.method, tc.path, code, tc.wantCode, body[:min(len(body), 100)])
			}
			if tc.wantBody != "" && !strings.Contains(body, tc.wantBody) {
				t.Errorf("%s %s body missing %q, got: %s", tc.method, tc.path, tc.wantBody, body[:min(len(body), 150)])
			}
		})
	}
}

func doRequest(t *testing.T, url, method, body string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(method, url, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
