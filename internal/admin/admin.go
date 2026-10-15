package admin

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/Black0Bag/wikisql/internal/config"
	"github.com/Black0Bag/wikisql/internal/store"
)

// PBKDF2-SHA256 参数（OWASP 建议量级；路由器上登录约百毫秒级）。
const (
	pbkdf2Iterations = 310_000
	pbkdf2KeyLen     = 32
	pbkdf2SaltLen    = 16
)

var (
	ErrBadPassword = errors.New("密码错误")
)

// HashPassword 生成 pbkdf2-sha256 哈希，格式：pbkdf2-sha256$迭代数$盐$哈希（均 base64）。
func HashPassword(password string) (string, error) {
	salt := make([]byte, pbkdf2SaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key, err := pbkdf2.Key(sha256.New, password, salt, pbkdf2Iterations, pbkdf2KeyLen)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("pbkdf2-sha256$%d$%s$%s",
		pbkdf2Iterations,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	), nil
}

// VerifyPassword 恒定时间校验密码。
func VerifyPassword(password, encoded string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 4 || parts[0] != "pbkdf2-sha256" {
		return false
	}
	var iter int
	if _, err := fmt.Sscanf(parts[1], "%d", &iter); err != nil || iter < 1000 {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[2])
	if err != nil {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[3])
	if err != nil {
		return false
	}
	got, err := pbkdf2.Key(sha256.New, password, salt, iter, len(want))
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare(got, want) == 1
}

// GeneratePassword 生成易读的随机初始管理密码。
func GeneratePassword() (string, error) {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "admin-" + base64.RawURLEncoding.EncodeToString(b), nil
}

// GenerateSecretPath 生成 16 字符随机路径后缀（URL 安全字符）。
func GenerateSecretPath() (string, error) {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

type session struct {
	expiresAt time.Time
}

// Manager 管理面板：会话、配置读写、状态采集。
type Manager struct {
	mu        sync.Mutex
	cfg       *config.Config
	cfgPath   string
	secretPath string
	sessions  map[string]session
	store     *store.Store
	logger    *slog.Logger
	startTime time.Time
	version   string
}

func NewManager(cfg *config.Config, cfgPath string, st *store.Store, version string, logger *slog.Logger) *Manager {
	m := &Manager{
		cfg:        cfg,
		cfgPath:    cfgPath,
		secretPath: cfg.Admin.SecretPath,
		sessions:   make(map[string]session),
		store:      st,
		logger:     logger,
		startTime:  time.Now(),
		version:    version,
	}
	go m.sessionJanitor()
	return m
}

func (m *Manager) sessionJanitor() {
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		m.mu.Lock()
		now := time.Now()
		for token, s := range m.sessions {
			if now.After(s.expiresAt) {
				delete(m.sessions, token)
			}
		}
		m.mu.Unlock()
	}
}

// Login 校验管理密码，成功返回会话令牌。
func (m *Manager) Login(password string) (string, error) {
	m.mu.Lock()
	hash := m.cfg.Admin.PasswordHash
	ttl := time.Duration(m.cfg.Admin.SessionTTLHours) * time.Hour
	m.mu.Unlock()

	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	if hash == "" || !VerifyPassword(password, hash) {
		// 统一延迟，减缓爆破
		time.Sleep(300 * time.Millisecond)
		return "", ErrBadPassword
	}

	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(tokenBytes)

	m.mu.Lock()
	m.sessions[token] = session{expiresAt: time.Now().Add(ttl)}
	m.mu.Unlock()
	return token, nil
}

func (m *Manager) Logout(token string) {
	m.mu.Lock()
	delete(m.sessions, token)
	m.mu.Unlock()
}

// SecretPath 返回当前管理面板的二次后缀路径。
func (m *Manager) SecretPath() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.secretPath
}

// Authenticated 校验会话令牌。
func (m *Manager) Authenticated(token string) bool {
	if token == "" {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[token]
	if !ok || time.Now().After(s.expiresAt) {
		delete(m.sessions, token)
		return false
	}
	return true
}

// InvalidateAllSessions 使全部会话失效（如修改密码后）。
func (m *Manager) InvalidateAllSessions() {
	m.mu.Lock()
	m.sessions = make(map[string]session)
	m.mu.Unlock()
}

// ChangePassword 校验旧密码后更新管理密码并写回配置。
func (m *Manager) ChangePassword(current, newPassword string) error {
	m.mu.Lock()
	hash := m.cfg.Admin.PasswordHash
	m.mu.Unlock()

	if hash == "" || !VerifyPassword(current, hash) {
		time.Sleep(300 * time.Millisecond)
		return ErrBadPassword
	}
	if len(newPassword) < 8 {
		return fmt.Errorf("新密码至少 8 个字符")
	}
	newHash, err := HashPassword(newPassword)
	if err != nil {
		return err
	}
	m.mu.Lock()
	m.cfg.Admin.PasswordHash = newHash
	err = config.WriteConfig(m.cfgPath, m.cfg)
	m.mu.Unlock()
	if err != nil {
		return fmt.Errorf("保存配置失败: %w", err)
	}
	m.InvalidateAllSessions()
	m.logger.Info("admin password changed")
	return nil
}
