package table

import (
	"context"
	"fmt"
	"strings"
)

// ColumnDef 结构化列定义（建表/加列用），不接收裸 SQL 杜绝注入面。
type ColumnDef struct {
	Name       string `json:"name"`
	Type       string `json:"type"`
	PrimaryKey bool   `json:"primary_key,omitempty"`
	NotNull    bool   `json:"not_null,omitempty"`
	Unique     bool   `json:"unique,omitempty"`
	Default    any    `json:"default,omitempty"`
}

type CreateTableRequest struct {
	Name    string     `json:"name"`
	Columns []ColumnDef `json:"columns"`
}

type CreateTableResponse struct {
	Table   string `json:"table"`
	Created bool   `json:"created"`
}

type AddColumnRequest struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	NotNull  bool   `json:"not_null,omitempty"`
	Default  any    `json:"default,omitempty"`
}

type AddColumnResponse struct {
	Table  string `json:"table"`
	Column string `json:"column"`
	Added  bool   `json:"added"`
}

type TruncateResponse struct {
	Table        string `json:"table"`
	RowsAffected int64  `json:"rows_affected"`
}

var allowedTypes = map[string]bool{
	"INTEGER": true, "TEXT": true, "REAL": true, "BLOB": true,
}

func (svc *Service) checkDDL() error {
	if !svc.allowDDL {
		return NewValidationError("DDL 操作未启用（tables.allow_ddl=false）")
	}
	return nil
}

// CreateTable 创建表（幂等）。已存在时什么都不做，返回 created=false。
func (svc *Service) CreateTable(ctx context.Context, req *CreateTableRequest) (*CreateTableResponse, error) {
	if err := svc.checkDDL(); err != nil {
		return nil, err
	}
	if err := validateTableName(req.Name); err != nil {
		return nil, err
	}
	if len(req.Columns) == 0 {
		return nil, NewValidationError("columns must not be empty")
	}
	// DDL 是幂等的，先查表是否存在——不存在才能建
	// 但如果已存在但列定义不同，我们不报错也不比对（webgo 首次连接会建，后续重复调用无害）
	// 已存在 → 直接返回 created=false
	exists, err := svc.store.TableExists(ctx, req.Name)
	if err != nil {
		return nil, fmt.Errorf("checking table existence: %w", err)
	}
	if exists {
		return &CreateTableResponse{Table: req.Name, Created: false}, nil
	}

	// 生成 DDL
	var sb strings.Builder
	fmt.Fprintf(&sb, "CREATE TABLE IF NOT EXISTS %q (", req.Name)
	for i, col := range req.Columns {
		if i > 0 {
			sb.WriteString(", ")
		}
		if err := validateIdentifier(col.Name); err != nil {
			return nil, fmt.Errorf("column %q: %w", col.Name, err)
		}
		colType := strings.ToUpper(col.Type)
		if !allowedTypes[colType] {
			return nil, NewValidationError(fmt.Sprintf("column %q type %q not allowed; use INTEGER/TEXT/REAL/BLOB", col.Name, col.Type))
		}
		sb.WriteString(fmt.Sprintf("%q %s", col.Name, colType))
		if col.PrimaryKey {
			sb.WriteString(" PRIMARY KEY")
		}
		if col.NotNull && !col.PrimaryKey {
			sb.WriteString(" NOT NULL")
		}
		if col.Unique {
			sb.WriteString(" UNIQUE")
		}
		if col.Default != nil {
			switch v := col.Default.(type) {
			case string:
				sb.WriteString(fmt.Sprintf(" DEFAULT %q", v))
			case float64:
				sb.WriteString(fmt.Sprintf(" DEFAULT %v", int64(v)))
			default:
				return nil, NewValidationError(fmt.Sprintf("column %q default must be string or number", col.Name))
			}
		}
	}
	sb.WriteString(")")

	if _, err := svc.store.ExecContext(ctx, sb.String()); err != nil {
		return nil, fmt.Errorf("creating table: %w", err)
	}
	return &CreateTableResponse{Table: req.Name, Created: true}, nil
}

// AddColumn 给表加列（幂等）。列已存在时什么都不做，返回 added=false。
func (svc *Service) AddColumn(ctx context.Context, tableName string, req *AddColumnRequest) (*AddColumnResponse, error) {
	if err := svc.checkDDL(); err != nil {
		return nil, err
	}
	if err := validateTableName(tableName); err != nil {
		return nil, err
	}
	if err := validateIdentifier(req.Name); err != nil {
		return nil, fmt.Errorf("column %q: %w", req.Name, err)
	}
	colType := strings.ToUpper(req.Type)
	if !allowedTypes[colType] {
		return nil, NewValidationError(fmt.Sprintf("type %q not allowed; use INTEGER/TEXT/REAL/BLOB", req.Type))
	}
	// SQLite 限制：加 NOT NULL 列必须带 DEFAULT
	if req.NotNull && req.Default == nil {
		return nil, NewValidationError("NOT NULL column requires DEFAULT (SQLite ALTER TABLE restriction)")
	}

	exists, err := svc.store.TableExists(ctx, tableName)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, fmt.Errorf("table %q does not exist", tableName)
	}
	// 检查列是否已存在
	columnExists, err := svc.columnExists(ctx, tableName, req.Name)
	if err != nil {
		return nil, err
	}
	if columnExists {
		return &AddColumnResponse{Table: tableName, Column: req.Name, Added: false}, nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "ALTER TABLE %q ADD COLUMN %q %s", tableName, req.Name, colType)
	if req.NotNull {
		sb.WriteString(" NOT NULL")
	}
	if req.Default != nil {
		switch v := req.Default.(type) {
		case string:
			fmt.Fprintf(&sb, " DEFAULT %q", v)
		case float64:
			fmt.Fprintf(&sb, " DEFAULT %v", int64(v))
		default:
			return nil, NewValidationError("default must be string or number")
		}
	}

	if _, err := svc.store.ExecContext(ctx, sb.String()); err != nil {
		return nil, fmt.Errorf("adding column: %w", err)
	}
	return &AddColumnResponse{Table: tableName, Column: req.Name, Added: true}, nil
}

// Truncate 清空表全部行。
func (svc *Service) Truncate(ctx context.Context, tableName string) (*TruncateResponse, error) {
	if err := svc.checkDDL(); err != nil {
		return nil, err
	}
	if err := validateTableName(tableName); err != nil {
		return nil, err
	}
	exists, err := svc.store.TableExists(ctx, tableName)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, fmt.Errorf("table %q does not exist", tableName)
	}
	res, err := svc.store.ExecContext(ctx, fmt.Sprintf("DELETE FROM %q", tableName))
	if err != nil {
		return nil, fmt.Errorf("truncating table: %w", err)
	}
	affected, _ := res.RowsAffected()
	return &TruncateResponse{Table: tableName, RowsAffected: affected}, nil
}

// columnExists 查看指定表的列是否存在。
func (svc *Service) columnExists(ctx context.Context, tableName, columnName string) (bool, error) {
	schema, err := svc.store.TableSchema(ctx, tableName)
	if err != nil {
		return false, err
	}
	return strings.Contains(schema, fmt.Sprintf("%q", columnName)), nil
}
