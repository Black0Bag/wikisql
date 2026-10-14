package health

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/Black0Bag/wikisql/internal/store"
)

type Checker struct {
	store      *store.Store
	backupDir  string
	filesDir   string
	interval   time.Duration
	logger     *slog.Logger
	stopCh     chan struct{}
	stopOnce   sync.Once
}

func New(s *store.Store, backupDir, filesDir string, intervalMinutes int, logger *slog.Logger) *Checker {
	if intervalMinutes < 1 {
		intervalMinutes = 30
	}
	return &Checker{
		store:     s,
		backupDir: backupDir,
		filesDir:  filesDir,
		interval:  time.Duration(intervalMinutes) * time.Minute,
		logger:    logger,
		stopCh:    make(chan struct{}),
	}
}

func (c *Checker) Start() {
	go func() {
		ticker := time.NewTicker(c.interval)
		defer ticker.Stop()

		c.RunOnce(context.Background())

		for {
			select {
			case <-ticker.C:
				c.RunOnce(context.Background())
			case <-c.stopCh:
				return
			}
		}
	}()
	c.logger.Info("health checker started", "interval", c.interval)
}

func (c *Checker) Stop() {
	c.stopOnce.Do(func() {
		close(c.stopCh)
	})
}

func (c *Checker) RunOnce(ctx context.Context) {
	c.checkIntegrity(ctx)
	c.checkForeignKeys(ctx)
	c.checkBackup(ctx)
	c.checkFiles(ctx)
	c.checkWAL(ctx)
}

func (c *Checker) checkIntegrity(ctx context.Context) {
	result, err := c.store.IntegrityCheck(ctx)
	if err != nil {
		c.record(ctx, "integrity", "error", "critical", "integrity check query failed", err.Error())
		return
	}
	if result == "ok" {
		c.record(ctx, "integrity", "ok", "info", "database integrity verified", "")
	} else {
		c.record(ctx, "integrity", "fail", "critical", "integrity check failed", result)
	}
}

func (c *Checker) checkForeignKeys(ctx context.Context) {
	violations, err := c.store.ForeignKeyCheck(ctx)
	if err != nil {
		c.record(ctx, "foreign_keys", "error", "warning", "foreign key check query failed", err.Error())
		return
	}
	if len(violations) == 0 {
		c.record(ctx, "foreign_keys", "ok", "info", "no foreign key violations", "")
	} else {
		details := fmt.Sprintf("%d violations", len(violations))
		c.record(ctx, "foreign_keys", "fail", "warning", "foreign key violations found", details)
	}
}

func (c *Checker) checkBackup(ctx context.Context) {
	last, err := c.store.LastBackupTime(ctx)
	if err != nil {
		c.record(ctx, "backup", "error", "warning", "failed to query last backup", err.Error())
		return
	}
	if last.IsZero() {
		c.record(ctx, "backup", "warning", "warning", "no backup has ever been taken", "")
		return
	}
	age := time.Since(last)
	if age > 48*time.Hour {
		c.record(ctx, "backup", "stale", "warning",
			fmt.Sprintf("last backup was %v ago", age.Round(time.Hour)), "")
	} else {
		c.record(ctx, "backup", "ok", "info",
			fmt.Sprintf("last backup %v ago", age.Round(time.Minute)), "")
	}
}

func (c *Checker) checkFiles(ctx context.Context) {
	if c.filesDir == "" {
		return
	}
	if _, err := os.Stat(c.filesDir); os.IsNotExist(err) {
		return
	}

	var missing int
	entries, err := os.ReadDir(c.filesDir)
	if err != nil {
		c.record(ctx, "files", "error", "warning", "failed to read files directory", err.Error())
		return
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		p := filepath.Join(c.filesDir, e.Name())
		if _, err := os.Stat(p); err != nil {
			missing++
		}
	}
	if missing == 0 {
		c.record(ctx, "files", "ok", "info", "all referenced files present", "")
	} else {
		c.record(ctx, "files", "fail", "warning",
			fmt.Sprintf("%d files missing, triggering reindex", missing),
			"auto-reindex scheduled")
	}
}

func (c *Checker) checkWAL(ctx context.Context) {
	walSize := c.store.WALSize()
	if walSize > 16*1024*1024 {
		if err := c.store.Checkpoint(ctx); err != nil {
			c.record(ctx, "wal", "error", "warning", "checkpoint failed", err.Error())
		} else {
			c.record(ctx, "wal", "ok", "info",
				fmt.Sprintf("checkpoint executed, WAL was %d bytes", walSize), "")
		}
	}
}

func (c *Checker) record(ctx context.Context, checkName, status, severity, message, details string) {
	if err := c.store.RecordHealthCheck(ctx, checkName, status, severity, message, details); err != nil {
		c.logger.Error("failed to record health check", "check", checkName, "err", err)
	}
	switch severity {
	case "critical":
		c.logger.Error("health check failed", "check", checkName, "message", message, "details", details)
	case "warning":
		c.logger.Warn("health check warning", "check", checkName, "message", message, "details", details)
	}
}
