package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

// WriteConfig 把配置完整写回文件（原子写：临时文件 + 改名）。
// 输出为带注释的模板格式；文件权限 0600（含密钥）。
// 注意：会用标准模板覆盖原文件——用户在文件里的自定义注释不会保留。
func WriteConfig(path string, c *Config) error {
	var b []byte
	w := func(format string, args ...any) {
		b = append(b, fmt.Sprintf(format, args...)...)
	}
	q := func(s string) string { return strconv.Quote(s) }
	bq := func(v bool) string { return strconv.FormatBool(v) }

	w("# wikisql 配置文件\n")
	w("# 本文件由服务自动管理（管理面板保存时会重写为本模板格式）。\n")
	w("# 修改后需重启服务生效：/etc/init.d/wikisql restart\n\n")

	w("server:\n")
	w("  host: %s            # 监听地址：127.0.0.1 仅本机；0.0.0.0 对外\n", q(c.Server.Host))
	w("  port: %d\n", c.Server.Port)
	w("  tls_cert_file: %s\n", q(c.Server.TLSCertFile))
	w("  tls_key_file: %s\n", q(c.Server.TLSKeyFile))
	w("  acknowledge_risk: %s  # 公网监听且未启用鉴权时的确认开关\n\n", bq(c.Server.AcknowledgeRisk))

	w("database:\n")
	w("  path: %s\n", q(c.Database.Path))
	w("  backup_dir: %s\n", q(c.Database.BackupDir))
	w("  backup_interval_hours: %d   # 定时备份间隔（0=关闭）\n\n", c.Database.BackupInterval)

	w("auth:\n")
	w("  enabled: %s\n", bq(c.Auth.Enabled))
	w("  api_key: %s        # 静态访问密钥（推荐，永不过期）\n", q(c.Auth.APIKey))
	w("  password: %s       # JWT 登录模式密码（可选）\n", q(c.Auth.Password))
	w("  jwt_secret: %s\n", q(c.Auth.JWTSecret))
	w("  token_ttl_hours: %d\n\n", c.Auth.TokenTTLHours)

	w("admin:\n")
	w("  enabled: %s\n", bq(c.Admin.Enabled))
	w("  password_hash: %s   # 管理面板密码（PBKDF2 哈希，不存明文）\n", q(c.Admin.PasswordHash))
	w("  secret_path: %s      # 二次后缀（/admin/<secret_path>），留空=首次自动生成\n", q(c.Admin.SecretPath))
	w("  session_ttl_hours: %d\n\n", c.Admin.SessionTTLHours)

	w("files:\n")
	w("  dir: %s\n", q(c.Files.Dir))
	w("  max_inline_size_bytes: %d\n\n", c.Files.MaxInlineSize)

	w("health:\n")
	w("  check_interval_minutes: %d\n\n", c.Health.CheckIntervalMinutes)

	w("llm:\n")
	w("  chat:\n")
	w("    provider: %s\n", q(c.LLM.Chat.Provider))
	w("    base_url: %s\n", q(c.LLM.Chat.BaseURL))
	w("    api_key: %s\n", q(c.LLM.Chat.APIKey))
	w("    model: %s\n", q(c.LLM.Chat.Model))
	w("  embedding:\n")
	w("    provider: %s\n", q(c.LLM.Embedding.Provider))
	w("    base_url: %s\n", q(c.LLM.Embedding.BaseURL))
	w("    api_key: %s\n", q(c.LLM.Embedding.APIKey))
	w("    model: %s\n\n", q(c.LLM.Embedding.Model))

	w("kb:\n")
	w("  chunk_max_runes: %d\n", c.KB.ChunkMaxRunes)
	w("  embed_batch_size: %d\n", c.KB.EmbedBatchSize)
	w("  context_headers: %s   # Anthropic 上下文预置（开启增加模型调用成本）\n\n", bq(c.KB.ContextHeaders))

	w("mcp:\n")
	w("  enabled: %s\n", bq(c.MCP.Enabled))
	w("  allow_writes: %s   # 是否允许外部 agent 写入数据库（谨慎开启）\n\n", bq(c.MCP.AllowWrites))

	w("security:\n")
	w("  max_body_bytes: %d\n", c.Security.MaxBodyBytes)
	w("  global_rate_per_min: %d\n", c.Security.GlobalRPM)
	w("  login_rate_per_min: %d\n", c.Security.LoginRPM)

	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".config-*.tmp")
	if err != nil {
		return fmt.Errorf("creating temp config: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // 成功后 rename 会先移除；失败时清理

	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return fmt.Errorf("writing temp config: %w", err)
	}
	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		return fmt.Errorf("chmod temp config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing temp config: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("renaming config: %w", err)
	}
	return nil
}
