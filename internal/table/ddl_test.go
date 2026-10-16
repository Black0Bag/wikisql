package table

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/Black0Bag/wikisql/internal/store"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func testServiceDDL(t *testing.T, allowDDL bool) (*Service, *store.Store) {
	t.Helper()
	dir := t.TempDir()
	s, err := store.New(filepath.Join(dir, "test.db"), testLogger())
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return New(s, allowDDL), s
}

func TestCreateTableAndIdempotent(t *testing.T) {
	svc, st := testServiceDDL(t, true)
	ctx := context.Background()

	req := &CreateTableRequest{
		Name: "test_posts",
		Columns: []ColumnDef{
			{Name: "id", Type: "INTEGER", PrimaryKey: true},
			{Name: "slug", Type: "TEXT", NotNull: true, Unique: true},
			{Name: "title", Type: "TEXT", NotNull: true},
			{Name: "draft", Type: "INTEGER", NotNull: true, Default: float64(0)},
		},
	}
	resp, err := svc.CreateTable(ctx, req)
	if err != nil {
		t.Fatalf("CreateTable: %v", err)
	}
	if !resp.Created || resp.Table != "test_posts" {
		t.Errorf("expected created=true test_posts, got %+v", resp)
	}
	// 验证表存在且 schema 正确
	exists, _ := st.TableExists(ctx, "test_posts")
	if !exists {
		t.Fatal("table should exist")
	}
	schema, _ := st.TableSchema(ctx, "test_posts")
	if schema == "" || !contains(schema, "PRIMARY KEY") || !contains(schema, "UNIQUE") {
		t.Errorf("unexpected schema: %s", schema)
	}

	// 幂等：再建一次应该什么都不做
	resp2, err := svc.CreateTable(ctx, req)
	if err != nil {
		t.Fatalf("CreateTable idempotent: %v", err)
	}
	if resp2.Created {
		t.Error("second CreateTable should return created=false")
	}
}

func TestCreateTableDDLOff(t *testing.T) {
	svc, _ := testServiceDDL(t, false)
	_, err := svc.CreateTable(context.Background(), &CreateTableRequest{
		Name:    "should_fail",
		Columns: []ColumnDef{{Name: "id", Type: "INTEGER", PrimaryKey: true}},
	})
	if err == nil {
		t.Fatal("expected error with allowDDL=false")
	}
	// 错误应该是 ValidationError（HTTP 400）
	var ve *ValidationError
	if !asValidationError(err, &ve) {
		t.Errorf("expected ValidationError, got %T: %v", err, err)
	}
}

func TestCreateTableValidation(t *testing.T) {
	svc, _ := testServiceDDL(t, true)
	ctx := context.Background()

	// 非法表名
	_, err := svc.CreateTable(ctx, &CreateTableRequest{
		Name:    "bad;DROP",
		Columns: []ColumnDef{{Name: "id", Type: "INTEGER", PrimaryKey: true}},
	})
	if err == nil {
		t.Error("bad table name should fail")
	}

	// 非法列类型
	_, err = svc.CreateTable(ctx, &CreateTableRequest{
		Name:    "ok_table",
		Columns: []ColumnDef{{Name: "id", Type: "INT", PrimaryKey: true}},
	})
	if err == nil {
		t.Error("bad column type should fail")
	}

	// 非法默认值类型
	_, err = svc.CreateTable(ctx, &CreateTableRequest{
		Name:    "ok_table2",
		Columns: []ColumnDef{{Name: "id", Type: "INTEGER", PrimaryKey: true}, {Name: "x", Type: "TEXT", Default: []string{"not", "valid"}}},
	})
	if err == nil {
		t.Error("slice default should fail")
	}
}

func TestAddColumnAndIdempotent(t *testing.T) {
	svc, st := testServiceDDL(t, true)
	ctx := context.Background()

	// 先建表
	svc.CreateTable(ctx, &CreateTableRequest{
		Name:    "test_addcol",
		Columns: []ColumnDef{{Name: "id", Type: "INTEGER", PrimaryKey: true}},
	})
	// 加列
	resp, err := svc.AddColumn(ctx, "test_addcol", &AddColumnRequest{
		Name: "tags", Type: "TEXT", NotNull: true, Default: "",
	})
	if err != nil {
		t.Fatalf("AddColumn: %v", err)
	}
	if !resp.Added {
		t.Error("expected added=true")
	}
	// 幂等
	resp2, err := svc.AddColumn(ctx, "test_addcol", &AddColumnRequest{
		Name: "tags", Type: "TEXT", NotNull: true, Default: "",
	})
	if err != nil {
		t.Fatalf("AddColumn idempotent: %v", err)
	}
	if resp2.Added {
		t.Error("second AddColumn should return added=false")
	}
	// 验证 schema
	schema, _ := st.TableSchema(ctx, "test_addcol")
	if !contains(schema, "tags") {
		t.Error("schema should contain tags column")
	}

	// 不存在的表
	_, err = svc.AddColumn(ctx, "nonexistent", &AddColumnRequest{Name: "x", Type: "TEXT"})
	if err == nil {
		t.Error("nonexistent table should fail")
	}
	// NOT NULL 无 DEFAULT
	_, err = svc.AddColumn(ctx, "test_addcol", &AddColumnRequest{Name: "y", Type: "TEXT", NotNull: true})
	if err == nil {
		t.Error("NOT NULL without DEFAULT should fail")
	}
}

func TestTruncate(t *testing.T) {
	svc, _ := testServiceDDL(t, true)
	ctx := context.Background()

	svc.CreateTable(ctx, &CreateTableRequest{
		Name:    "test_trunc",
		Columns: []ColumnDef{{Name: "id", Type: "INTEGER", PrimaryKey: true}},
	})
	// 插入 3 行
	for i := 0; i < 3; i++ {
		svc.Execute(ctx, &QueryRequest{Table: "test_trunc", Operation: "insert", Values: map[string]any{"id": float64(i)}})
	}
	// 清空
	resp, err := svc.Truncate(ctx, "test_trunc")
	if err != nil {
		t.Fatalf("Truncate: %v", err)
	}
	if resp.RowsAffected != 3 {
		t.Errorf("expected 3 affected, got %d", resp.RowsAffected)
	}
	// 验证空：select 应无行
	empty, err := svc.Execute(ctx, &QueryRequest{Table: "test_trunc", Operation: "select"})
	if err != nil {
		t.Fatalf("verify empty: %v", err)
	}
	if len(empty.Rows) != 0 {
		t.Errorf("table should be empty, got %d rows", len(empty.Rows))
	}
}

func TestOrderDesc(t *testing.T) {
	svc, _ := testServiceDDL(t, true)
	ctx := context.Background()

	svc.CreateTable(ctx, &CreateTableRequest{
		Name:    "test_desc",
		Columns: []ColumnDef{{Name: "id", Type: "INTEGER", PrimaryKey: true}},
	})
	for i := 1; i <= 5; i++ {
		svc.Execute(ctx, &QueryRequest{Table: "test_desc", Operation: "insert", Values: map[string]any{"id": float64(i)}})
	}
	// 降序
	resp, err := svc.Execute(ctx, &QueryRequest{Table: "test_desc", Operation: "select", OrderBy: "id", OrderDesc: true, Limit: 1})
	if err != nil {
		t.Fatalf("select desc: %v", err)
	}
	if len(resp.Rows) != 1 || toInt(resp.Rows[0]["id"]) != 5 {
		t.Errorf("expected id=5 first (desc), got %+v", resp.Rows)
	}
	// 升序（默认）
	resp2, _ := svc.Execute(ctx, &QueryRequest{Table: "test_desc", Operation: "select", OrderBy: "id", Limit: 1})
	if len(resp2.Rows) != 1 || toInt(resp2.Rows[0]["id"]) != 1 {
		t.Errorf("expected id=1 first (asc), got %+v", resp2.Rows)
	}
}

func contains(s, sub string) bool { return len(s) >= len(sub) && (s == sub || len(s) > 0 && stringsContains(s, sub)) }
func stringsContains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
func asValidationError(err error, target **ValidationError) bool {
	if ve, ok := err.(*ValidationError); ok {
		*target = ve
		return true
	}
	return false
}

func toInt(v any) int {
	switch n := v.(type) {
	case int64:
		return int(n)
	case float64:
		return int(n)
	case int:
		return n
	}
	return -1
}
