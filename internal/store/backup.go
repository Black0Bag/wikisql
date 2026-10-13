package store

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

func (s *Store) Backup(ctx context.Context, backupDir string) (string, error) {
	if err := mkdirAll(backupDir); err != nil {
		return "", fmt.Errorf("creating backup directory: %w", err)
	}

	ts := time.Now().Format("20060102-150405")
	backupPath := filepath.Join(backupDir, fmt.Sprintf("backup-%s.db", ts))

	_, err := s.writer.ExecContext(ctx, fmt.Sprintf("VACUUM INTO '%s'", backupPath))
	if err != nil {
		return "", fmt.Errorf("backup via VACUUM INTO: %w", err)
	}

	var size int64
	if fi, e := os.Stat(backupPath); e == nil {
		size = fi.Size()
	}

	s.logger.Info("backup completed", "path", backupPath, "size_bytes", size)

	if _, err := s.writer.ExecContext(ctx,
		"INSERT INTO audit_log (action, details) VALUES (?, ?)",
		"backup", backupPath,
	); err != nil {
		s.logger.Error("failed to record backup in audit log", "err", err)
	}

	return backupPath, nil
}

func (s *Store) LastBackupTime(ctx context.Context) (time.Time, error) {
	row := s.reader.QueryRowContext(ctx,
		"SELECT created_at FROM audit_log WHERE action = 'backup' ORDER BY id DESC LIMIT 1",
	)
	var ts string
	if err := row.Scan(&ts); err != nil {
		return time.Time{}, nil
	}
	t, err := time.Parse("2006-01-02 15:04:05", ts)
	if err != nil {
		return time.Time{}, nil
	}
	return t, nil
}

func (s *Store) IntegrityCheck(ctx context.Context) (string, error) {
	var result string
	err := s.reader.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&result)
	return result, err
}

func (s *Store) ForeignKeyCheck(ctx context.Context) ([]string, error) {
	rows, err := s.reader.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var violations []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		violations = append(violations, v)
	}
	return violations, nil
}

func (s *Store) DBSize(ctx context.Context) (int64, error) {
	var pageCount, pageSize int64
	if err := s.reader.QueryRowContext(ctx, "PRAGMA page_count").Scan(&pageCount); err != nil {
		return 0, err
	}
	if err := s.reader.QueryRowContext(ctx, "PRAGMA page_size").Scan(&pageSize); err != nil {
		return 0, err
	}
	return pageCount * pageSize, nil
}

func (s *Store) WALSize() int64 {
	walPath := s.dbPath + "-wal"
	if fi, err := os.Stat(walPath); err == nil {
		return fi.Size()
	}
	return 0
}

func (s *Store) Checkpoint(ctx context.Context) error {
	_, err := s.writer.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)")
	return err
}

func (s *Store) RecordHealthCheck(ctx context.Context, checkName, status, severity, message, details string) error {
	_, err := s.writer.ExecContext(ctx,
		`INSERT INTO health_checks (check_name, status, severity, message, details) VALUES (?, ?, ?, ?, ?)`,
		checkName, status, severity, message, details,
	)
	return err
}

func (s *Store) ListTables(ctx context.Context) ([]string, error) {
	rows, err := s.reader.QueryContext(ctx,
		`SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		tables = append(tables, name)
	}
	return tables, nil
}

func (s *Store) TableSchema(ctx context.Context, tableName string) (string, error) {
	var schema string
	err := s.reader.QueryRowContext(ctx,
		`SELECT sql FROM sqlite_master WHERE type = 'table' AND name = ?`, tableName,
	).Scan(&schema)
	return schema, err
}

func (s *Store) TableExists(ctx context.Context, tableName string) (bool, error) {
	var count int
	err := s.reader.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, tableName,
	).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

func (s *Store) TableRowCount(ctx context.Context, tableName string) (int64, error) {
	var count int64
	err := s.reader.QueryRowContext(ctx,
		fmt.Sprintf("SELECT COUNT(*) FROM \"%s\"", tableName),
	).Scan(&count)
	return count, err
}
