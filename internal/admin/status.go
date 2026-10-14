package admin

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/Black0Bag/wikisql/internal/config"
)

type statusResponse struct {
	Version       string          `json:"version"`
	UptimeSeconds int64           `json:"uptime_seconds"`
	Time          string          `json:"time"`
	GoVersion     string          `json:"go_version"`
	Goroutines    int             `json:"goroutines"`
	RSSBytes      int64           `json:"rss_bytes"`
	Modules       map[string]any  `json:"modules"`
	DB            map[string]any  `json:"db"`
	Backup        map[string]any  `json:"backup"`
	Disk          map[string]any  `json:"disk"`
	HealthRecent  []map[string]any `json:"health_recent"`
}

func (m *Manager) Status(ctx context.Context) *statusResponse {
	m.mu.Lock()
	c := *m.cfg
	m.mu.Unlock()

	st := &statusResponse{
		Version:       m.version,
		UptimeSeconds: int64(time.Since(m.startTime).Seconds()),
		Time:          time.Now().Format("2006-01-02 15:04:05"),
		GoVersion:     runtime.Version(),
		Goroutines:    runtime.NumGoroutine(),
		RSSBytes:      readRSS(),
		Modules: map[string]any{
			"kb_enabled":   c.LLM.Embedding.BaseURL != "" && c.LLM.Embedding.Model != "",
			"mcp_enabled":  c.MCP.Enabled,
			"mcp_writes":   c.MCP.AllowWrites,
			"auth_mode":    authMode(c),
			"admin_enabled": c.Admin.Enabled,
		},
		DB:       m.dbStatus(ctx),
		Backup:   m.backupStatus(ctx, c.Database.BackupDir),
		Disk:     diskStatus(filepath.Dir(c.Database.Path)),
		HealthRecent: m.recentHealth(ctx),
	}
	return st
}

func authMode(c config.Config) string {
	if !c.Auth.Enabled {
		return "已关闭"
	}
	if c.Auth.APIKey != "" {
		return "API Key 模式"
	}
	return "JWT 登录模式"
}

func (m *Manager) dbStatus(ctx context.Context) map[string]any {
	out := map[string]any{}
	if size, err := m.store.DBSize(ctx); err == nil {
		out["size_bytes"] = size
	}
	out["wal_bytes"] = m.store.WALSize()
	if n, err := m.store.TableRowCount(ctx, "kb_collections"); err == nil {
		out["collections"] = n
	}
	if n, err := m.store.TableRowCount(ctx, "kb_documents"); err == nil {
		out["documents"] = n
	}
	if n, err := m.store.TableRowCount(ctx, "kb_chunks"); err == nil {
		out["chunks"] = n
	}
	if n, err := m.store.TableRowCount(ctx, "kb_embed_cache"); err == nil {
		out["embed_cache"] = n
	}
	if tables, err := m.store.ListTables(ctx); err == nil {
		out["tables"] = len(tables)
	}
	return out
}

func (m *Manager) backupStatus(ctx context.Context, backupDir string) map[string]any {
	out := map[string]any{}
	if t, err := m.store.LastBackupTime(ctx); err == nil && !t.IsZero() {
		out["last_time"] = t.Format("2006-01-02 15:04:05")
		out["last_ago"] = time.Since(t).Round(time.Minute).String()
	} else {
		out["last_time"] = "从未备份"
	}
	count := 0
	if entries, err := os.ReadDir(backupDir); err == nil {
		for _, e := range entries {
			if !e.IsDir() && strings.HasPrefix(e.Name(), "backup-") && strings.HasSuffix(e.Name(), ".db") {
				count++
			}
		}
	}
	out["count"] = count
	return out
}

func diskStatus(path string) map[string]any {
	out := map[string]any{"path": path}
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err == nil {
		total := int64(stat.Blocks) * int64(stat.Bsize)
		free := int64(stat.Bavail) * int64(stat.Bsize)
		out["total_bytes"] = total
		out["free_bytes"] = free
		if total > 0 {
			out["used_percent"] = int((total - free) * 100 / total)
		}
	}
	return out
}

func (m *Manager) recentHealth(ctx context.Context) []map[string]any {
	rows, err := m.store.RecentHealthChecks(ctx, 10)
	if err != nil {
		return nil
	}
	return rows
}

func readRSS() int64 {
	data, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "VmRSS:") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				if kb, err := strconv.ParseInt(fields[1], 10, 64); err == nil {
					return kb * 1024
				}
			}
		}
	}
	return 0
}

// ApplyConfig 把管理面板提交的字段补丁应用到配置并写回文件。
// 密钥字段：空字符串表示"保持原值"；非空表示更新。
func (m *Manager) ApplyConfig(patch map[string]any) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	c := *m.cfg // 先复制，校验通过后才提交

	setStr := func(dst *string, key string) {
		if v, ok := patch[key].(string); ok {
			*dst = v
		}
	}
	setInt := func(dst *int, key string) {
		if v, ok := patch[key].(float64); ok {
			*dst = int(v)
		}
	}
	setInt64 := func(dst *int64, key string) {
		if v, ok := patch[key].(float64); ok {
			*dst = int64(v)
		}
	}
	setBool := func(dst *bool, key string) {
		if v, ok := patch[key].(bool); ok {
			*dst = v
		}
	}
	setSecret := func(dst *string, key string) {
		if v, ok := patch[key].(string); ok && v != "" {
			*dst = v
		}
	}

	setStr(&c.Server.Host, "server.host")
	setInt(&c.Server.Port, "server.port")
	setStr(&c.Server.TLSCertFile, "server.tls_cert_file")
	setStr(&c.Server.TLSKeyFile, "server.tls_key_file")
	setBool(&c.Server.AcknowledgeRisk, "server.acknowledge_risk")

	setStr(&c.Database.Path, "database.path")
	setStr(&c.Database.BackupDir, "database.backup_dir")
	setInt(&c.Database.BackupInterval, "database.backup_interval_hours")

	setBool(&c.Auth.Enabled, "auth.enabled")
	setSecret(&c.Auth.APIKey, "auth.api_key")
	setSecret(&c.Auth.Password, "auth.password")
	setSecret(&c.Auth.JWTSecret, "auth.jwt_secret")
	setInt(&c.Auth.TokenTTLHours, "auth.token_ttl_hours")

	setBool(&c.Admin.Enabled, "admin.enabled")
	setInt(&c.Admin.SessionTTLHours, "admin.session_ttl_hours")

	setStr(&c.Files.Dir, "files.dir")
	setInt(&c.Files.MaxInlineSize, "files.max_inline_size_bytes")

	setInt(&c.Health.CheckIntervalMinutes, "health.check_interval_minutes")

	setStr(&c.LLM.Chat.Provider, "llm.chat.provider")
	setStr(&c.LLM.Chat.BaseURL, "llm.chat.base_url")
	setSecret(&c.LLM.Chat.APIKey, "llm.chat.api_key")
	setStr(&c.LLM.Chat.Model, "llm.chat.model")
	setStr(&c.LLM.Embedding.Provider, "llm.embedding.provider")
	setStr(&c.LLM.Embedding.BaseURL, "llm.embedding.base_url")
	setSecret(&c.LLM.Embedding.APIKey, "llm.embedding.api_key")
	setStr(&c.LLM.Embedding.Model, "llm.embedding.model")

	setInt(&c.KB.ChunkMaxRunes, "kb.chunk_max_runes")
	setInt(&c.KB.EmbedBatchSize, "kb.embed_batch_size")
	setBool(&c.KB.ContextHeaders, "kb.context_headers")

	setBool(&c.MCP.Enabled, "mcp.enabled")
	setBool(&c.MCP.AllowWrites, "mcp.allow_writes")

	setInt64(&c.Security.MaxBodyBytes, "security.max_body_bytes")
	setInt(&c.Security.GlobalRPM, "security.global_rate_per_min")
	setInt(&c.Security.LoginRPM, "security.login_rate_per_min")

	// 环境变量覆盖会在重启后生效，这里先做基础校验
	if err := c.Validate(); err != nil {
		return err
	}
	if err := config.WriteConfig(m.cfgPath, &c); err != nil {
		return fmt.Errorf("写入配置失败: %w", err)
	}
	*m.cfg = c
	m.logger.Info("admin updated config", "fields", len(patch))
	return nil
}

// TriggerRestart 利用 init.d 脚本在 3 秒后重启服务（响应先返回，再重启）。
func (m *Manager) TriggerRestart() error {
	script := "/etc/init.d/wikisql"
	if _, err := os.Stat(script); err != nil {
		return fmt.Errorf("未找到 %s，请手动重启服务", script)
	}
	cmd := exec.Command("sh", "-c", "sleep 3; "+script+" restart")
	// 脱离父进程：独立会话，输出丢弃
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("启动重启命令失败: %w", err)
	}
	m.logger.Info("restart triggered via admin panel")
	return nil
}
