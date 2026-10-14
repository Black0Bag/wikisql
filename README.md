# wikisql

个人自托管的一体化数据底座：**SQLite 数据库服务 + 知识库**，单进程、单文件、零外部依赖。

两个功能深度绑定在同一进程、同一份数据文件里：
数据库是知识库的索引底座，知识库是数据库的语义层；一个二进制拷走就能跑，备份就是拷一个文件。

## 功能一览

**数据库服务**
- 通用表操作：结构化查询接口 + RESTful 行级增删改查
- 表名/字段白名单校验与参数占位符绑定（防注入）
- SQLite WAL 模式、读写分离连接池、自动检查点
- 版本化迁移、在线备份（VACUUM INTO，带去重与保留策略）、操作审计
- 全文检索（FTS5 + gse 中文分词）

**知识库服务**
- 文档入库状态机（pending → chunking → embedding → ready/failed，可重试）
- 中文分词 + 向量嵌入，混合检索（关键词 + 语义，RRF k=60 融合）
- small-to-big 上下文扩展、带 [n] 引用的问答
- 内容哈希去重（文档级 + 嵌入缓存，不重复调用模型 API）
- LLM 可选增强：查询改写、列表式重排

**对外接入**
- REST API（OpenAPI 3.1 规范 + Swagger UI）
- MCP 端点（官方 Go SDK，Streamable HTTP）供 AI agent 直接接入
- 静态 API Key / JWT 双模式鉴权、限流、安全响应头

**运维**
- 心跳巡检：完整性、外键、备份状态、WAL 体积、文件一致性
- OpenWrt init.d 开机自启、每日自动备份

## 快速开始

```bash
# 构建（Go 1.24+，纯静态，无需 CGO）
CGO_ENABLED=0 go build -o wikisql ./cmd/server

# 配置
cp config.example.yaml config.yaml   # 填入模型 API 与访问密钥

# 运行
./wikisql -config config.yaml
```

浏览器打开 `http://127.0.0.1:8080/` 自动跳转到交互式 API 文档（`/docs`）。

## 接入示例

```bash
# 查询表
curl http://YOUR_HOST:8210/api/v1/tables \
  -H "Authorization: Bearer YOUR_API_KEY"

# 知识库检索（中文混合检索）
curl -X POST http://YOUR_HOST:8210/api/v1/kb/search \
  -H "Authorization: Bearer YOUR_API_KEY" -H 'Content-Type: application/json' \
  -d '{"query":"想搜什么","mode":"hybrid","top_k":5}'

# 带引用问答
curl -X POST http://YOUR_HOST:8210/api/v1/kb/chat \
  -H "Authorization: Bearer YOUR_API_KEY" -H 'Content-Type: application/json' \
  -d '{"query":"想问什么"}'
```

MCP 客户端配置：

```json
{
  "mcpServers": {
    "wikisql": {
      "url": "http://YOUR_HOST:8210/mcp",
      "headers": { "Authorization": "Bearer YOUR_API_KEY" }
    }
  }
}
```

## 技术栈

| 组件 | 选型 | 理由 |
|---|---|---|
| 语言 | Go | 单静态二进制、交叉编译友好 |
| 数据库 | [modernc.org/sqlite](https://pkg.go.dev/modernc.org/sqlite) | 纯 Go 翻译版 SQLite，零 CGO |
| 向量检索 | [modernc.org/sqlite/vec](https://pkg.go.dev/modernc.org/sqlite/vec) | sqlite-vec 官方打包，向量检索留在 SQL 层 |
| HTTP 路由 | [go-chi/chi/v5](https://github.com/go-chi/chi) | 100% net/http 兼容 |
| 鉴权 | [golang-jwt/jwt/v5](https://github.com/golang-jwt/jwt) | 生态标准 |
| MCP | [modelcontextprotocol/go-sdk](https://github.com/modelcontextprotocol/go-sdk) | MCP 官方 Go SDK |
| 中文分词 | [go-ego/gse](https://github.com/go-ego/gse) | 内嵌词典，零外部文件 |
| 文本分块 | [1Vewton/textsplitter](https://github.com/1Vewton/textsplitter) | Unicode 感知的递归分块 |

## 架构

```
cmd/server/          进程入口（装配与生命周期）
internal/
  config/            配置加载（YAML + 环境变量覆盖）
  store/             唯一 SQL 执行层（连接池/迁移/备份/审计）
  table/             通用表操作服务（白名单校验/占位符绑定）
  kb/                知识库（分词/分块/嵌入/混合检索/问答）
  llm/               外部模型适配（OpenAI 兼容，多供应商）
  mcpserver/         MCP 端点（官方 SDK，显式 schema）
  httpapi/           HTTP 路由/中间件/鉴权/文档
  auth/              API Key 与 JWT 鉴权
  health/            心跳巡检
api/openapi.yaml     OpenAPI 3.1 规范
```

设计要点：
- **唯一数据通道**：全项目只有 `internal/store` 能执行 SQL，其他层只能调它的方法。
- **依赖单向**：`httpapi → kb/table → store`，反向依赖为零。
- **安全默认**：默认只监听本机；写工具（MCP）默认关闭；公网无鉴权需显式确认风险。

## 文档

- [docs/goal.md](docs/goal.md) — 项目目标与边界
- [docs/plan.md](docs/plan.md) — 实施计划与里程碑
- [docs/structure.md](docs/structure.md) — 架构说明
- [docs/rules.md](docs/rules.md) — 编码规范
- [docs/research.md](docs/research.md) — 技术调研存档（选型依据）
- [docs/usage.md](docs/usage.md) — 使用手册

## 许可

个人项目，仅供学习参考。
