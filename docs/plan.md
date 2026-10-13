# 实施计划（Plan）

## 里程碑
1. 里程碑 1「地基」：单进程可启动，SQLite 存储层稳定（读写分离双连接池、迁移、在线备份、审计），通用表操作接口（结构化查询 + RESTful 路由两层）上线，心跳巡检层跑起来，服务默认只监听本机。
2. 里程碑 2「知识库」：文档上传、gse 中文分词、FTS5 全文索引、RecursiveSplitter 分块、嵌入入库、sqlite-vec 向量检索、全文+向量双路 RRF 融合检索、带引用的问答跑通；外置文件目录与内容指纹校验可用。
3. 里程碑 3「暴露与加固」（已完成，2026-10-13 验证）：JWT 鉴权（golang-jwt/v5 HS256，登录端点限流）、HTTPS（MinVersion 1.2，手动证书）、公网 acknowledge_risk 二次确认、安全响应头、全局限流、请求体上限、OpenAPI 3.1 规范（/openapi.json + /docs Swagger UI）、MCP 官方 SDK 端点（/mcp，读工具默认开，写工具 mcp.allow_writes 控制）。autocert 自动证书列为后续可选项。

## 任务拆解
- 任务 1：项目骨架与配置体系。目标：可编译、可启动、配置分层明确（YAML 配置文件 + 环境变量覆盖）。输出：cmd/server/main.go、internal/config 包、log/slog 日志、go.mod。
- 任务 2：存储层。目标：唯一 SQL 执行入口，读写分离双连接池（写池 MaxOpenConns=1 + _txlock=immediate，读池 MaxOpenConns=4），PRAGMA 进 DSN（journal_mode=WAL、busy_timeout=5000、foreign_keys=1、synchronous=NORMAL），迁移记录表，在线备份（modernc.org/sqlite Backup API），审计表。输出：internal/store 包与测试。
- 任务 3：通用表操作接口。目标：两层接口——结构化查询接口 POST /api/v1/query（带表名、操作类型、条件、字段），RESTful 路由 /api/v1/tables/{name}/rows（CRUD）；表名与字段白名单校验，参数值用占位符绑定。输出：internal/httpapi 路由与处理函数。
- 任务 4：心跳巡检层。目标：定时跑 PRAGMA integrity_check、foreign_key_check、page_count、WAL 文件大小、外置文件一致性（自动重建索引）、备份状态；异常写 health 表。输出：internal/health 包。
- 任务 5：知识库数据模型。目标：文档表、分块表、嵌入表（vec0 虚拟表）、FTS5 全文索引表（gse 预分词后写入）。输出：迁移脚本与存储方法。
- 任务 6：文本处理与嵌入流水线。目标：上传后用 RecursiveSplitter 分块（512 token，无重叠），gse 分词后写入 FTS5，批量调外部嵌入 API 入库 vec0，失败可重试，嵌入结果按内容指纹缓存。输出：入库流水线。
- 任务 7：检索与问答。目标：关键词走 FTS5、语义走向量列取距离排序，两路 RRF 融合后返回；问答把命中分块交给 LLM 适配层生成带引用的回答。输出：检索服务与问答接口。
- 任务 8：外部模型适配。目标：对话接口 Chat(messages)→text 与嵌入接口 Embed(text)→vector，多家供应商可配置切换，密钥走配置。输出：internal/llm 包。
- 任务 9：安全加固与对外暴露。目标：JWT 鉴权（golang-jwt/jwt/v5，HS256 签名），HTTPS，公网防护开关，OpenAPI 文档生成。输出：传输层与鉴权中间件。
- 任务 10：可选 MCP 工具暴露。目标：把数据库操作和知识库能力封装成 MCP 工具，供外部 agent 调用。输出：internal/mcp 包（里程碑 3 的可选后置任务）。

## 验收节点
- 验收点 1：里程碑 1 结束时，用真实请求完成建表、插入、查询、全文检索，并跑通一次备份恢复演练。
- 验收点 2：里程碑 2 结束时，录入若干真实中文文档，语义检索命中文档，全文检索中文可用，问答输出带可点击引用。
- 验收点 3：里程碑 3 结束时，开启鉴权后未带凭据的请求被拒绝；公网监听且未启用鉴权时提示需要 acknowledge_risk: true。

## 风险与回滚
- 风险：纯 Go 版 SQLite 写入吞吐低于 C 版。
  回滚策略：读写分离双连接池 + busy_timeout 规避；若仍不足，接口层不变换 CGO 版驱动，但需重新评估静态二进制特性。
- 风险：sqlite-vec 未到 1.0，接口可能变。
  回滚策略：go.mod 锁定版本；升级时重测检索一致性；备选方案是向量以二进制存库、Go 内存算距离。
- 风险：外部模型接口不稳定或计费不可控。
  回滚策略：适配层抽象供应商，嵌入结果按内容指纹缓存避免重复计费；供应商不可用时检索降级为纯关键词。
- 风险：单文件库损坏导致数据库与知识库同时受损。
  回滚策略：在线备份 API 做定时备份，备份文件与主库分开存放；恢复演练列为里程碑 1 验收条件。
- 风险：gse 分词词典体积影响二进制大小。
  回滚策略：词典通过 go:embed 内嵌；若体积过大，备选 trigram 分词器（改 CREATE VIRTUAL TABLE 一行语句，数据需重建索引）。
