package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/aicode/kb-server/internal/config"
	"github.com/aicode/kb-server/internal/auth"
	"github.com/aicode/kb-server/internal/health"
	"github.com/aicode/kb-server/internal/kb"
	"github.com/aicode/kb-server/internal/llm"
	"github.com/aicode/kb-server/internal/mcpserver"
	"github.com/aicode/kb-server/internal/httpapi"
	"github.com/aicode/kb-server/internal/store"
	"github.com/aicode/kb-server/internal/table"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	cfg, err := config.Load("config.yaml")
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

	if _, err := s.Backup(context.Background(), cfg.Database.BackupDir); err != nil {
		logger.Warn("initial backup failed", "err", err)
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

	// 鉴权：启用后 /api/v1/*（除登录）与 /mcp 都需要 Bearer token
	if cfg.Auth.Enabled {
		secret := cfg.Auth.JWTSecret
		if secret == "" {
			secret, err = auth.GenerateSecret()
			if err != nil {
				logger.Error("failed to generate jwt secret", "err", err)
				os.Exit(1)
			}
			logger.Warn("auth.jwt_secret not set; using ephemeral secret (tokens will not survive restart)")
		}
		authMgr := auth.NewManager(secret, cfg.Auth.Password, cfg.Auth.TokenTTLHours)
		apiServer.SetAuth(authMgr)
		logger.Info("authentication enabled", "token_ttl_hours", cfg.Auth.TokenTTLHours)
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
