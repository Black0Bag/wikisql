# 项目架构（Structure）

## 目录结构
- 根目录：Go 模块根，含 go.mod、config.yaml、docs 目录。
- cmd/server/main.go：进程入口，只做装配与生命周期，不放业务逻辑。
- internal/config：配置加载与校验（YAML 文件 + 环境变量覆盖）。
- internal/store：全项目唯一可直接执行 SQL 的包。读写分离双连接池（写池 MaxOpenConns=1 + _txlock=immediate，读池 MaxOpenConns=4），PRAGMA 进 DSN，迁移记录表，在线备份（Backup API），审计表。
- internal/table：通用表操作服务，面向上层组件。两层接口：结构化查询与 RESTful 路由。只做受控的表与行操作，不含任何上层业务。
- internal/kb：知识库。文档、分块（RecursiveSplitter 512 token 无重叠）、嵌入（vec0 虚拟表）、全文索引（FTS5 + gse 中文预分词）、混合检索（全文+向量双路 RRF 融合）、带引用问答。
- internal/llm：外部模型适配层。对话接口 Chat(messages)→text，嵌入接口 Embed(text)→vector，多家供应商可配置切换。
- internal/health：心跳巡检层。定时跑 PRAGMA 检查、备份状态、外置文件一致性（自动重建索引）、WAL 文件大小。纯规则代码，不调用 LLM。
- internal/httpapi：HTTP 路由（go-chi/chi/v5）、处理函数、安全响应头与限流中间件、OpenAPI 与 Swagger UI（/openapi.json、/docs）。
- internal/auth：JWT 签发与校验（golang-jwt/jwt/v5，HS256）、登录处理、鉴权中间件。
- internal/mcpserver：MCP 端点（官方 SDK modelcontextprotocol/go-sdk，Streamable HTTP /mcp），暴露数据库与知识库工具；写工具由 mcp.allow_writes 控制。
- api/openapi.yaml：OpenAPI 3.1 规范（代码事实来源的手写文档工件）。
- cmd/mcp-e2e：MCP 端点连通性自检工具。
- migrations：按版本编号的 SQL 迁移脚本。
- data：默认数据目录，含主库文件、备份目录、外置文件目录。

## 模块划分
- 数据层：internal/store，对外只暴露操作方法，不暴露 *sql.DB。
- 服务层：internal/table、internal/kb、internal/health，依赖 store 与 llm。
- 接口层：internal/httpapi，只做协议转换、校验、鉴权，不含业务规则。
- 横切关注点：internal/config、internal/llm、审计与日志。

## 数据流/调用关系
- 接口层接收请求，校验后调用服务层；服务层调用 store 获取或修改数据；store 是唯一执行 SQL 的边界。
- 知识库入库：上传文件落入外置目录，库内记录路径与内容 SHA-256，文本进 RecursiveSplitter 分块，gse 分词后写入 FTS5 表，调 llm 生成嵌入写入 vec0 虚拟表。
- 知识库检索：关键词走 FTS5 全文检索，语义走 vec0 向量距离排序，两路 RRF 融合后返回；问答把命中分块交给 llm 生成带引用的回答。
- 心跳巡检：定时触发，只读查询统计信息，异常写 health 表，外置文件缺失自动重建索引。

## 依赖与外部接口
- 数据库驱动：modernc.org/sqlite（纯 Go，不需要 CGO），配套 modernc.org/sqlite/vec（vec0 虚拟表，向量检索）。
- HTTP 路由：go-chi/chi/v5（100% 兼容 net/http，生产级，无外部依赖）。
- 鉴权：golang-jwt/jwt/v5（标准 JWT，HS256 签名）。
- 中文分词：github.com/go-ego/gse（go:embed 内嵌词典）。
- 文本分块：github.com/1Vewton/textsplitter（RecursiveSplitter，Unicode 感知）。
- 外部模型供应商：对话与嵌入两类接口，地址、密钥、模型名全部走配置。
- 无其他外部服务依赖：不接外部向量数据库、外部缓存、外部搜索引擎。

## 关键入口文件
- cmd/server/main.go：装配配置、存储层、服务层、接口层，启动 HTTP 服务与定时巡检，处理优雅退出。
- internal/store/store.go：存储层构造函数与核心操作方法。
- internal/httpapi/server.go：路由注册与中间件链。
- migrations 目录中的编号脚本：每次结构变更的唯一来源。

## 高风险模块
- internal/store：单文件库的唯一写入口，读写分离双连接池与备份逻辑的正确性直接决定数据安全。
- 备份与恢复：在线备份 API 自动处理 WAL 未检查点数据；恢复演练列为里程碑 1 验收条件。
- FTS5 中文分词：gse 预分词的正确性决定中文全文检索是否可用；备选 trigram 分词器。
- 传输层与鉴权：公网误配导致裸库暴露；acknowledge_risk 二次确认机制兜底。
