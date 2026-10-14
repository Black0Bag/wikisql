# 深度论证：SQLite + 向量知识库 + 智能代理的一体化方案

本文记录 2026 年 10 月的外部调研结论，以及据此对架构做出的调整。所有结论以文中给出的链接与出处为准，未经我实际运行的都标注了「未实测」。

## 一、外部调研得到的关键事实

### 1. SQLite 做向量检索已经是成熟路线，不是玩具

SQLite 官方在 2026 年维护着一个叫 vec1 的向量检索扩展，用 C 写成、无外部依赖、支持欧氏距离与余弦距离，还支持倒排文件索引做近似检索。也就是说，「SQLite 只能做精确扫描」这个老印象已经过期。见 sqlite.org 的 vec1 参考文档。

社区的主力实现是 sqlite-vec（前身是 sqlite-vss），作者 asg017。它的定位很明确：放弃 Faiss 重型依赖，改成单个 C 文件、无依赖、任何能加载 SQLite 扩展的地方都能用，包括浏览器里。据其官方基准，一百万向量以内流畅运行，超过这个量级才需要服务型向量数据库。它声明自己尚未到 1.0 版本，接口可能变，这一点必须记下来。

在 Go 生态里，modernc.org/sqlite（纯 Go 翻译版 SQLite，不需要 CGO）已经官方打包了 sqlite-vec，导入 modernc.org/sqlite/vec 即可让所有连接自动加载。这是本次调研对本项目最有价值的一条发现：**它意味着我们可以在完全不用 CGO 的前提下，把向量检索保留在 SQL 层。**

另一条路线是阿里 2026 年 2 月开源的 Zvec，嵌入式向量数据库，定位「向量数据库版的 SQLite」。它功能比 sqlite-vec 全（量化、多路召回、重排、崩溃恢复），但它是一个独立的存储系统，与「所有数据只进 SQLite」这条硬约束冲突，因此本项目不采用。它的价值在于提示了一个方向：未来如果向量规模超过 SQLite 单文件能舒适承载的量，再单独评估。

### 2. 「在数据库里跑智能代理」这条路已经有人走通，但方式和我们不同

sqliteai 团队做了一整套 SQLite 扩展：sqlite-vector 做向量检索、sqlite-ai 做本地模型推理、sqlite-memory 做代理记忆、sqlite-mcp 让 SQL 直接调外部工具、sqlite-agent 把这几样拼成「数据库内的自主代理」。他们的做法是用 SELECT agent_run(...) 这样的 SQL 语句驱动一个多步代理，代理通过 MCP 协议拿外部数据、回写数据库表。

他们的设计对我们的启示有两点：一是「代理的记忆应该是独立、可检索的一块数据」，这与我把代理记忆单独放一个库文件的想法一致；二是他们证明了「代理产出应当落成结构化数据行，而不是一段聊天文字」，这也支持我用提案表来承载代理输出。

但他们的路线和本项目有根本冲突：他们默认模型跑在本地（GGUF 格式文件，支持 GPU 加速），而本项目明确不做本地模型；他们把智能做成了 SQLite 扩展，而我们要求全部能力经 SQLite 官方接口——用扩展不等于绕过，但把 agent 塞进数据库内核会让 Go 进程失去对执行边界的控制。因此本项目只借鉴其数据模型，不引入其扩展。

### 3. 「自然语言转 SQL」的工程现实：只读可以放手，写入必须卡住

学术与开源侧的共识相当一致。AgentNLQ 的做法是规划、生成、执行三个角色分工，先用离线流水线把表结构、字段含义、表间关系生成描述并做嵌入索引，运行时再据此生成 SQL；关键点是它用「语法校验、真实执行、结果集启发式」三种信号做反馈，而不是让大模型自己评价自己写对了没有。另一篇 PostgreSQL 自愈流水的论文更进一步，把所有连接强制为只读，用数据库自己的错误码驱动重试，实测最强模型的自愈增益约 4.6 到 9.3 个百分点。

还有一篇 2026 年的 DBA-Bench 论文专门研究「用大模型做数据库运维代理」，得出的结论值得本项目当戒条：数据库运维本身是专业性极强、持续性、高风险的工作，让代理去诊断甚至去修，最大的风险不是它不会，而是「工作负载还在继续跑，而代理的每一个探测和修复动作都在改变锁、优化器状态和下一步能看到的证据」。

这三条调研结论共同指向同一套设计，也就是本项目已经定下的规则：代理可以生成 SQL 供人审，但绝不被允许自己执行写入；代理做诊断时只用只读查询，不碰任何会改变数据库状态的语句。

### 4. RAG 检索的成熟配方：混合检索加融合排序

sqliteai 的 sqlite-rag 给出了一个和本项目设想高度重合的配方：全文检索与向量检索两路并行，用 RRF（Reciprocal Rank Fusion，一种不依赖具体分数的排序融合算法）融合两路结果，再把候选交给重排模型。中文社区那个本地知识库项目 fast_retriever 也是同一套路：BM25 关键词加向量相似度，RRF 融合，最后 cross-encoder 重排。

这条对本项目的直接影响：检索阶段要设计成两路可插拔，融合算法先用 RRF 这种不需要调分数的，重排作为可选增强后置。

## 二、本次调研促成的三个架构决定

**决定一：向量检索走 modernc.org/sqlite/vec，不自己算距离。**
上一轮我给的选项 B（向量以二进制存库、Go 在内存里算距离）作废。原因是调研发现纯 Go 驱动已经官方打包了 sqlite-vec，走 CGO 的理由消失了。用 vec0 虚拟表的好处是检索逻辑留在 SQL 层，与「全部经 SQLite 官方接口」的硬约束完全贴合；代价是 sqlite-vec 尚未 1.0，版本要锁定，且升级时要重测检索结果是否一致。这一点写进风险清单。

**决定二：检索固定为「全文 + 向量」双路，融合用 RRF。**
不做单路语义检索。理由是纯语义对专有名词、缩写、编号类查询（比如项目代号、错误码）明显弱于关键词，而纯关键词对换种说法的查询无能。两路都跑再融合，是调研中所有方案的共同做法，成本只是多一次查询。

**决定三：代理的定位从「自维护」下调为「只读诊断 + 人工确认执行」。**
调研中 DBA-Bench 那条结论是关键依据：数据库运维动作会相互干扰证据链。因此代理的巡检全部用只读查询，产出写进提案表；任何修复动作都必须人点确认后才执行，执行前后都写审计。这比原设想「数据库自维护」弱，但这是唯一在个人自托管场景下风险收益成正负得清的形态。

## 三、第二轮深挖：四个技术细节的落实方案

### 5. FTS5 中文分词：本项目最大的技术风险

FTS5 默认的 unicode61 分词器对中文基本不可用：中文没有词间空格，一整段中文会被当成一个不可分割的词，搜「认证」匹配不到「认证流程」。这个问题在 2026 年仍是社区高频踩坑点。

三档方案：
- A 档 trigram 分词器（SQLite 内置）：按三字符滑动窗口切片，天然支持子串匹配，索引大 3-5 倍，纯 Go 兼容。
- B 档 Go 层预分词 + unicode61：用 gse 库（github.com/go-ego/gse，约 15 万词）在写入前切成空格分隔的词，查询时同样分词。dscli 项目验证了此方案，词典通过 go:embed 内嵌，零外部文件。
- C 档 C 扩展分词器（jieba/simple）：效果最好但需要 CGO，排除。

决定：采用 B 档为主、A 档为备。两个方案数据结构相同（都是 FTS5 虚拟表），切换只影响分词逻辑，不动表结构。

### 6. SQLite 在线备份：modernc.org/sqlite 原生支持

modernc.org/sqlite 提供 Backup 类型，与 C 层 Online Backup API 一一对应：NewBackup、Step(n)、Remaining/PageCount、Finish。备份过程中源库可继续读写，备份结果是开始时刻的逐字节快照。WAL 模式下自动处理未检查点数据。不需要 cp 拷文件。

### 7. 鉴权：HMAC-SHA256 签名令牌

单用户自托管，不需要 OAuth/JWT 库。用 Go 标准库 crypto/hmac + crypto/sha256 实现签名令牌，签名验签各十几行。公网无鉴权不拒绝启动，要求配置里显式写 acknowledge_risk: true。

### 8. modernc.org/sqlite/vec 用法确认

Side-effect import modernc.org/sqlite/vec 后所有连接自动注册 vec0 虚拟表和 vec_* 函数。用法：
```sql
CREATE VIRTUAL TABLE vec_docs USING vec0(embedding float[768]);
INSERT INTO vec_docs(rowid, embedding) VALUES (1, '[...]');
SELECT rowid, distance FROM vec_docs WHERE embedding MATCH ? ORDER BY distance LIMIT 5;
```
与「所有数据操作只走 SQLite 官方接口」硬约束完全贴合。

## 四、已决定的待定项

- 底座接口粒度：用户选 C（结构化查询接口打底 + 语义化路由叠加）。
- 外置文件一致性校验：自动重建索引。
- 公网无鉴权：不拒绝启动，二次确认即可。

## 六、第三轮深挖：存储层并发、分块策略、代理巡检

### 9. 存储层并发模型：读写分离连接池

SQLite WAL 模式允许并发读、单写。但 Go 的 database/sql 连接池有一个经典陷阱：PRAGMA 是每连接的，在 *sql.DB 上执行 PRAGMA journal_mode=WAL 只影响拿到的那一个连接，连接池新建的第二个连接不带这个设置。多个 Go 协程同时 BEGIN; INSERT; COMMIT 仍然会撞 SQLITE_BUSY（数据库忙错误）。

hollis-labs/go-sqlite 项目（sqlitekit）把这个问题彻底理清了解决方案：
- PRAGMA 写进 DSN 的 _pragma= 查询参数，这样 modernc.org/sqlite 在每打开一个新连接时都自动应用，不依赖运行时调用。
- 写连接池强制 MaxOpenConns=1 + _txlock=immediate（在 BEGIN 时就获取写锁，而不是等第一条写语句）。
- 读连接池 MaxOpenConns=4（或更多），不带 _txlock，允许并发读。

这个模式完全适配本项目的单写者队列需求。关键 PRAGMA 组合：journal_mode(WAL)、busy_timeout(5000)、foreign_keys(1)、synchronous(NORMAL)。

### 10. 知识库分块策略：句子级切分 + 无重叠

2026 年 1 月的 arXiv 论文（2601.14123）做了系统评测，得出三条结论：
- 重叠区间没有可量化的收益，反而增加索引成本。
- 句子级切分是最具性价比的方法，效果跟语义切分相当（直到约 5000 token）。
- 超过约 2500 token 后检索质量断座下降（"context cliff"）。

NVIDIA 2025 年 6 月的技术博客结论类似：512-1024 token 是最佳区间，128 和 2048 都明显偏弱。IEEE COINS 2025 的论文在七个学术领域上测了 90 种组合，句子切分 + 512 token 窗口表现最好。

Go 生态的句子分割库：
- github.com/clipperhouse/uax29/sentences：基于 Unicode UAX #29 文本分割标准（Unicode 15.0.0），纯 Go，标准合规。UAX #29 把 U+3002（。）定义为句子终止符，所以对中文有效。
- github.com/1Vewton/textsplitter：提供 Fixed、Recursive、Semantic 三种切分器，Unicode 感知（按 rune 计数，不按字节），支持 context，支持多文档并发切分。Recursive 模式的分隔符列表包含中文标点（。、，）。

决定：采用句子级切分，目标 512 token，无重叠区间。切分库选用 1Vewton/textsplitter 的 RecursiveSplitter（先按段落、再按句子、再按词、最后按字符回退）。如果后续实测发现检索质量不足，再考虑加重叠或切到语义切分。

### 11. 代理巡检规则集与提案数据结构

代理巡检的只读查询集（均可通过 SQLite 官方接口完成）：
- 完整性：PRAGMA integrity_check、PRAGMA foreign_key_check（孤立外键引用）
- 体积与增长：PRAGMA page_count × PRAGMA page_size（库体积）、WAL 文件大小（检查点是否需要执行）
- 孤立数据：知识库分块表里指向已删除文档的残留记录、外置文件目录里有路径但文件已缺失的记录
- 索引健康：FTS5 索引大小、vec0 虚拟表行数与主表行数是否一致
- 备份状态：上次成功备份的时间戳（从审计表查）、备份文件是否存在且大小合理
- 慢查询检测：如果开启了 sqlite_stat1 统计，检查哪些表缺少索引

提案表结构（存在主库中）：
- id：唯一标识
- created_at：提案时间
- severity：info / warning / critical
- category：integrity / performance / storage / consistency
- title：一句话描述
- description：详细说明
- evidence：诊断查询的输出结果
- proposed_action：建议的修复动作（SQL 语句或操作描述）
- status：pending / approved / rejected / executed
- executed_at、executed_by：执行记录
- audit_id：关联的审计记录

代理的记忆与审计独立存第二个 SQLite 文件（agent.db），主库的维护任务不触碰它。

## 七、第三轮深挖：对技术路线本身的质疑

### 12. Go + SQLite 打包到一起，这个方案真的好吗？

**支持的证据：**

SQLite 官方文档的选型清单很明确：数据与应用在同一台机器上、没有多进程并发写、不需要通过网络分离数据访问——满足这三条，SQLite 就是正确选择。本项目是单进程、单机、个人自用，三条全中。

2026 年的基准测试（andrecasal/sqlite-vs-postgres-benchmark）实测：单进程场景下，SQLite WAL 模式的顺序写入吞吐是 PostgreSQL 的 3 倍（23403 vs 7740 ops/sec），混合读写是 8 倍（96051 vs 11824 ops/sec）。原因是 SQLite 在进程内直接调用库函数（约 0.02ms），PostgreSQL 要走 Unix socket 进程间通信（约 0.10ms）。个人自托管场景没有多进程写，SQLite 的单写者约束不构成瓶颈。

SQLite 官方数据：日访问量 10 万次以下完全没问题，实测过 10 倍（100 万次/日）。个人知识库的负载远低于此。

向量方面：1000 万向量以内，pgvector 与专用向量数据库的延迟差异在用户不可感知范围（3ms vs 12ms）；sqlite-vec 官方基准在 100 万向量以内流畅。个人知识库不可能接近这个量级。

2026 年 6 月 Java Code Geeks 的文章标题就叫「SQLite in 2026: Why Serious Apps Are Choosing It Over Postgres」——核心论点是 WAL 模式、Litestream 连续备份、sqlite-vec 向量扩展三件事补齐了 SQLite 过去的短板。

**诚实的反面：**

如果只看「最佳工程实践」，2026 年个人 RAG 的事实标准是 PostgreSQL + pgvector。它在一千万向量以内够用、ACID 事务保证向量与关系数据一致性、一个数据库实例解决所有问题。多个来源（Medium 2026 年 5 月的生产决策指南、rack2cloud 的成本分析、wz-it.com 的 Qdrant vs pgvector 对比）都给出同一个建议：「先从 pgvector 开始，只在能说出具体瓶颈时才迁移到专用向量数据库」。

但 PostgreSQL 是 C/S 架构（客户端/服务器），需要一个独立的数据库进程在后台运行。这意味着：你要装 PostgreSQL、配置它、启动它、维护它——跟你「一个二进制文件拷走就能跑」的期望直接冲突。而且你的硬约束是「所有数据全部以 SQLite 形式存取」，选 PostgreSQL 就等于推翻这条。

**结论：对你的场景，Go + SQLite 打包是正确选择。** 理由不是「SQLite 更好」，而是「SQLite 的形态与你的约束完全匹配」：单进程、单文件、零运维、零配置、静态二进制。性能上 SQLite 在这个量级不输 PostgreSQL，备份用 Online Backup API 解决，向量用 sqlite-vec 解决，中文检索用 gse 解决。唯一真正的代价是单写者约束——但个人自托管场景的写入量远不到瓶颈。

### 13. 真的需要集成一个微 agent 智能体吗？

**业界现状：**

2026 年 AI 数据库代理是一个热词。IBM Db2 Genius Hub 做了「代理式维护、代理式自愈、代理式响应」三层架构；Oracle Autonomous Database 宣称自管理、自安全、自修复；Google Cloud 在 Next '26 发布了两个 AI 数据库代理；TiDB 宣布向「AI 原生数据库」演进，说「人类不再是数据库的主要使用者」。连 Revefi 都做了一个 AI DBA 产品。

但这些都是**企业级场景**：数百个数据库实例、数十 TB 数据、数十个 DBA 团队、每天数百万次查询。代理的价值在于把 DBA 从重复劳动中解放出来。

**对你的场景的诚实判断：**

一个个人 SQLite 数据库，需要的维护任务是：偶尔跑一次 `PRAGMA integrity_check`、定期备份、检查外置文件是否还在。这些事情用几十行 Go 代码写个定时任务就够了，不需要大模型。

所以如果 agent 只是做数据库巡检，那它确实是过度设计。一个 cron job 加几个 SQL 查询就能干完。

**但 agent 的真正价值不在「数据库维护」，在以下三点——而这三点是 cron job 干不了的：**

1. **知识库的语义能力。** 知识库需要：判断两段文本是否说同一件事、给文档自动打标签、根据用户问题从库中检索并生成带引用的回答。这些不是 SQL 能做的，必须调外部大模型。与其把 LLM 调用逻辑散落在知识库各处，不如把它收拢到一个有记忆、有治理的 agent 层。

2. **自然语言查询数据库。** 你存了数据，将来可能想问「上个月入库的文档里哪些跟 Go 有关」——不用写 SQL，让 agent 把自然语言转成 SQL 供你审、你确认后执行。调研中 AgentNLQ 的做法证明了这条路可行（前提是只读查询可以直接放手，写操作必须人工确认）。

3. **跨数据源的语义关联。** 将来网盘、博客都挂在这个库上，agent 有可能做到「网盘里有个 PDF 讲 Go 并发，博客里你写过一篇 goroutine 的文章，它们说的是同一件事」——这种跨组件的语义关联只有 agent 能做。

**结论：agent 的定位应该从「为数据库提供智能化」调整为「为整个数据底座提供语义能力」。** 数据库巡检可以留着，但它不是 agent 的核心价值，只是附带功能。真正让 agent 有存在意义的是知识库的语义检索与问答、自然语言查询、跨组件关联。如果这三件事你不需要，那 agent 确实可以砍掉，只留一个定时任务做巡检。

## 八、第四轮调研：成熟知识库方案全景（里程碑 2 开工前）

用户质疑：不能直接写，要先参考世界上最先进的知识库方案。本轮调研结论如下。

### 14. 知识库不是只有一种

市面上的知识库分四类：

1. **文档 RAG**（文件→分块→向量→检索→问答）：RAGFlow（68K star，深度文档理解、模板化分块、有据引用）、AnythingLLM（工作区隔离、LanceDB、重排、内置 agent）、Open WebUI（聊天优先，RAG 是附件）、Onyx/Danswer（企业级、40+ 连接器、混合搜索加重排加知识图谱）。
2. **笔记/备忘型**（结构化笔记、标签、双向链接）：Obsidian、SiYuan、memos。
3. **agent 记忆型**（从对话提取事实、按作用域存储）：Mem0（提取式事实，token 高效，LongMemEval 94.4）、Letta/MemGPT（三层记忆：核心/回忆/归档，自管理）、Zep/Graphiti（时序知识图谱，因果推理强）。
4. **知识图谱型（GraphRAG）**：多跳推理和全局问题强（全局主题类问题全面性提升 50-70%），但需要图数据库、社区检测、LLM 抽取管道，部署重。arXiv 2502.11371 结论：RAG 强在事实型问答，GraphRAG 强在推理密集型问答，互补不替代。

本项目定位：文档 RAG 为主体 + API 面向 agent 记忆友好（未来正式 agent 的归档记忆直接用本知识库），图谱型不做（太重），表结构预留扩展空间。

### 15. 从成熟产品学到的具体设计

**入库状态机（RAGFlow）**：文档入库是状态机 pending→parsing→chunking→embedding→ready/failed，每步可查、失败可重试。原设计没有这个。

**有据引用（RAGFlow/AnythingLLM 共同卖点）**：回答必须带可溯源引用标记（第几块、来自哪篇文档），降低幻觉。

**父子分块检索 small-to-big（SemEval 2026 冠军 H-RAG、EMNLP 2024 最佳实践）**：检索在细粒度子块上做（精度高），生成时返回父级上下文（相邻块拼接，语义完整）。检索粒度与生成粒度解耦。

**Anthropic Contextual Retrieval（官方实测数字）**：
- BM25+向量混合在所有测试域一致优于单向量。
- 给每个分块预置 LLM 生成的 50-100 token 上下文说明，再嵌入和建 BM25：检索失败率降 49%；加重排降 67%（5.7%→1.9%）。
- 传给模型 20 个分块比 5/10 个效果好。
- 用 prompt caching 后成本约 $1.02/百万文档 token（一次性）。
- 结论：作为每集合可选开关（默认关，烧 LLM 调用），需要时打开。

**查询改写（SemEval 2026 前三名全部采用）**：对话式查询（含指代）先 LLM 改写成独立完整查询再检索；多路改写+RRF 是 RAG-Fusion。

**重排**：初始检索 top-150，重排取 top-20 给模型。纯 Go 无交叉编码器，用 LLM 列表式重排替代，可选开关。

**memorywire（2026 论文，验证我们的技术栈）**：参考实现用 sqlite-vec + FTS5 + RRF k=60 做 agent 记忆后端，sqlite-vec 适配器支持全部记忆类型。独立佐证 SQLite 栈路线成立。

**agent 记忆 API 模式（Mem0/Letta）**：记忆按作用域组织（用户/会话/agent），检索按作用域过滤。我们的集合（collection）天然充当作用域。

### 16. 修正后的里程碑 2 设计

**表结构**：
- kb_collections（集合=工作区=记忆作用域）：id, name, description, embedding_model, dimension, created_at
- kb_documents：id, collection_id, title, source_type(text/file), source_path, content_hash, status(pending/chunking/embedding/ready/failed), error, chunk_count, created_at, updated_at
- kb_chunks：id, document_id, seq, content, token_count, content_hash, context_header(可选), created_at
- kb_chunks_fts：FTS5，内容 = context_header + gse 分词后的 chunk
- kb_chunks_vec：vec0 虚拟表，维度按集合配置
- 父上下文：命中子块后拼接同文档相邻块作为生成上下文（无需单独父表）

**入库流水线（状态机）**：上传→解析（md/txt 一等公民，PDF 纯 Go 尽力提取，失败标记不阻塞）→RecursiveSplitter 512 分块→（可选）LLM 生成 context_header→gse 分词写 FTS5→批量嵌入写 vec0→ready。内容哈希去重：未变化的块不重复嵌入。

**检索管道（可配置档位）**：
- 基础档：FTS5 关键词 + vec0 语义，RRF k=60 融合，top-K
- 增强档（每请求可开）：LLM 查询改写→双路→RRF→LLM 重排 top-20→top-K
- 命中子块→拼接相邻块→生成上下文（small-to-big）

**问答（带引用）**：top-K 块（含相邻块）交 LLM，输出带 [n] 标记映射到具体块。

**API（/api/v1/kb）**：集合 CRUD；POST /collections/{id}/documents；GET /documents/{id} 查状态；POST /search（query, collection_ids, top_k, mode=hybrid|keyword|semantic, rewrite, rerank）；POST /chat（带引用回答）。

### 17. 明确不做（本轮调研后决策）

- GraphRAG/知识图谱：重，个人场景以事实型问答为主；表结构不阻碍将来加实体关系表。
- HyDE/HyPE：多一次 LLM 调用，收益不确定；查询改写已覆盖大部分收益。
- 交叉编码器重排：纯 Go 无成熟实现；用 LLM 列表式重排替代（可选开关）。

## 九、第五轮调研：里程碑 3 安全加固与对外暴露

### 18. JWT 鉴权：golang-jwt/v5 是事实标准，单用户场景简化设计

golang-jwt/jwt/v5（v5.3.1，15000+ 项目引用）是 Go 生态 JWT 事实标准。注意安全通告 GO-2025-3553（header 解析内存放大）影响 v5.2.1 之前版本，必须用 v5.2.1+（用最新 v5.3.1）。

签名算法选择（auth0 官方指南结论）：HMAC/HS256 适用于「单一服务、自己签发自己验证」——正是本项目场景。RS256/JWKS 那套是给多服务/第三方身份提供方的，不需要。

单用户登录端点设计：POST /api/v1/auth/login {password} → 发 HS256 签名 token（sub=owner, exp=可配置 TTL）。密码用 crypto/subtle 恒定时间比较；密码只走配置或环境变量（WIKISQL_AUTH_PASSWORD）。2026 年最佳实践提但刷新令牌轮换（refresh token rotation）——多用户场景必要；单用户个人服务用「长 TTL + 过期重登」更简单可靠，不引入刷新链路。登录端点限流必须做。

### 19. MCP：官方 Go SDK 已成熟（重大利好）

modelcontextprotocol/go-sdk 是 MCP 官方 Go SDK，v1.8.0（2026-09 发布，5.1K star，Apache-2.0），实现了 2026-07-28 协议版本。关键 API：
- mcp.NewServer(&mcp.Implementation{Name, Version}, nil) 创建服务
- server.AddTool(...) 把 Go 函数绑定为工具
- mcp.NewStreamableHTTPHandler(getServer, opts) 返回标准 http.Handler——可以直接挂到我们的 chi 路由上，与 REST API 同进程同端口
- 支持 Stateless 模式（无会话校验，适合简单部署）
- 另有 StdioTransport（本地 agent 标准接入方式，Claude Desktop 等）

这意味着：本项目加一个 /mcp 端点，任何 MCP 客户端（Claude、Cursor、用户将来的正式 agent）都能直接发现并调用我们的数据库与知识库能力。选官方 SDK 而非社区版 mark3labs/mcp-go（也很流行但非官方）。

工具集设计（读默认开，写需配置开关）：
- kb_search / kb_chat / kb_list_collections（知识库）
- db_list_tables / db_get_schema / db_select（数据库只读）
- db_insert / db_update / db_delete（写操作，mcp.allow_writes 默认 false）

### 20. OpenAPI 文档：手写 spec + 内嵌 Swagger UI

两条路线：spec-first（oapi-codegen 从 YAML 生成代码）vs code-first（swaggo 注释/Huma 生成 spec）。oapi-codegen v2.6.0 支持 chi-server 生成，但要求按生成接口重写全部 handler——对已完成的项目是大重构。swaggo 注释侵入性强。

结论：API 已稳定，代码是事实来源。手写一份 openapi.yaml（OpenAPI 3.1，覆盖全部端点），服务端在 /openapi.json 提供，配 /docs 页面内嵌 Swagger UI（go:embed swagger-ui-dist，约 1MB）。这是对现有代码零侵入的最标准做法。

### 21. 公网暴露安全清单（OWASP + Go 官方文档综合）

- TLS：显式设 MinVersion=TLS1.2（Go 默认服务端最低 1.0，必须手动收紧）；TLS 1.3 优先
- 安全响应头中间件：X-Content-Type-Options: nosniff、X-Frame-Options: DENY、Referrer-Policy: no-referrer、HSTS（仅 TLS 时）、API 响应 Cache-Control: no-store
- 限流：golang.org/x/time/rate 按 IP，全局宽松 + 登录端点严格（如 5 次/分钟）
- 请求体上限：http.MaxBytesReader（默认 64MB，配置可调）
- 超时：ReadHeaderTimeout 等（已有）+ middleware.Timeout（已有）
- autocert（Let's Encrypt 自动证书）：golang.org/x/crypto/acme/autocert，标准做法；列为可选配置，首版先支持手动证书文件
- CORS：个人 API 默认不开跨域；预留 cors.allowed_origins 配置
- acknowledge_risk 二次确认机制已在配置校验中实现
