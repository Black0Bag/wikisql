package table

import (
	"context"
	"fmt"
	"strings"

	"github.com/Black0Bag/wikisql/internal/store"
)

type Service struct {
	store *store.Store
}

// ValidationError 表示请求参数不符合白名单或格式要求，HTTP 层应映射为 400。
type ValidationError struct{ msg string }

func (e *ValidationError) Error() string { return e.msg }

func validationErrf(format string, args ...any) error {
	return &ValidationError{msg: fmt.Sprintf(format, args...)}
}

// NewValidationError 供其他包构造校验错误（HTTP 层会映射为 400）。
func NewValidationError(msg string) error {
	return &ValidationError{msg: msg}
}

func New(s *store.Store) *Service {
	return &Service{store: s}
}

type QueryRequest struct {
	Table       string         `json:"table"`
	Operation   string         `json:"operation"`
	Columns     []string       `json:"columns,omitempty"`
	Values      map[string]any `json:"values,omitempty"`
	Where       map[string]any `json:"where,omitempty"`
	OrderBy     string         `json:"order_by,omitempty"`
	Limit       int            `json:"limit,omitempty"`
	Offset      int            `json:"offset,omitempty"`
}

type QueryResponse struct {
	Table   string        `json:"table"`
	Columns []string      `json:"columns,omitempty"`
	Rows    []map[string]any `json:"rows,omitempty"`
	RowsAffected int64    `json:"rows_affected,omitempty"`
	LastInsertID int64    `json:"last_insert_id,omitempty"`
}

func (svc *Service) Execute(ctx context.Context, req *QueryRequest) (*QueryResponse, error) {
	if err := validateTableName(req.Table); err != nil {
		return nil, err
	}

	exists, err := svc.store.TableExists(ctx, req.Table)
	if err != nil {
		return nil, fmt.Errorf("checking table existence: %w", err)
	}
	if !exists {
		return nil, fmt.Errorf("table %q does not exist", req.Table)
	}

	switch strings.ToLower(req.Operation) {
	case "select":
		return svc.doSelect(ctx, req)
	case "insert":
		return svc.doInsert(ctx, req)
	case "update":
		return svc.doUpdate(ctx, req)
	case "delete":
		return svc.doDelete(ctx, req)
	default:
		return nil, fmt.Errorf("unknown operation %q; expected select/insert/update/delete", req.Operation)
	}
}

func (svc *Service) ListTables(ctx context.Context) ([]string, error) {
	return svc.store.ListTables(ctx)
}

func (svc *Service) GetSchema(ctx context.Context, tableName string) (string, error) {
	if err := validateTableName(tableName); err != nil {
		return "", err
	}
	return svc.store.TableSchema(ctx, tableName)
}

func (svc *Service) doSelect(ctx context.Context, req *QueryRequest) (*QueryResponse, error) {
	for _, col := range req.Columns {
		if err := validateIdentifier(col); err != nil {
			return nil, fmt.Errorf("column %q: %w", col, err)
		}
	}

	cols := req.Columns
	if len(cols) == 0 {
		cols = []string{"*"}
	}

	query := fmt.Sprintf("SELECT %s FROM \"%s\"", strings.Join(cols, ", "), req.Table)

	var args []any
	if len(req.Where) > 0 {
		for k := range req.Where {
			if err := validateIdentifier(k); err != nil {
				return nil, fmt.Errorf("where column %q: %w", k, err)
			}
		}
		var clauses []string
		for k, v := range req.Where {
			clauses = append(clauses, fmt.Sprintf("\"%s\" = ?", k))
			args = append(args, v)
		}
		query += " WHERE " + strings.Join(clauses, " AND ")
	}

	if req.OrderBy != "" {
		if err := validateIdentifier(req.OrderBy); err != nil {
			return nil, fmt.Errorf("order_by %q: %w", req.OrderBy, err)
		}
		query += fmt.Sprintf(" ORDER BY \"%s\"", req.OrderBy)
	}

	if req.Limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", req.Limit)
		if req.Offset > 0 {
			query += fmt.Sprintf(" OFFSET %d", req.Offset)
		}
	}

	rows, err := svc.store.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	defer rows.Close()

	colNames, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("getting column names: %w", err)
	}

	var result []map[string]any
	for rows.Next() {
		values := make([]any, len(colNames))
		valuePtrs := make([]any, len(colNames))
		for i := range values {
			valuePtrs[i] = &values[i]
		}
		if err := rows.Scan(valuePtrs...); err != nil {
			return nil, fmt.Errorf("scanning row: %w", err)
		}
		row := make(map[string]any, len(colNames))
		for i, name := range colNames {
			row[name] = values[i]
		}
		result = append(result, row)
	}

	return &QueryResponse{
		Table:   req.Table,
		Columns: colNames,
		Rows:    result,
	}, nil
}

func (svc *Service) doInsert(ctx context.Context, req *QueryRequest) (*QueryResponse, error) {
	if len(req.Values) == 0 {
		return nil, fmt.Errorf("insert requires values")
	}

	for k := range req.Values {
		if err := validateIdentifier(k); err != nil {
			return nil, fmt.Errorf("insert column %q: %w", k, err)
		}
	}

	var cols []string
	var placeholders []string
	var args []any
	for k, v := range req.Values {
		cols = append(cols, fmt.Sprintf("\"%s\"", k))
		placeholders = append(placeholders, "?")
		args = append(args, v)
	}

	query := fmt.Sprintf("INSERT INTO \"%s\" (%s) VALUES (%s)",
		req.Table, strings.Join(cols, ", "), strings.Join(placeholders, ", "))

	res, err := svc.store.ExecContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("insert: %w", err)
	}

	id, _ := res.LastInsertId()
	affected, _ := res.RowsAffected()

	return &QueryResponse{
		Table:        req.Table,
		LastInsertID: id,
		RowsAffected: affected,
	}, nil
}

func (svc *Service) doUpdate(ctx context.Context, req *QueryRequest) (*QueryResponse, error) {
	if len(req.Values) == 0 {
		return nil, fmt.Errorf("update requires values")
	}
	if len(req.Where) == 0 {
		return nil, fmt.Errorf("update requires where clause to prevent accidental mass updates")
	}

	for k := range req.Values {
		if err := validateIdentifier(k); err != nil {
			return nil, fmt.Errorf("update column %q: %w", k, err)
		}
	}
	for k := range req.Where {
		if err := validateIdentifier(k); err != nil {
			return nil, fmt.Errorf("where column %q: %w", k, err)
		}
	}

	var setClauses []string
	var args []any
	for k, v := range req.Values {
		setClauses = append(setClauses, fmt.Sprintf("\"%s\" = ?", k))
		args = append(args, v)
	}

	var whereClauses []string
	for k, v := range req.Where {
		whereClauses = append(whereClauses, fmt.Sprintf("\"%s\" = ?", k))
		args = append(args, v)
	}

	query := fmt.Sprintf("UPDATE \"%s\" SET %s WHERE %s",
		req.Table, strings.Join(setClauses, ", "), strings.Join(whereClauses, " AND "))

	res, err := svc.store.ExecContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("update: %w", err)
	}

	affected, _ := res.RowsAffected()
	return &QueryResponse{
		Table:        req.Table,
		RowsAffected: affected,
	}, nil
}

func (svc *Service) doDelete(ctx context.Context, req *QueryRequest) (*QueryResponse, error) {
	if len(req.Where) == 0 {
		return nil, fmt.Errorf("delete requires where clause to prevent accidental mass deletes")
	}

	for k := range req.Where {
		if err := validateIdentifier(k); err != nil {
			return nil, fmt.Errorf("where column %q: %w", k, err)
		}
	}

	var whereClauses []string
	var args []any
	for k, v := range req.Where {
		whereClauses = append(whereClauses, fmt.Sprintf("\"%s\" = ?", k))
		args = append(args, v)
	}

	query := fmt.Sprintf("DELETE FROM \"%s\" WHERE %s",
		req.Table, strings.Join(whereClauses, " AND "))

	res, err := svc.store.ExecContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("delete: %w", err)
	}

	affected, _ := res.RowsAffected()
	return &QueryResponse{
		Table:        req.Table,
		RowsAffected: affected,
	}, nil
}

func validateTableName(name string) error {
	if name == "" {
		return validationErrf("table name must not be empty")
	}
	return validateIdentifier(name)
}

func validateIdentifier(name string) error {
	if name == "" {
		return validationErrf("identifier must not be empty")
	}
	for _, r := range name {
		if !(r == '_' || r == '.' ||
			(r >= 'a' && r <= 'z') ||
			(r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9')) {
			return validationErrf("identifier %q contains invalid character %q; only alphanumeric, underscore and dot allowed", name, r)
		}
	}
	return nil
}
