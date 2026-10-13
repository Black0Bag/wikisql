package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

var (
	ErrInvalidCredentials = errors.New("invalid password")
	ErrUnauthorized       = errors.New("unauthorized")
)

type Manager struct {
	secret   []byte
	password string
	apiKey   string
	ttl      time.Duration
}

func NewManager(secret, password string, ttlHours int) *Manager {
	if ttlHours <= 0 {
		ttlHours = 72
	}
	return &Manager{
		secret:   []byte(secret),
		password: password,
		ttl:      time.Duration(ttlHours) * time.Hour,
	}
}

// NewAPIKeyManager 创建静态 API Key 模式的管理器：配置里写一次 key，
// 所有请求直接用这个 key 当 Bearer 值，不过期不刷新，自用最简。
func NewAPIKeyManager(apiKey string) *Manager {
	return &Manager{
		apiKey: apiKey,
		secret: []byte(apiKey),
		ttl:    0,
	}
}

func (m *Manager) IsAPIKeyMode() bool { return m.apiKey != "" }

// GenerateSecret 生成 256 位随机密钥（jwt_secret 未配置时的进程级默认值，
// 重启后失效，所有已发 token 作废）。
func GenerateSecret() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func (m *Manager) Issue(ctx context.Context) (token string, expiresAt time.Time, err error) {
	now := time.Now()
	exp := now.Add(m.ttl)
	claims := jwt.RegisteredClaims{
		Subject:   "owner",
		Issuer:    "kb-server",
		IssuedAt:  jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(exp),
	}
	t := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := t.SignedString(m.secret)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("signing token: %w", err)
	}
	return signed, exp, nil
}

func (m *Manager) Verify(tokenString string) error {
	_, err := jwt.Parse(tokenString, func(t *jwt.Token) (any, error) {
		return m.secret, nil
	}, jwt.WithValidMethods([]string{"HS256"}), jwt.WithIssuer("kb-server"))
	if err != nil {
		return ErrUnauthorized
	}
	return nil
}

func (m *Manager) checkPassword(password string) bool {
	return subtle.ConstantTimeCompare([]byte(password), []byte(m.password)) == 1
}

func (m *Manager) checkAPIKey(key string) bool {
	return m.apiKey != "" && subtle.ConstantTimeCompare([]byte(key), []byte(m.apiKey)) == 1
}

type loginRequest struct {
	Password string `json:"password"`
}

type loginResponse struct {
	Token     string `json:"token"`
	ExpiresAt string `json:"expires_at"`
	TokenType string `json:"token_type"`
}

// LoginHandler 返回登录端点。登录失败统一返回 401 且不区分原因。
func (m *Manager) LoginHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req loginRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		if !m.checkPassword(req.Password) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]any{"error": ErrInvalidCredentials.Error()})
			return
		}
		token, exp, err := m.Issue(r.Context())
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]any{"error": "failed to issue token"})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(loginResponse{
			Token:     token,
			ExpiresAt: exp.UTC().Format(time.RFC3339),
			TokenType: "Bearer",
		})
	})
}

// Middleware 校验 Authorization: Bearer 头。API Key 模式直接比对静态值；JWT 模式验签。
func (m *Manager) Middleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := bearerToken(r)

			var authorized bool
			if m.apiKey != "" {
				authorized = m.checkAPIKey(token)
			} else {
				authorized = token != "" && m.Verify(token) == nil
			}

			if !authorized {
				w.Header().Set("WWW-Authenticate", `Bearer realm="kb-server"`)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				json.NewEncoder(w).Encode(map[string]any{"error": ErrUnauthorized.Error()})
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if h == "" {
		return ""
	}
	parts := strings.SplitN(h, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return ""
	}
	return strings.TrimSpace(parts[1])
}
