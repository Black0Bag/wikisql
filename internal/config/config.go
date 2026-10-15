package config

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server   ServerConfig   `yaml:"server"`
	Database DatabaseConfig `yaml:"database"`
	Auth     AuthConfig     `yaml:"auth"`
	Admin    AdminConfig    `yaml:"admin"`
	Files    FilesConfig    `yaml:"files"`
	LLM      LLMConfig      `yaml:"llm"`
	Health   HealthConfig   `yaml:"health"`
	KB       KBConfig       `yaml:"kb"`
	MCP      MCPConfig      `yaml:"mcp"`
	Security SecurityConfig `yaml:"security"`
}

type AdminConfig struct {
	Enabled         bool   `yaml:"enabled"`
	PasswordHash    string `yaml:"password_hash"`
	SecretPath      string `yaml:"secret_path"`
	SessionTTLHours int    `yaml:"session_ttl_hours"`
}

type MCPConfig struct {
	Enabled     bool `yaml:"enabled"`
	AllowWrites bool `yaml:"allow_writes"`
}

type SecurityConfig struct {
	MaxBodyBytes int64 `yaml:"max_body_bytes"`
	GlobalRPM    int   `yaml:"global_rate_per_min"`
	LoginRPM     int   `yaml:"login_rate_per_min"`
}

type KBConfig struct {
	ChunkMaxRunes  int  `yaml:"chunk_max_runes"`
	EmbedBatchSize int  `yaml:"embed_batch_size"`
	ContextHeaders bool `yaml:"context_headers"`
}

type ServerConfig struct {
	Host            string `yaml:"host"`
	Port            int    `yaml:"port"`
	TLSCertFile     string `yaml:"tls_cert_file"`
	TLSKeyFile      string `yaml:"tls_key_file"`
	AcknowledgeRisk bool   `yaml:"acknowledge_risk"`
}

type DatabaseConfig struct {
	Path        string `yaml:"path"`
	BackupDir   string `yaml:"backup_dir"`
	BackupInterval int  `yaml:"backup_interval_hours"`
}

type AuthConfig struct {
	Enabled       bool   `yaml:"enabled"`
	APIKey        string `yaml:"api_key"`
	JWTSecret     string `yaml:"jwt_secret"`
	TokenTTLHours int    `yaml:"token_ttl_hours"`
	Password      string `yaml:"password"`
}

type FilesConfig struct {
	Dir          string `yaml:"dir"`
	MaxInlineSize int   `yaml:"max_inline_size_bytes"`
}

type LLMConfig struct {
	Chat      ModelConfig   `yaml:"chat"`
	Embedding ModelConfig   `yaml:"embedding"`
}

type ModelConfig struct {
	Provider string `yaml:"provider"`
	BaseURL  string `yaml:"base_url"`
	APIKey   string `yaml:"api_key"`
	Model    string `yaml:"model"`
}

type HealthConfig struct {
	CheckIntervalMinutes int `yaml:"check_interval_minutes"`
}

func Default() *Config {
	return &Config{
		Server: ServerConfig{
			Host: "127.0.0.1",
			Port: 8080,
		},
		Database: DatabaseConfig{
			Path:          "data/main.db",
			BackupDir:     "data/backups",
			BackupInterval: 24,
		},
		Auth: AuthConfig{
			Enabled:       false,
			JWTSecret:     "",
			TokenTTLHours: 72,
		},
		Files: FilesConfig{
			Dir:          "data/files",
			MaxInlineSize: 256 * 1024,
		},
		Health: HealthConfig{
			CheckIntervalMinutes: 30,
		},
		KB: KBConfig{
			ChunkMaxRunes:  1000,
			EmbedBatchSize: 32,
			ContextHeaders: false,
		},
		Admin: AdminConfig{
			Enabled:         true,
			SecretPath:      "",
			SessionTTLHours: 24,
		},
		MCP: MCPConfig{
			Enabled:     true,
			AllowWrites: false,
		},
		Security: SecurityConfig{
			MaxBodyBytes: 64 * 1024 * 1024,
			GlobalRPM:    120,
			LoginRPM:     5,
		},
	}
}

func Load(path string) (*Config, error) {
	cfg := Default()

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			applyEnvOverrides(cfg)
			if err := cfg.Validate(); err != nil {
				return nil, err
			}
			return cfg, nil
		}
		return nil, fmt.Errorf("reading config file %s: %w", path, err)
	}

	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parsing config file %s: %w", path, err)
	}

	applyEnvOverrides(cfg)

	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}

func applyEnvOverrides(cfg *Config) {
	if v := os.Getenv("WIKISQL_SERVER_HOST"); v != "" {
		cfg.Server.Host = v
	}
	if v := os.Getenv("WIKISQL_SERVER_PORT"); v != "" {
		var port int
		if _, err := fmt.Sscanf(v, "%d", &port); err == nil && port > 0 {
			cfg.Server.Port = port
		}
	}
	if v := os.Getenv("WIKISQL_DB_PATH"); v != "" {
		cfg.Database.Path = v
	}
	if v := os.Getenv("WIKISQL_AUTH_SECRET"); v != "" {
		cfg.Auth.JWTSecret = v
		cfg.Auth.Enabled = true
	}
	if v := os.Getenv("WIKISQL_AUTH_PASSWORD"); v != "" {
		cfg.Auth.Password = v
		cfg.Auth.Enabled = true
	}
	if v := os.Getenv("WIKISQL_AUTH_API_KEY"); v != "" {
		cfg.Auth.APIKey = v
		cfg.Auth.Enabled = true
	}
	if v := os.Getenv("WIKISQL_LLM_CHAT_KEY"); v != "" {
		cfg.LLM.Chat.APIKey = v
	}
	if v := os.Getenv("WIKISQL_LLM_EMBED_KEY"); v != "" {
		cfg.LLM.Embedding.APIKey = v
	}
}

func (c *Config) Validate() error {
	if c.Server.Port < 1 || c.Server.Port > 65535 {
		return fmt.Errorf("invalid server port: %d", c.Server.Port)
	}
	if c.Database.Path == "" {
		return fmt.Errorf("database path must not be empty")
	}
	if c.Auth.Enabled && c.Auth.APIKey == "" && c.Auth.Password == "" {
		return fmt.Errorf("auth enabled but both api_key and password are empty; set auth.api_key (recommended) or auth.password")
	}
	if c.Admin.Enabled && c.Admin.SecretPath != "" {
		for _, r := range c.Admin.SecretPath {
			if !(r == '_' || r == '-' ||
				(r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
				(r >= '0' && r <= '9')) {
				return fmt.Errorf("admin.secret_path contains invalid character %q; only alphanumeric, underscore and hyphen allowed", r)
			}
		}
	}
	if c.isPublicHost() && !c.Auth.Enabled && !c.Server.AcknowledgeRisk {
		msg := "server is configured to listen on a public address (" + c.Server.Host + ") without authentication; set acknowledge_risk: true in config to confirm you understand the risk"
		return fmt.Errorf("%s", msg)
	}
	return nil
}

func (c *Config) isPublicHost() bool {
	host := strings.TrimSpace(c.Server.Host)
	if host == "" || host == "localhost" || host == "127.0.0.1" || host == "::1" {
		return false
	}
	return true
}

func (c *Config) IsTLS() bool {
	return c.Server.TLSCertFile != "" && c.Server.TLSKeyFile != ""
}

func (c *Config) ListenAddr() string {
	return fmt.Sprintf("%s:%d", c.Server.Host, c.Server.Port)
}
