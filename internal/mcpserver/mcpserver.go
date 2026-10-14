package mcpserver

import (
	"context"
	"encoding/json"
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

// register 注册全部工具。所有 schema 手写为最简兼容格式（单一类型字符串，
// 无 additionalProperties、无联合类型），确保所有 OpenAI 兼容 API 都能接受。
func (s *Server) register(allowWrites bool) {
	tool := func(name, desc, schema string, handler mcp.ToolHandler) {
		s.mcp.AddTool(&mcp.Tool{
			Name:        name,
			Description: desc,
			InputSchema: json.RawMessage(schema),
		}, handler)
	}

	getArgs := func(req *mcp.CallToolRequest) map[string]any {
		if req.Params == nil || len(req.Params.Arguments) == 0 {
			return map[string]any{}
		}
		var m map[string]any
		if err := json.Unmarshal(req.Params.Arguments, &m); err != nil {
			return map[string]any{}
		}
		return m
	}

	getString := func(args map[string]any, key string) string {
		if v, ok := args[key].(string); ok {
			return v
		}
		return ""
	}
	getFloat := func(args map[string]any, key string) float64 {
		if v, ok := args[key].(float64); ok {
			return v
		}
		return 0
	}
	getSlice := func(args map[string]any, key string) []any {
		if v, ok := args[key].([]any); ok {
			return v
		}
		return nil
	}
	getMap := func(args map[string]any, key string) map[string]any {
		if v, ok := args[key].(map[string]any); ok {
			return v
		}
		return nil
	}

	// ── 数据库工具 ──

	tool("db_list_tables", "列出数据库中所有用户表（不含 SQLite 内部表）。",
		`{"type":"object","properties":{}}`,
		func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			tables, err := s.tables.ListTables(ctx)
			if err != nil {
				return nil, err
			}
			return s.jsonResult(map[string]any{"tables": tables})
		})

	tool("db_get_schema", "查看指定表的建表语句（DDL）。",
		`{"type":"object","properties":{"table":{"type":"string","description":"表名"}},"required":["table"]}`,
		func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			args := getArgs(req)
			schema, err := s.tables.GetSchema(ctx, getString(args, "table"))
			if err != nil {
				return nil, err
			}
			return s.jsonResult(map[string]any{"table": getString(args, "table"), "schema": schema})
		})

	tool("db_select", "按条件查询任意表（只读）。默认最多返回 50 行。",
		`{"type":"object","properties":{"table":{"type":"string","description":"表名"},"columns":{"type":"array","items":{"type":"string"},"description":"要返回的列，留空返回全部"},"where":{"type":"object","description":"等值过滤条件"},"order_by":{"type":"string","description":"排序字段"},"limit":{"type":"integer","description":"返回行数上限，默认 50"}},"required":["table"]}`,
		func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			args := getArgs(req)
			limit := int(getFloat(args, "limit"))
			if limit <= 0 {
				limit = 50
			}
			var columns []string
			for _, c := range getSlice(args, "columns") {
				if s, ok := c.(string); ok {
					columns = append(columns, s)
				}
			}
			resp, err := s.tables.Execute(ctx, &table.QueryRequest{
				Table:     getString(args, "table"),
				Operation: "select",
				Columns:   columns,
				Where:     getMap(args, "where"),
				OrderBy:   getString(args, "order_by"),
				Limit:     limit,
			})
			if err != nil {
				return nil, err
			}
			return s.jsonResult(resp)
		})

	if allowWrites {
		tool("db_insert", "向指定表插入一行。",
			`{"type":"object","properties":{"table":{"type":"string","description":"表名"},"values":{"type":"object","description":"字段与值"}},"required":["table","values"]}`,
			func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				args := getArgs(req)
				resp, err := s.tables.Execute(ctx, &table.QueryRequest{
					Table: getString(args, "table"), Operation: "insert", Values: getMap(args, "values"),
				})
				if err != nil {
					return nil, err
				}
				return s.jsonResult(resp)
			})

		tool("db_update", "按条件更新指定表的行。",
			`{"type":"object","properties":{"table":{"type":"string","description":"表名"},"values":{"type":"object","description":"新值"},"where":{"type":"object","description":"条件（必填防误改）"}},"required":["table","values","where"]}`,
			func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				args := getArgs(req)
				resp, err := s.tables.Execute(ctx, &table.QueryRequest{
					Table: getString(args, "table"), Operation: "update", Values: getMap(args, "values"), Where: getMap(args, "where"),
				})
				if err != nil {
					return nil, err
				}
				return s.jsonResult(resp)
			})

		tool("db_delete", "按条件删除指定表的行。",
			`{"type":"object","properties":{"table":{"type":"string","description":"表名"},"where":{"type":"object","description":"条件（必填防误删）"}},"required":["table","where"]}`,
			func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				args := getArgs(req)
				resp, err := s.tables.Execute(ctx, &table.QueryRequest{
					Table: getString(args, "table"), Operation: "delete", Where: getMap(args, "where"),
				})
				if err != nil {
					return nil, err
				}
				return s.jsonResult(resp)
			})
	}

	// ── 知识库工具 ──

	if s.kb != nil {
		tool("kb_list_collections", "列出知识库的全部集合（工作区）。",
			`{"type":"object","properties":{}}`,
			func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				cols, err := s.kb.ListCollections(ctx)
				if err != nil {
					return nil, err
				}
				return s.jsonResult(map[string]any{"collections": cols})
			})

		tool("kb_search", "在知识库中检索：中文全文检索 + 向量语义双路融合（RRF）。",
			`{"type":"object","properties":{"query":{"type":"string","description":"搜索问题或关键词"},"collection_ids":{"type":"array","items":{"type":"integer"},"description":"限定集合 ID，留空搜全部"},"top_k":{"type":"integer","description":"返回条数，默认 5"},"mode":{"type":"string","description":"hybrid 混合（默认）/keyword 关键词/semantic 语义"}},"required":["query"]}`,
			func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				args := getArgs(req)
				var collectionIDs []int64
				for _, v := range getSlice(args, "collection_ids") {
					if f, ok := v.(float64); ok {
						collectionIDs = append(collectionIDs, int64(f))
					}
				}
				resp, err := s.kb.Search(ctx, &kb.SearchRequest{
					Query:         getString(args, "query"),
					CollectionIDs: collectionIDs,
					TopK:          int(getFloat(args, "top_k")),
					Mode:          getString(args, "mode"),
				})
				if err != nil {
					return nil, err
				}
				return s.jsonResult(resp)
			})

		tool("kb_chat", "基于知识库内容回答问题，输出带 [n] 引用标记及引用来源。",
			`{"type":"object","properties":{"query":{"type":"string","description":"要问的问题"},"collection_ids":{"type":"array","items":{"type":"integer"},"description":"限定问答范围"}},"required":["query"]}`,
			func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				args := getArgs(req)
				var collectionIDs []int64
				for _, v := range getSlice(args, "collection_ids") {
					if f, ok := v.(float64); ok {
						collectionIDs = append(collectionIDs, int64(f))
					}
				}
				resp, err := s.kb.Chat(ctx, &kb.SearchRequest{
					Query:         getString(args, "query"),
					CollectionIDs: collectionIDs,
				})
				if err != nil {
					return nil, err
				}
				return s.jsonResult(resp)
			})
	}

	s.logger.Info("MCP tools registered (explicit schemas)", "allow_writes", allowWrites, "kb_enabled", s.kb != nil)
}

// jsonResult 把任意值序列化为 MCP CallToolResult 的文本内容。
func (s *Server) jsonResult(v any) (*mcp.CallToolResult, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: string(b)}},
	}, nil
}

var _ = fmt.Sprintf
