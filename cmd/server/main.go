package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"flag"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Black0Bag/wikisql/internal/config"
	"github.com/Black0Bag/wikisql/internal/admin"
	"github.com/Black0Bag/wikisql/internal/auth"
	"github.com/Black0Bag/wikisql/internal/health"
	"github.com/Black0Bag/wikisql/internal/kb"
	"github.com/Black0Bag/wikisql/internal/llm"
	"github.com/Black0Bag/wikisql/internal/mcpserver"
	"github.com/Black0Bag/wikisql/internal/httpapi"
	"github.com/Black0Bag/wikisql/internal/store"
	"github.com/Black0Bag/wikisql/internal/table"
)

const appVersion = "0.3.0"

func main() {
	configPath := flag.String("config", "config.yaml", "配置文件路径")
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	cfg, err := config.Load(*configPath)
	if err != nil {
		logger.Error("failed to load config", "err", err)
		os.Exit(1)
	}

	s, err := store.New(cfg.Database.Path, logger)
	if err != nil {
		logger.Error("failed to open database", "err", err)
		os.Exit(1)
	}
	defer s.Close()

	logger.Info("database opened", "path", cfg.Database.Path)

	// 启动备份（内置 10 分钟去重，防止重启风暴导致备份泛滥）
	if _, err := s.Backup(context.Background(), cfg.Database.BackupDir); err != nil {
		logger.Warn("initial backup failed", "err", err)
	}
	// 清理旧备份，保留最近 7 份
	if removed, err := s.PruneBackups(cfg.Database.BackupDir, 7); err != nil {
		logger.Warn("backup prune failed", "err", err)
	} else if removed > 0 {
		logger.Info("startup backup prune", "removed", removed)
	}


	checker := health.New(s, cfg.Database.BackupDir, cfg.Files.Dir, cfg.Health.CheckIntervalMinutes, logger)
	checker.Start()
	defer checker.Stop()

	tablesSvc := table.New(s)
	apiServer := httpapi.New(tablesSvc, s, logger)
	apiServer.SetSecurity(httpapi.SecurityOptions{
		MaxBodyBytes: cfg.Security.MaxBodyBytes,
		GlobalRPM:    cfg.Security.GlobalRPM,
		LoginRPM:     cfg.Security.LoginRPM,
	})

	// 管理面板：独立密码 + 会话 + 二次后缀路径；首次启动自动生成并写入配置
	if cfg.Admin.Enabled {
		if cfg.Admin.SecretPath == "" {
			secret, err := admin.GenerateSecretPath()
			if err != nil {
				logger.Error("failed to generate admin secret path", "err", err)
				os.Exit(1)
			}
			cfg.Admin.SecretPath = secret
			if err := config.WriteConfig(*configPath, cfg); err != nil {
				logger.Warn("failed to persist admin secret path", "err", err)
			}
			logger.Warn("=====================================================================")
			logger.Warn("管理面板地址", "path", "/admin/"+secret)
			logger.Warn("（此地址是管理面板的唯一入口，请保存；错误后缀将看到伪造的 404 页面）")
			logger.Warn("=====================================================================")
		}
		if cfg.Admin.PasswordHash == "" {
			initPw, err := admin.GeneratePassword()
			if err != nil {
				logger.Error("failed to generate admin password", "err", err)
				os.Exit(1)
			}
			hash, err := admin.HashPassword(initPw)
			if err != nil {
				logger.Error("failed to hash admin password", "err", err)
				os.Exit(1)
			}
			cfg.Admin.PasswordHash = hash
			if err := config.WriteConfig(*configPath, cfg); err != nil {
				logger.Warn("failed to persist admin password to config", "err", err)
			}
			logger.Warn("=========================================================")
			logger.Warn("管理面板初始密码（请登录 /admin 后立即修改）", "password", initPw)
			logger.Warn("=========================================================")
		}
		adminMgr := admin.NewManager(cfg, *configPath, s, appVersion, logger)
		apiServer.SetAdmin(adminMgr)
		logger.Info("admin panel enabled", "path", "/admin/"+cfg.Admin.SecretPath)
	}

	// 鉴权：启用后 /api/v1/*（除登录）与 /mcp 都需要 Bearer token
	// API Key 模式（推荐自用）：配置 auth.api_key，所有请求直接用它当 Bearer 值，永不过期
	// JWT 模式（多用户/公网）：配置 auth.password，登录换 token
	if cfg.Auth.Enabled {
		var authMgr *auth.Manager
		if cfg.Auth.APIKey != "" {
			authMgr = auth.NewAPIKeyManager(cfg.Auth.APIKey)
			logger.Info("authentication enabled (API Key mode)", "mode", "static bearer token")
		} else {
			secret := cfg.Auth.JWTSecret
			if secret == "" {
				secret, err = auth.GenerateSecret()
				if err != nil {
					logger.Error("failed to generate jwt secret", "err", err)
					os.Exit(1)
				}
				logger.Warn("auth.jwt_secret not set; using ephemeral secret (tokens will not survive restart)")
			}
			authMgr = auth.NewManager(secret, cfg.Auth.Password, cfg.Auth.TokenTTLHours)
			logger.Info("authentication enabled (JWT mode)", "token_ttl_hours", cfg.Auth.TokenTTLHours)
		}
		apiServer.SetAuth(authMgr)
	}

	var kbSvc *kb.Service
	if cfg.LLM.Embedding.BaseURL != "" && cfg.LLM.Embedding.Model != "" {
		embedClient := llm.NewEmbedClient(llm.ModelConfig{
			Provider: cfg.LLM.Embedding.Provider,
			BaseURL:  cfg.LLM.Embedding.BaseURL,
			APIKey:   cfg.LLM.Embedding.APIKey,
			Model:    cfg.LLM.Embedding.Model,
		}, logger)
		var chatClient llm.ChatClient
		if cfg.LLM.Chat.BaseURL != "" && cfg.LLM.Chat.Model != "" {
			chatClient = llm.NewChatClient(llm.ModelConfig{
				Provider: cfg.LLM.Chat.Provider,
				BaseURL:  cfg.LLM.Chat.BaseURL,
				APIKey:   cfg.LLM.Chat.APIKey,
				Model:    cfg.LLM.Chat.Model,
			}, logger)
		}

		var err error
		kbSvc, err = kb.New(s, embedClient, chatClient, kb.Options{
			ChunkMaxRunes:  cfg.KB.ChunkMaxRunes,
			ContextHeaders: cfg.KB.ContextHeaders,
			EmbedBatchSize: cfg.KB.EmbedBatchSize,
		}, logger)
		if err != nil {
			logger.Error("failed to init knowledge base", "err", err)
			os.Exit(1)
		}

		if !kbSvc.DimensionReady(context.Background()) {
			logger.Info("probing embedding dimension")
			vectors, err := embedClient.Embed(context.Background(), []string{"dimension probe"})
			if err != nil {
				logger.Error("failed to probe embedding dimension", "err", err)
				os.Exit(1)
			}
			dim := len(vectors[0])
			if err := kbSvc.InitDimension(context.Background(), dim); err != nil {
				logger.Error("failed to init embedding dimension", "err", err)
				os.Exit(1)
			}
			logger.Info("embedding dimension initialized", "dimension", dim)
		}

		apiServer.RegisterKB(kbSvc)
		logger.Info("knowledge base module enabled")
	} else {
		logger.Info("knowledge base module disabled (llm.embedding not configured)")
	}

	// MCP 端点：外部 agent 的标准接入通道（与 REST 同一鉴权与安全中间件）
	if cfg.MCP.Enabled {
		mcpSrv := mcpserver.New(tablesSvc, kbSvc, mcpserver.Config{AllowWrites: cfg.MCP.AllowWrites}, logger)
		apiServer.SetMCP(mcpSrv.Handler())
		logger.Info("mcp endpoint enabled", "path", "/mcp", "allow_writes", cfg.MCP.AllowWrites)
	}

	handler := apiServer.Routes()

	srv := &http.Server{
		Addr:              cfg.ListenAddr(),
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// 定时备份（按 backup_interval_hours 配置，默认 24 小时）
	if cfg.Database.BackupInterval > 0 {
		go func() {
			interval := time.Duration(cfg.Database.BackupInterval) * time.Hour
			ticker := time.NewTicker(interval)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					if _, err := s.Backup(context.Background(), cfg.Database.BackupDir); err != nil {
						logger.Warn("scheduled backup failed", "err", err)
					}
					if removed, err := s.PruneBackups(cfg.Database.BackupDir, 7); err != nil {
						logger.Warn("scheduled prune failed", "err", err)
					} else if removed > 0 {
						logger.Info("scheduled prune", "removed", removed)
					}
				case <-ctx.Done():
					return
				}
			}
		}()
		logger.Info("scheduled backup enabled", "interval_hours", cfg.Database.BackupInterval)
	}

	go func() {
		logger.Info("listening", "addr", cfg.ListenAddr(), "tls", cfg.IsTLS())
		var err error
		if cfg.IsTLS() {
			err = srv.ListenAndServeTLS(cfg.Server.TLSCertFile, cfg.Server.TLSKeyFile)
		} else {
			err = srv.ListenAndServe()
		}
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("listen failed", "err", err)
			stop()
		}
	}()

	<-ctx.Done()
	logger.Info("shutting down")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("shutdown error", "err", err)
	}

	logger.Info("shutdown complete")
}
