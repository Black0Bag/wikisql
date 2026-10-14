package store

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Backup 执行在线备份。backupDir 为相对路径时基于数据库文件所在目录解析
//（而非进程 cwd，防止 init.d/procd 启动时备份散落到根目录）。
// 若备份目录中最近一次备份距现在不足 minBackupInterval，则跳过并返回已有路径。
func (s *Store) Backup(ctx context.Context, backupDir string) (string, error) {
	backupDir = s.resolveBackupDir(backupDir)
	if err := mkdirAll(backupDir); err != nil {
		return "", fmt.Errorf("creating backup directory: %w", err)
	}

	// 去重：防止重启风暴导致备份泛滥
	if recent, err := s.findRecentBackup(backupDir, minBackupInterval); err == nil && recent != "" {
		s.logger.Info("backup skipped, recent backup exists", "path", recent)
		return recent, nil
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

const minBackupInterval = 10 * time.Minute

// resolveBackupDir 将相对路径解析为数据库文件所在目录下的路径。
// 防止 cwd 不对时备份散落到根目录 /data。
func (s *Store) resolveBackupDir(backupDir string) string {
	if filepath.IsAbs(backupDir) {
		return backupDir
	}
	// 相对路径：基于数据库文件所在目录解析
	dbDir := filepath.Dir(s.dbPath)
	return filepath.Join(dbDir, backupDir)
}

// findRecentBackup 检查备份目录中是否有指定时间内的备份文件。
func (s *Store) findRecentBackup(backupDir string, within time.Duration) (string, error) {
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		return "", err
	}
	cutoff := time.Now().Add(-within)
	var newest string
	var newestTime time.Time
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), "backup-") || !strings.HasSuffix(e.Name(), ".db") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().After(cutoff) && info.ModTime().After(newestTime) {
			newest = filepath.Join(backupDir, e.Name())
			newestTime = info.ModTime()
		}
	}
	return newest, nil
}

// PruneBackups 保留最近 keep 份备份，删除更旧的。防止备份无限增长撑满磁盘。
func (s *Store) PruneBackups(backupDir string, keep int) (removed int, err error) {
	backupDir = s.resolveBackupDir(backupDir)
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		return 0, fmt.Errorf("reading backup directory: %w", err)
	}

	type backupFile struct {
		path    string
		modTime time.Time
	}
	var files []backupFile
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), "backup-") || !strings.HasSuffix(e.Name(), ".db") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		files = append(files, backupFile{filepath.Join(backupDir, e.Name()), info.ModTime()})
	}

	if len(files) <= keep {
		return 0, nil
	}

	sort.Slice(files, func(i, j int) bool { return files[i].modTime.After(files[j].modTime) })
	for _, f := range files[keep:] {
		if err := os.Remove(f.path); err != nil {
			s.logger.Warn("failed to remove old backup", "path", f.path, "err", err)
			continue
		}
		removed++
	}
	if removed > 0 {
		s.logger.Info("pruned old backups", "removed", removed, "kept", keep)
	}
	return removed, nil
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
		`SELECT name, COALESCE(sql, '') FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	// 先收集全部表和虚拟表名，再过滤虚拟表的影子表。
	// FTS5 影子表（_data/_idx/_content/_docsize/_config）与 vec0 影子表
	// （_chunks/_rowids/_info/_vector_chunks*）是实现细节，不应暴露给使用者。
	type tableInfo struct {
		name      string
		virtualID string // 所属虚拟表名（影子表）或自身名（虚拟表/普通表）
	}
	var infos []tableInfo
	var vtabNames []string
	for rows.Next() {
		var name, sql string
		if err := rows.Scan(&name, &sql); err != nil {
			return nil, err
		}
		if strings.Contains(strings.ToUpper(sql), "VIRTUAL TABLE") {
			vtabNames = append(vtabNames, name)
			infos = append(infos, tableInfo{name: name, virtualID: name})
		} else {
			infos = append(infos, tableInfo{name: name})
		}
	}

	var tables []string
	for _, info := range infos {
		if info.virtualID != "" {
			tables = append(tables, info.name)
			continue
		}
		isShadow := false
		for _, v := range vtabNames {
			if strings.HasPrefix(info.name, v+"_") {
				isShadow = true
				break
			}
		}
		if !isShadow {
			tables = append(tables, info.name)
		}
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
