package mcpserver

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/aicode/kb-server/internal/kb"
	"github.com/aicode/kb-server/internal/table"
)

type Config struct {
	AllowWrites bool
}

type Server struct {
	mcp    *mcp.Server
	tables *table.Service
	kb     *kb.Service
	logger *slog.Logger
}

func New(tables *table.Service, kbSvc *kb.Service, cfg Config, logger *slog.Logger) *Server {
	s := &Server{
		mcp:    mcp.NewServer(&mcp.Implementation{Name: "kb-server", Version: "0.1.0"}, nil),
		tables: tables,
		kb:     kbSvc,
		logger: logger,
	}
	s.register(cfg.AllowWrites)
	return s
}

// Handler 返回挂在 /mcp 上的 Streamable HTTP 处理器（Stateless 模式，便于简单部署）。
func (s *Server) Handler() http.Handler {
	return mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		return s.mcp
	}, &mcp.StreamableHTTPOptions{Stateless: true})
}

// register 注册全部工具。读工具默认开放；写工具按配置开关。
func (s *Server) register(allowWrites bool) {
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "db_list_tables",
		Description: "列出数据库中所有用户表（不含 SQLite 内部表）。",
	}, func(ctx context.Context, req *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		tables, err := s.tables.ListTables(ctx)
		if err != nil {
			return nil, nil, err
		}
		return nil, map[string]any{"tables": tables}, nil
	})

	type schemaArgs struct {
		Table string `json:"table" jsonschema:"表名"`
	}
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "db_get_schema",
		Description: "查看指定表的建表语句（DDL）。",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args schemaArgs) (*mcp.CallToolResult, any, error) {
		schema, err := s.tables.GetSchema(ctx, args.Table)
		if err != nil {
			return nil, nil, err
		}
		return nil, map[string]any{"table": args.Table, "schema": schema}, nil
	})

	type selectArgs struct {
		Table   string         `json:"table" jsonschema:"表名"`
		Columns []string       `json:"columns,omitempty" jsonschema:"要返回的列，留空返回全部"`
		Where   map[string]any `json:"where,omitempty" jsonschema:"等值过滤条件"`
		OrderBy string         `json:"order_by,omitempty" jsonschema:"排序字段"`
		Limit   int            `json:"limit,omitempty" jsonschema:"返回行数上限，默认 50"`
	}
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "db_select",
		Description: "按条件查询任意表（只读）。默认最多返回 50 行。",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args selectArgs) (*mcp.CallToolResult, any, error) {
		if args.Limit <= 0 {
			args.Limit = 50
		}
		resp, err := s.tables.Execute(ctx, &table.QueryRequest{
			Table:     args.Table,
			Operation: "select",
			Columns:   args.Columns,
			Where:     args.Where,
			OrderBy:   args.OrderBy,
			Limit:     args.Limit,
		})
		if err != nil {
			return nil, nil, err
		}
		return nil, resp, nil
	})

	if allowWrites {
		type insertArgs struct {
			Table  string         `json:"table" jsonschema:"表名"`
			Values map[string]any `json:"values" jsonschema:"要插入的字段与值"`
		}
		mcp.AddTool(s.mcp, &mcp.Tool{
			Name:        "db_insert",
			Description: "向指定表插入一行。",
		}, func(ctx context.Context, req *mcp.CallToolRequest, args insertArgs) (*mcp.CallToolResult, any, error) {
			resp, err := s.tables.Execute(ctx, &table.QueryRequest{
				Table: args.Table, Operation: "insert", Values: args.Values,
			})
			if err != nil {
				return nil, nil, err
			}
			return nil, resp, nil
		})

		type updateArgs struct {
			Table  string         `json:"table" jsonschema:"表名"`
			Values map[string]any `json:"values" jsonschema:"要更新的字段与值"`
			Where  map[string]any `json:"where" jsonschema:"更新条件（必填，防误改全表）"`
		}
		mcp.AddTool(s.mcp, &mcp.Tool{
			Name:        "db_update",
			Description: "按条件更新指定表的行。",
		}, func(ctx context.Context, req *mcp.CallToolRequest, args updateArgs) (*mcp.CallToolResult, any, error) {
			resp, err := s.tables.Execute(ctx, &table.QueryRequest{
				Table: args.Table, Operation: "update", Values: args.Values, Where: args.Where,
			})
			if err != nil {
				return nil, nil, err
			}
			return nil, resp, nil
		})

		type deleteArgs struct {
			Table string         `json:"table" jsonschema:"表名"`
			Where map[string]any `json:"where" jsonschema:"删除条件（必填，防误删全表）"`
		}
		mcp.AddTool(s.mcp, &mcp.Tool{
			Name:        "db_delete",
			Description: "按条件删除指定表的行。",
		}, func(ctx context.Context, req *mcp.CallToolRequest, args deleteArgs) (*mcp.CallToolResult, any, error) {
			resp, err := s.tables.Execute(ctx, &table.QueryRequest{
				Table: args.Table, Operation: "delete", Where: args.Where,
			})
			if err != nil {
				return nil, nil, err
			}
			return nil, resp, nil
		})
	}

	if s.kb != nil {
		type collectionArgs struct{}
		mcp.AddTool(s.mcp, &mcp.Tool{
			Name:        "kb_list_collections",
			Description: "列出知识库的全部集合（工作区）。",
		}, func(ctx context.Context, req *mcp.CallToolRequest, _ collectionArgs) (*mcp.CallToolResult, any, error) {
			cols, err := s.kb.ListCollections(ctx)
			if err != nil {
				return nil, nil, err
			}
			return nil, map[string]any{"collections": cols}, nil
		})

		type searchArgs struct {
			Query         string  `json:"query" jsonschema:"搜索问题或关键词"`
			CollectionIDs []int64 `json:"collection_ids,omitempty" jsonschema:"限定搜索的集合 ID，留空搜索全部"`
			TopK          int     `json:"top_k,omitempty" jsonschema:"返回条数，默认 5"`
			Mode          string  `json:"mode,omitempty" jsonschema:"hybrid 混合（默认）/keyword 关键词/semantic 语义"`
		}
		mcp.AddTool(s.mcp, &mcp.Tool{
			Name:        "kb_search",
			Description: "在知识库中检索：中文全文检索 + 向量语义双路融合（RRF）。",
		}, func(ctx context.Context, req *mcp.CallToolRequest, args searchArgs) (*mcp.CallToolResult, any, error) {
			resp, err := s.kb.Search(ctx, &kb.SearchRequest{
				Query:         args.Query,
				CollectionIDs: args.CollectionIDs,
				TopK:          args.TopK,
				Mode:          args.Mode,
			})
			if err != nil {
				return nil, nil, err
			}
			return nil, resp, nil
		})

		type chatArgs struct {
			Query         string  `json:"query" jsonschema:"要问的问题"`
			CollectionIDs []int64 `json:"collection_ids,omitempty" jsonschema:"限定问答范围"`
		}
		mcp.AddTool(s.mcp, &mcp.Tool{
			Name:        "kb_chat",
			Description: "基于知识库内容回答问题，输出带 [n] 引用标记及引用来源。",
		}, func(ctx context.Context, req *mcp.CallToolRequest, args chatArgs) (*mcp.CallToolResult, any, error) {
			resp, err := s.kb.Chat(ctx, &kb.SearchRequest{
				Query:         args.Query,
				CollectionIDs: args.CollectionIDs,
			})
			if err != nil {
				return nil, nil, err
			}
			return nil, resp, nil
		})
	}

	s.logger.Info("MCP tools registered", "allow_writes", allowWrites, "kb_enabled", s.kb != nil)
}

var _ = fmt.Sprintf
