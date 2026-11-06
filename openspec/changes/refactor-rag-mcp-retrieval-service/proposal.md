## Why

更新：2026-11-06。RAG作为独立同步HTTP MCP查询服务，平台和Travel各自保存所属调用/证据；RAG不写业务PG、不使用Kafka执行检索，不建立RAG Job或结果Outbox。首版检索和管理范围保持不变，见[当前Agent协议](../refactor-agent-kernel-plan-execute/mq-mcp-dispatch-protocol.md)。

现有独立 Python RAG MCP 服务已具备文档入库、混合召回、管理界面和评测基础，但当前小豆包使用 stdio、Chroma 和单个演示片段，知识库策略、索引版本及远端多客户端访问尚未形成统一契约。需要在复用现有工程的基础上，演进为自行管理文档与策略、可通过远端 MCP 调用的知识检索服务，并为分块、重排和时间感知消融提供可追溯的数据基础。

## What Changes

- 扩展现有 MCP 服务为 Streamable HTTP `/mcp`，首版返回 JSON，使用 HTTPS 和 API Key；保留 stdio 运维兼容入口，不因 HTTP 失败自动回退本地进程。暂不引入 OAuth、SSE 进度推送或新的对外写入工具。
- 平台通过HTTP MCP同步取得检索结果，调用方保持Job running/续租并占执行槽；固定逻辑调用、权限和总期限，已保存结果优先复用，未保存只读检索允许有界重试。不增加RAG专业Agent、Job Pool、Kafka请求/结果或自有结果Outbox，不写平台Run/Job/计划表。
- 继续暴露 `query_knowledge_hub`、`query_policy_evidence`、`list_collections`、`get_document_summary` 四个只读工具。保留已有必填字段和政策证据 v1 契约，通过可选字段补充查询类型、时间条件、策略及索引追踪信息。
- **BREAKING** 新建知识索引的目标后端采用 Milvus，Dense 与 BM25 使用相同片段集合；历史 Chroma、JSON BM25 和 SQLite 文件保留，只在重新构建、对照验证成功后显式切换。
- 策略归知识库管理。首版所有正常查询统一使用知识库默认混合召回（Dense + BM25 + RRF）；后台保留按查询类型映射策略的能力，但本次不预设按范围自动分类或不同召回算法分流，Host 不选择算法和权重。
- 查询流程为混合召回、融合去重、可配置 Rerank、可配置时间融合、最终 Top-K。Query 改写及问题时间提取由发起 Agent 负责；RAG 不另启语义改写模型。
- 沿用现有 CLI 和 Dashboard 管理文档，补全文档身份、解析/分块配置快照、索引构建与发布、删除、回退和任务状态。RAG 自有管理存储与小豆包业务库独立。
- 复用评测与 Trace 基础，记录语料、索引、策略、时间信号、证据位置与失败信息，支持后台消融；跨分块评测以原文证据范围为基准。

## Capabilities

### New Capabilities

- `rag-mcp-access`：远端MCP同步工具、API Key/知识库授权、四工具完整证据、期限与失败边界。
- `rag-knowledge-indexes`：RAG 自主管理文档、分块、Milvus 索引版本、发布回退与删除。
- `rag-retrieval-strategies`：知识库默认混合策略、后台类型映射、Rerank、时间融合、证据与评测。

### Modified Capabilities

无既有主规格变更。本change扩展外部服务，不改变travel-policy“证据不等于合规”；平台同步MCP适配归原agent-kernel change，Query改写归Agent，不另开竞争接入规格。

## Impact

- 主要实现项目：`G:/简历项目/MODULAR-RAG-MCP-SERVER`，涉及 `src/mcp_server`、`src/core/query_engine`、`src/ingestion`、`src/libs/vector_store`、Dashboard、CLI、评测与测试。
- 正式规划记录在当前已初始化的 OpenSpec 项目中；外部 RAG 未初始化 OpenSpec，本次不在外部项目创建另一套规格结构。
- 增加Milvus/PyMilvus和HTTP ASGI运行依赖，保留原文件/本地模型能力，不增加Python Kafka客户端或异步检索Outbox。精确版本和部署资源实施计划前锁定，当前未安装。
- 两个仓库均有未提交内容，不覆盖、提交或清理无关修改。与refactor-agent-kernel-plan-execute通过同一[工程协议](../refactor-agent-kernel-plan-execute/engineering-protocol.md)协调平台/RAG两侧边界；不覆盖add-general-web-search未完成工作。

## Non-goals

本阶段只提交规划文档；不修改产品代码、部署服务或迁移数据。不引入 RAGFlow 运行时替换、平台业务数据库耦合、通用 Agent 内核改造、公网抓取器或按查询类型学习的策略路由。RAGFlow 保留为此前比较对象，本方案延续用户选定的现有 RAG MCP 与 Milvus 路线。
