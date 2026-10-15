package admin

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
)

const sessionCookie = "wikisql_admin_session"

type APIError struct {
	Error string `json:"error"`
	Code  int    `json:"-"`
}

// Routes 挂载管理面板路由。secretPath 为二次后缀（/admin/<secretPath>）。
// 访问 /admin 或 /admin/ 返回伪装错误页；只有 /admin/<secretPath> 才是真实入口。
// loginMiddleware 为登录端点的额外严格限流（可为 nil）。
func (m *Manager) Routes(r chi.Router, secretPath string, loginMiddleware func(http.Handler) http.Handler) {
	basePath := "/admin/" + secretPath

	// 伪装路由：/admin 和 /admin/ 返回 404 风格页面（不暴露管理面板存在）
	r.Get("/admin", serveDecoy)
	r.Get("/admin/", serveDecoy)
	// 其他 /admin/* 但不匹配 secretPath 的也走伪装（chi 自动 404 会暴露路由存在，
	// 所以显式注册通配，Handle 不限方法，避免 405 泄露路由信息）
	r.Handle("/admin/*", http.HandlerFunc(serveDecoy))

	// 真实管理面板路由
	r.Get(basePath, m.serveIndex)
	r.Get(basePath+"/", m.serveIndex)

	r.Route(basePath+"/api", func(r chi.Router) {
		if loginMiddleware != nil {
			r.With(loginMiddleware).Post("/login", m.handleLogin)
		} else {
			r.Post("/login", m.handleLogin)
		}

		// 其余端点都需要会话
		r.Group(func(r chi.Router) {
			r.Use(m.sessionMiddleware)
			r.Post("/logout", m.handleLogout)
			r.Get("/session", m.handleSessionCheck)
			r.Get("/status", m.handleStatus)
			r.Get("/config", m.handleGetConfig)
			r.Post("/config", m.handleSaveConfig)
			r.Post("/password", m.handleChangePassword)
			r.Post("/restart", m.handleRestart)
		})
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, APIError{Error: msg})
}

// sessionMiddleware 校验会话 Cookie。
func (m *Manager) sessionMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(sessionCookie)
		if err != nil || !m.Authenticated(c.Value) {
			writeErr(w, http.StatusUnauthorized, "未登录或会话已过期")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (m *Manager) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	token, err := m.Login(req.Password)
	if err != nil {
		writeErr(w, http.StatusUnauthorized, err.Error())
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/admin/" + m.secretPath,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   r.TLS != nil,
		MaxAge:   m.cfg.Admin.SessionTTLHours * 3600,
	})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (m *Manager) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		m.Logout(c.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/admin/" + m.secretPath, MaxAge: -1, HttpOnly: true})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (m *Manager) handleSessionCheck(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"authenticated": true})
}

// 配置字段视图：不含明文密钥，只标记是否已设置。
type configField struct {
	Key      string   `json:"key"`
	Label    string   `json:"label"`
	Type     string   `json:"type"` // string | int | bool | select | secret | model
	Value    any      `json:"value,omitempty"`
	HasValue bool     `json:"has_value,omitempty"`
	Options  []string `json:"options,omitempty"`
	Help     string   `json:"help,omitempty"`
}

type configSection struct {
	Name   string        `json:"name"`
	Fields []configField `json:"fields"`
}

// configView 构建完整字段清单（所有可设置项）。
func (m *Manager) configView() []configSection {
	m.mu.Lock()
	c := *m.cfg
	m.mu.Unlock()

	secret := func(key, label string, v string) configField {
		return configField{Key: key, Label: label, Type: "secret", HasValue: v != ""}
	}
	return []configSection{
		{Name: "服务", Fields: []configField{
			{Key: "server.host", Label: "监听地址", Type: "string", Value: c.Server.Host, Help: "127.0.0.1 仅本机；0.0.0.0 对外"},
			{Key: "server.port", Label: "端口", Type: "int", Value: c.Server.Port},
			{Key: "server.tls_cert_file", Label: "TLS 证书路径", Type: "string", Value: c.Server.TLSCertFile},
			{Key: "server.tls_key_file", Label: "TLS 私钥路径", Type: "string", Value: c.Server.TLSKeyFile},
			{Key: "server.acknowledge_risk", Label: "已知晓公网无鉴权风险", Type: "bool", Value: c.Server.AcknowledgeRisk},
		}},
		{Name: "数据库", Fields: []configField{
			{Key: "database.path", Label: "数据库文件路径", Type: "string", Value: c.Database.Path},
			{Key: "database.backup_dir", Label: "备份目录", Type: "string", Value: c.Database.BackupDir},
			{Key: "database.backup_interval_hours", Label: "定时备份间隔（小时）", Type: "int", Value: c.Database.BackupInterval, Help: "0 表示关闭"},
		}},
		{Name: "鉴权", Fields: []configField{
			{Key: "auth.enabled", Label: "启用鉴权", Type: "bool", Value: c.Auth.Enabled},
			secret("auth.api_key", "静态 API Key", c.Auth.APIKey),
			secret("auth.password", "JWT 登录密码", c.Auth.Password),
			secret("auth.jwt_secret", "JWT 签名密钥", c.Auth.JWTSecret),
			{Key: "auth.token_ttl_hours", Label: "JWT 有效期（小时）", Type: "int", Value: c.Auth.TokenTTLHours},
		}},
		{Name: "管理面板", Fields: []configField{
			{Key: "admin.enabled", Label: "启用管理面板", Type: "bool", Value: c.Admin.Enabled},
			{Key: "admin.session_ttl_hours", Label: "会话有效期（小时）", Type: "int", Value: c.Admin.SessionTTLHours},
		}},
		{Name: "文件存储", Fields: []configField{
			{Key: "files.dir", Label: "外置文件目录", Type: "string", Value: c.Files.Dir},
			{Key: "files.max_inline_size_bytes", Label: "小文件内联阈值（字节）", Type: "int", Value: c.Files.MaxInlineSize},
		}},
		{Name: "心跳巡检", Fields: []configField{
			{Key: "health.check_interval_minutes", Label: "巡检间隔（分钟）", Type: "int", Value: c.Health.CheckIntervalMinutes},
		}},
		{Name: "外部模型", Fields: []configField{
			{Key: "llm.chat.provider", Label: "对话供应商", Type: "select", Value: c.LLM.Chat.Provider, Options: []string{"siliconflow", "openai", "deepseek", "custom"}},
			{Key: "llm.chat.base_url", Label: "对话接口地址", Type: "string", Value: c.LLM.Chat.BaseURL},
			secret("llm.chat.api_key", "对话模型 API Key", c.LLM.Chat.APIKey),
			{Key: "llm.chat.model", Label: "对话模型", Type: "model", Value: c.LLM.Chat.Model, Options: []string{"Qwen/Qwen2.5-7B-Instruct", "Qwen/Qwen3-8B", "deepseek-ai/DeepSeek-V3", "Pro/deepseek-ai/DeepSeek-V3", "gpt-4o-mini", "gpt-4o"}},
			{Key: "llm.embedding.provider", Label: "嵌入供应商", Type: "select", Value: c.LLM.Embedding.Provider, Options: []string{"siliconflow", "openai", "custom"}},
			{Key: "llm.embedding.base_url", Label: "嵌入接口地址", Type: "string", Value: c.LLM.Embedding.BaseURL},
			secret("llm.embedding.api_key", "嵌入模型 API Key", c.LLM.Embedding.APIKey),
			{Key: "llm.embedding.model", Label: "嵌入模型", Type: "model", Value: c.LLM.Embedding.Model, Options: []string{"BAAI/bge-m3", "BAAI/bge-large-zh-v1.5", "text-embedding-3-small"}, Help: "更换嵌入模型后需重新灌入知识库（维度变化）"},
		}},
		{Name: "知识库", Fields: []configField{
			{Key: "kb.chunk_max_runes", Label: "分块上限（字符）", Type: "int", Value: c.KB.ChunkMaxRunes},
			{Key: "kb.embed_batch_size", Label: "嵌入批大小", Type: "int", Value: c.KB.EmbedBatchSize},
			{Key: "kb.context_headers", Label: "上下文预置（费模型额度）", Type: "bool", Value: c.KB.ContextHeaders},
		}},
		{Name: "MCP（供 AI agent 调用）", Fields: []configField{
			{Key: "mcp.enabled", Label: "启用 MCP 端点", Type: "bool", Value: c.MCP.Enabled},
			{Key: "mcp.allow_writes", Label: "允许 agent 写数据库（谨慎）", Type: "bool", Value: c.MCP.AllowWrites},
		}},
		{Name: "安全防护", Fields: []configField{
			{Key: "security.max_body_bytes", Label: "请求体上限（字节）", Type: "int", Value: c.Security.MaxBodyBytes},
			{Key: "security.global_rate_per_min", Label: "全局限流（次/分钟/IP）", Type: "int", Value: c.Security.GlobalRPM},
			{Key: "security.login_rate_per_min", Label: "登录限流（次/分钟/IP）", Type: "int", Value: c.Security.LoginRPM},
		}},
	}
}

func (m *Manager) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"sections": m.configView()})
}

func (m *Manager) handleSaveConfig(w http.ResponseWriter, r *http.Request) {
	var patch map[string]any
	if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
		writeErr(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	if err := m.ApplyConfig(patch); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "已保存，重启服务后生效"})
}

func (m *Manager) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Current string `json:"current"`
		New     string `json:"new"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	if err := m.ChangePassword(req.Current, req.New); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	// 全部会话已失效，清 Cookie 让前端回登录页
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/admin/" + m.secretPath, MaxAge: -1, HttpOnly: true})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "密码已更新，请重新登录"})
}

func (m *Manager) handleRestart(w http.ResponseWriter, r *http.Request) {
	if err := m.TriggerRestart(); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "服务将在 3 秒后重启"})
}

func (m *Manager) handleStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, m.Status(r.Context()))
}
