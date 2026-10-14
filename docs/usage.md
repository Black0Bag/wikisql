# wikisql 使用手册

## 基本信息
- GitHub: https://github.com/Black0Bag/wikisql
- 局域网地址: http://YOUR_HOST:8210
- 公网地址: http://YOUR_HOST:8210
- API Key: YOUR_API_KEY
- 登录方式: 在请求头带上 Authorization: Bearer YOUR_API_KEY

## 浏览器入口
打开 http://YOUR_HOST:8210/ 会自动跳转到 /docs，那里有完整的交互式 API 文档，可以直接在页面里试调每个接口。

## 管理面板（Web 配置界面）
打开 http://YOUR_HOST:8210/admin，用管理密码登录后可以：
- 查看服务状态：运行时长、内存、数据库大小、磁盘、备份、巡检记录
- 修改全部配置：服务端口、密钥、模型、知识库、MCP、安全防护等（可选项为下拉菜单）
- 修改管理密码、一键重启服务

管理密码首次启动时自动生成并打印到日志（/tmp/wikisql.log），登录后请立即在「安全」页修改。
密码以 PBKDF2-SHA256 哈希存储，不存明文；会话有效期默认 24 小时。

## 常用接口（全部是 curl 示例，可直接复制使用）

### 查看有哪些表
curl http://YOUR_HOST:8210/api/v1/tables -H "Authorization: Bearer YOUR_API_KEY"

### 查询一张表
curl -X POST http://YOUR_HOST:8210/api/v1/query -H "Authorization: Bearer YOUR_API_KEY" -H 'Content-Type: application/json' -d '{"table":"kb_documents","operation":"select","limit":10}'

### 上传文档到知识库（用集合 1「默认知识库」）
curl -X POST http://YOUR_HOST:8210/api/v1/kb/collections/1/documents -H "Authorization: Bearer YOUR_API_KEY" -H 'Content-Type: application/json' -d '{"title":"文档标题","content":"文档正文"}'

### 检索知识库
curl -X POST http://YOUR_HOST:8210/api/v1/kb/search -H "Authorization: Bearer YOUR_API_KEY" -H 'Content-Type: application/json' -d '{"query":"想搜什么","mode":"hybrid","top_k":5}'

mode 可选：hybrid（混合，默认）、keyword（中文关键词）、semantic（语义向量）

### 基于知识库问答（输出带 [n] 引用）
curl -X POST http://YOUR_HOST:8210/api/v1/kb/chat -H "Authorization: Bearer YOUR_API_KEY" -H 'Content-Type: application/json' -d '{"query":"想问什么"}'

### 上传文件（PDF/md/txt 都行，会走中文分词和向量索引）
curl -X POST http://YOUR_HOST:8210/api/v1/kb/collections/1/documents -H "Authorization: Bearer YOUR_API_KEY" -F "file=@/path/to/your/file.pdf"

## 给 AI 用的 MCP 入口
- 端点: http://YOUR_HOST:8210/mcp（公网）或 http://YOUR_HOST:8210/mcp（内网）
- 工具: db_list_tables、db_get_schema、db_select、kb_search、kb_chat、kb_list_collections
- 配置方法: 在任意 MCP 客户端的配置文件中加入这个端点并带上 Authorization: Bearer YOUR_API_KEY

## 现有集合
1. 默认知识库: 存放日常文档
2. wikisql 项目文档: 本项目的架构、调研、计划、规范、API 文档（已灌入）

## 服务管理（在路由器上执行）
/etc/init.d/wikisql start    # 启动
/etc/init.d/wikisql stop     # 停止
/etc/init.d/wikisql restart  # 重启
/etc/init.d/wikisql status   # 查看状态

## 路由器上的文件
- 二进制: /root/wikisql/wikisql
- 配置: /root/wikisql/config.yaml（含 API Key，权限 700）
- 数据库: /root/wikisql/data/main.db
- 备份: /root/wikisql/data/backups/（每天自动备份一次）
- 日志: /tmp/wikisql.log
- 开机自启: 已通过 /etc/init.d/wikisql enable 启用
