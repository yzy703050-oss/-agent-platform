## Context

更新：2026-11-06。平台通过Outbox/Kafka/Dispatch向独立Travel MCP服务分发专业任务；平台及Travel对RAG仍是同步HTTP MCP查询。调用方保持running/续租/占槽并保存所属调用和证据；RAG自管知识/索引/构建数据，不写业务PG、不建RAG Job或请求/结果Topic。平台执行边界见[Agent协议](../refactor-agent-kernel-plan-execute/mq-mcp-dispatch-protocol.md)，授权/预算见[公共协议](../refactor-agent-kernel-plan-execute/engineering-protocol.md)。

动机、范围和三个正式能力见 [proposal.md](proposal.md) 与 [specs](specs)。本文件是待审阅技术设计；不表示代码已经实现或真实依赖已经验收。

当前外部项目位于 `G:/简历项目/MODULAR-RAG-MCP-SERVER`，尚未初始化 OpenSpec。当前项目已有 OpenSpec，本变更在此保存唯一正式规格，实施时需显式定位外部仓库并保护两个仓库已有修改。

已有基础：官方 Python MCP SDK `1.30.0`，本地可查询其最高协议版本为 `2025-11-25`；stdio 入口、四个只读工具、CLI、Streamlit、MarkItDown PDF/TXT Loader、递归分块、BGE 本地嵌入、Chroma、JSON BM25、SQLite、Trace 和 EvalRunner。Go 消费方固定 SDK `v1.8.0`，目前为 stdio，另有单实例锁；本变更不声称平台已支持多进程部署。

已发现的实现限制：共享 `_current_collection` 和锁；混合异常被 `_perform_search` 转为 `[]`；Dashboard 上传选项包含内核不支持的格式，且未检查 `PipelineResult.success`；临时上传路径参与身份；旧评测依赖 chunk ID；文件哈希去重没有完整处理配置；Rerank 提前截断结果。迁移需覆盖这些实际路径，不能仅增加配置字段。

## Goals / Non-Goals

**Goals:** 复用已有工具与管理入口，以独立服务管理文档、策略、版本和实验；Host 只提供查询、逻辑知识库、可选查询类型及结构化时间条件。生产检索默认混合，后台实验可配置并可复现。

**Non-Goals:** 不实现Agent Query改写，不接入RAGFlow，不写平台业务PG或增加公开写工具，不引入OAuth、RAG SSE、Redis状态库、Kafka检索交接或共享SQLite多副本写入。HTTP服务多个客户端，首版单实例及一个构建worker，检索并发有界。

## Decisions

### D1. 决策来源与策略归属

| 来源 | 已定内容 |
|---|---|
| 用户明确决定 | 外部独立 RAG MCP；Milvus；远端 HTTP、JSON、API Key；暂不 OAuth；Query 改写由 Agent 完成；分块/召回可选；加入 Rerank 和可选 TimelyRAG 时间感知 |
| 用户本轮决定 | 知识库默认策略；后台可按查询类型配置；本轮正常查询统一混合；后续依据范围消融再决定分流 |
| 2026-11-05后续明确确认 | Travel同步调用RAG工具，无RAG Job/Kafka结果交接；独立服务、自管知识数据及无用户整轮取消/暂停保持 |
| 代理补充默认值 | 沿用 CLI/Dashboard 与四个只读工具；自有 SQLite 管理库；单实例服务/单构建 worker；版本化发布；参数、时间缺失规则和降级规则如下 |

`knowledge_bases.default_retrieval_profile_id` 决定默认策略，`query_type_bindings` 保存后台显式类型映射。首版 `default` 为通用类型；可以登记业务类型标签，但所有启用绑定引用同一混合 profile。未知标签回默认，返回 `routing_source=default`。不引入 LLM 分类器或范围推断路由。

Host 不传 `retrieval_profile_id`、模型、权重或物理 collection。后台消融通过独立内部评测调用传入不可变 profile snapshot，不改正常知识库指针。新增查询类型分流需后续实验依据及规格更新。

曾比较保留轻量内核与接入 RAGFlow；本设计延续已选择的现有 MCP + Milvus。曾讨论 Host 指定策略，用户本轮已选择知识库管理，因此不实现该选项。

### D2. MCP 入口、协议与身份

复用 `create_mcp_server` 和工具注册，增加 ASGI Streamable HTTP 入口；沿用现有 Python SDK，协议验收基线固定 `2025-11-25`，不混用 `2026-07-28` 的会话/取消语义。配置 `stateless`、`json_response`，不建立长驻 SSE 或业务会话；GET `/mcp` 可按该版本返回 405。stdio 通过显式启动模式保留。

2026-11-06固定SDK源码及Go→Python实验已核验：直接使用`StreamableHTTPSessionManager(app=原low-level Server, stateless=True, json_response=True)`，在ASGI lifespan中运行唯一manager.run()。json_response并不自动关闭GET SSE，路由显式GET405；Go客户端设DisableStandaloneSSE=true、MaxRetries=-1并复用HTTP池。API Key/Host/Origin/请求体限额在入口处理，请求身份和_meta从每请求上下文取得，不放共享全局变量或模型参数。实验仅固定证据检索器，产品授权及真实检索仍待实施。

远端 URL 为 `https://<host>/mcp`，TLS 可由反向代理终止；仅显式本机开发允许回环 HTTP。SDK 原生处理协议初始化、版本头、方法分发与取消；配置允许的 Host/Origin，禁止为上线方便全局关闭校验。客户端按标准支持返回类型，服务端首版工具返回 JSON。

API Key 是服务凭据，使用 `Authorization: Bearer <opaque-key>`。服务保存高熵 key 的摘要、key ID、启用状态与工具/知识库授权，恒定时间比较；支持先发新 key、客户端切换、撤旧 key。仅记录 key ID。不得把小豆包用户 Cookie、数据库或模型密钥传入此服务。

HTTP 入口处理 401/403/413/429；授权身份通过每请求上下文传递到所有工具与数据服务。`list_collections` 返回授权逻辑知识库，文档摘要受同样权限检查。物理存储名称由服务解析，不接受 Query 中 `collection:xxx` 等语法切换范围。

MCP调用者按地址和服务身份复用本进程HTTP连接池，不同进程各自维护；平台通过原Agent change的同步工具适配接入。Travel等待网络仍占专业槽并续租，不新建RAG Job Pool；无跨网络PG长事务。平台单实例锁适配归Agent change，不能仅删锁宣称多实例就绪。

初始可配置限额：查询期限 30 秒，服务端同时执行检索 8 个，Embedding/Rerank 共享模型并发各 2 个，超额直接限流；构建 worker 1 个。超时不能保证已进入原生库的 CPU 计算立即终止，迟到结果丢弃，资源许可在实际 worker 结束后释放。重连不自动重放写任务。

### D3. 工具契约与兼容

四个名称、已有字段与 `top_k`（默认 5、范围 1..20）保留；`collection` 对应逻辑知识库。查询工具可增加以下可选字段，老请求仍有效：

| 字段 | 定义 |
|---|---|
| `query_type` | 知识库后台登记的查询类型标签；未绑定回默认；只影响服务端既有绑定 |
| `time_context.query_issued_at` | 带时区的 RFC3339 原问题发起时间，由 Host 程序注入；省略使用服务接收时间并标注来源 |
| `time_context.target` | Agent 提取的目标日期或闭区间：`start`、`end`（ISO 日期）、`granularity=day/month/year`；保留原精度 |

知识库时区首版默认 `Asia/Shanghai`，可显式配置；内部时刻保存 UTC，业务日期保留原时区。日期起止颠倒、格式错误、非有限数值、空 query、超过 4,000 字符的 query 作为参数错误。Host 传未知算法控制参数时拒绝。省略 collection 使用已配置且已授权的默认库，不猜测其他可用库。

`query_policy_evidence` 保留 `schema_version=1`、`status=ok/no_knowledge/error`、`collection`、`fragments`。每个 fragment 保留 `id == chunk_id`、完整 `content`、有限 `score`、真实 `metadata.source`，添加真实 `document_id/document_version_id/chunk_set_id` 和 `location`。空片段时不能返回 ok。

可选顶层 `execution` 包含 `request_id/index_build_id/profile_version/visibility_revision/query_type/routing_source`、`actual_stages`、`degraded`、安全错误/警告代码；片段可带 `scores` 和 `temporal_match`。Go 现有 v1 解码器忽略新增顶层字段、保留 metadata，不依赖这些新字段即可消费证据。正常状态枚举不增 `partial`，用 `degraded=true` 表示部分成功。

`query_knowledge_hub` 继续提供展示文本、引用和现有返回约定，使用同一内部完整证据对象投影。`get_document_summary` 保留摘要/预览语义，不把预览冒充全文证据。远端 `source` 使用稳定文档引用和真实展示文件名，不暴露服务器绝对路径；内部路径保留在管理记录。

请求体上限初始 64 KiB，完整响应上限 1,000,000 字节，与现有 Go 解码边界协调。超过上限返回明确错误，不静默截断政策段落。对请求体、结构和工具契约的错误按 SDK 协议表达；业务依赖失败使用 `isError=true` 和安全错误码，不输出 traceback、凭据或供应方完整错误。

### D4. 自有管理存储与文档入口

为复用现有 SQLite 经验并控制首版部署复杂度，增加 RAG 自有 `data/db/rag_catalog.db`（WAL、迁移版本、事务）。不使用小豆包 PostgreSQL。单构建 worker 使用持久化任务和原子认领；并发检索读取快照。若未来需要多实例写入，再单独迁移管理库。

| 逻辑表 | 核心职责 |
|---|---|
| `knowledge_bases` | 逻辑 ID、默认 profile、活动索引、时区、可见性修订 |
| `retrieval_profiles` / `query_type_bindings` | 不可变配置快照及后台显式类型映射 |
| `processing_profiles` | 解析、分块、嵌入和检索文本构造配置及哈希 |
| `documents` / `document_versions` | 稳定来源身份、原文件位置、内容哈希、DIT、发布时间、删除状态 |
| `chunk_sets` / `chunks` | 处理配置、规范化原文引用、片段正文/检索文本、定位与时间事实 |
| `index_builds` / `index_build_items` | 语料清单、物理集合、模型/分词配置、构建和发布状态 |
| `jobs` | 导入、构建、清理、评测的持久化状态、幂等键和失败原因 |
| `api_keys` / `api_key_grants` | 摘要、身份、撤销和工具/知识库授权 |
| `evaluation_runs` / `evaluation_results` | 评测集/语料/策略快照与逐问题结果 |

文档原文件以内容哈希持久化在 `data/documents`，解析后规范化正文和块清单保存在版本目录。Dashboard 临时文件必须先归档，再按逻辑来源创建/定位 document；文件名不作为唯一身份，可显式更新已存在 document。DIT 是文档版本首次成功登记入库的时间，重建不更新；单独保存 `indexed_at/published_at`。

沿用 CLI 的 `--path/--collection/--config` 和管理页面，增加处理 profile 选择与持久化 job 状态。首版只启用 TXT/PDF；修正当前 UI 格式与内核不一致、忽略 PipelineResult、临时文件身份不稳等路径。管理页面首版仅回环或受控运维入口，普通检索 key 不授予文档写权限，不新增公开 MCP 写工具或任意 URL 抓取。

### D5. Milvus 存储和索引发布

一条记录代表一个处理版本中的 chunk，主键由索引构建和 chunk 身份确定，重试 upsert 幂等。每个独立构建使用一个物理 collection，包含整个知识库语料快照；不按单文档创建 collection，也不把不同分块实验混在相同 BM25 统计语料中。

| 字段组 | 核心字段 |
|---|---|
| 身份 | `record_id`、`knowledge_base_id`、`document_id`、`document_version_id`、`chunk_set_id`、`chunk_id`、`chunk_index`、`index_build_id` |
| 内容与溯源 | `content`、`retrieval_text`、`content_hash`、`document_title`、`source_uri`、`location` |
| 检索 | `dense_vector`、BM25 Function 输出的 `sparse_vector` |
| 时间/过滤 | `document_inserted_at`、可确定的条款有效范围、时间状态与 JSON `temporal_facts`、后台允许的元数据 |

稳定过滤字段显式建 schema，扩展信息放受控 JSON；首版关闭任意 dynamic field。Dense 初始复用本地 BGE-small-zh-v1.5、512 维；维度相同但模型版本不同也分开构建。BM25 从 `retrieval_text` 生成；分词器与词典配置参与构建哈希，中文 analyzer 在集成测试核验，不假定 Milvus 默认分词等同原 jieba。Milvus/PyMilvus 选用支持上述能力的同一稳定版本线，精确版本在技术复核锁定，避免采用未核验的新 schema 特性。

本轮候选锁定Milvus2.6.26 / PyMilvus2.6.17，精确官方依赖及未就绪环境见[统一复核记录](../refactor-agent-kernel-plan-execute/engineering-review.md)。选择中文jieba analyzer方向，必须用真实服务的分词/检索测试核验，不能只比较旧Python jieba输出。发布前完成flush/索引就绪/加载及两路语料校验，正常查询显式Strong一致性起步，避免默认有界陈旧读取使刚发布构建表现为空；性能优化不能暗改发布可见性。

默认 `recursive`（1000 字符/重叠 200）沿用现状；提供 `paragraph` 作为非 LLM 对照。两者均受实际 Embedding tokenizer 输入上限约束，超限须继续分块或明确拒绝，不让嵌入库静默截断片段。`retrieval_text` 可补充标题/章节，但 `content` 保持原文。位置锚定规范化原文版本、block IDs/字符区间；PDF 页码仅在解析器提供可靠页映射时记录，不编造。

Embedding目标revision为7999e1d3359715c523056ef9478215996d62a620，Reranker目标revision为953dc6f6f85a1b2dbfca4c34a2796e7dde08d41e。当前缓存BGE的max_seq_length=512；原配置另指向本地副本，仍须核对清单/hash及真实tokenizer。先持久化canonical原文及位置，再派生retrieval_text；现有LLM refinement/enrichment不得改写用于证据的content。成对Rerank输入也需计query＋正文总长度，不能只检查单片段字符数。

构建状态：`queued -> running -> validating -> ready -> published`，失败为 `failed`，未发布完成不得供普通查询使用。先校验两路片段覆盖、身份、向量维度、来源和配置，再以 SQLite 事务 CAS 更新活动指针。查询开始一次性固定活动构建/profile/visibility_revision，物理集合和检索器以不可变构建 ID 为键缓存，移除跨请求可变 current_collection 和全查询串行锁。

删除先提交 tombstone 和 visibility_revision，新查询由服务解析的可见文档清单限制两路召回及融合前候选；重排前去除不可见项。首版正确性优先，不以最终 Top-K 之后才过滤替代授权/删除规则；大语料过滤清单优化另行压测。删除后的索引回退同样应用当前 tombstone。已开始查询允许使用已固定快照，清理等待在用 pin 释放；历史评测快照仅通过管理权限显式访问。物理清理作为可重试 job，不把网络删除放进 SQLite 长事务。

查询固定快照与登记pin必须在同一catalog短事务完成；构建清理/发布在同一受控catalog中核对pin和当前指针，不能先读指针、离开事务后才登记pin。pin持续到实际原生worker退出，包含HTTP超时/取消后的仍在途计算；首版单服务进程内pin与catalog保护共同工作，服务重启后仅清理可证明已无旧worker的占用，不把另一个进程正在计算误判为可删。

实施细化：catalog持久query_pins标记service_boot_id与query_id，不建立异步检索Job；HTTP单服务持有跨进程OS文件锁，所有原生检索均为该进程的有界线程工作。构建worker另持单worker锁；清理只有在pin被实际完成回调释放，或取得旧service进程退出后OS释放的锁、证明旧boot已结束时才能回收。仅lease时间到或HTTP取消不是删除pin的证据。标准库Windows/POSIX文件锁封装及进程杀停测试纳入实施计划，避免内存pin无法保护另一个构建worker的清理。

### D6. 默认检索流水线与 Rerank

```text
请求上下文/授权 -> 知识库活动快照 -> Query 规范化和编码
 -> Dense(20) + BM25(20)，相同可见文档范围
 -> chunk_id 去重、RRF(k=60)，保留至多40个候选
 -> 配置的 Rerank 对候选评分
 -> 可选时间融合
 -> 最终 top_k（默认5，上限20）
 -> 完整证据对象 -> 各工具投影
```

候选量、RRF 常数、阶段上限属于 profile snapshot。Rerank 初始选本地 Cross-Encoder 适配，默认模型候选为多语言 `BAAI/bge-reranker-v2-m3`；实际部署必须显式配置可用模型路径/版本并做中文政策验收。当前本机只有 BGE Embedding，不能把未安装的 reranker 描述为已可用。启用模型缺失或维度/接口不兼容时 profile 发布失败；运行中阶段异常按 D8 降级。

复用现有 RerankerFactory/BaseReranker，确保输出每候选可追踪相关性分数与稳定并列排序，不在该层按最终5条提前截断。不同评分器使用版本化转换；有限值 min-max 归一化到 [0,1]，全相等保持同分且由稳定次序打破，不能把 raw Cross-Encoder 分数当概率或直接与时间相加。

### D7. 时间融合的首版可执行定义

该模块是参考论文的 `timelyrag-adapted-v1`，不是对作者仓库或论文实验结果的完整复现。论文有效期模型与作者当前点时间/启发式代码存在差别，须分别引用并固定实现版本；不把新鲜度排序或手工权重叫已验证的原论文收益。

QIT 来自 Host 原问题时间，无则接收时间；QET 来自 Agent 的结构化 target，无则 `skipped_no_target`。DIT 为该文档版本实际首次入库时间；发布时间和重建时间另存。DET 从明确条款时间、可信元数据或后台人工确认获得，保存文本位置、粒度、提取方法和可信状态；首版不新增在线时间提取 LLM。未知不得转换为无限有效期。

时间距离按日期粒度、知识库时区计算；目标和条款可为闭区间，区间相交距离0，不相交为最近边界距离。该区间扩展在算法版本中明示。对已知时间使用：

`delta = gap(QET, DET) + lambda1*gap(QIT, DIT) + lambda2*gap(QIT, DET) + lambda3*gap(QET, DIT)`。

默认三个辅助权重均为0.1；候选集内 min-max 后 `T=exp(-gamma*z)`，初始 `gamma=2`。与归一化 Rerank 分数按 `final=(1-w)*R+w*T` 融合，初始 `w=0.3`，仅是待评测的工程基线，未声称论文默认或最佳值。权重可后台配置，首版不引入另一模型估计权重。参数与日期粒度进 snapshot。

已知 DET 的候选参与时间相对排序；缺 DET 的候选保留归一化 R 分数、标记 unknown，不把 DIT 冒充有效期。全部缺失时整阶段跳过；仅一个已知时间候选或所有 delta 相等时标记 `non_discriminative` 并跳过，以免平白抬高时间分数。`temporal_match` 单独为 matched/mismatched/unknown/not_requested，软分数不能代替有效性判断。

首版默认软排序；明确有效期硬过滤仅作为后台实验配置。历史问题不因 DIT > QET 被过滤。重放过去系统知识范围时才应用显式的 `DIT <= QIT` 因果约束，且只表示入库可用性；若将来要查询“现实中当时已公布的资料”，需使用独立发布时间/资料截止契约，不借用入库时间实现。异常未来 QIT 的普通请求拒绝，历史时间重放走管理评测入口。

正常 profile 可启用时间能力，实际只在提供 QET 且存在足够可信时间信号时执行。Agent 仍承担时间解析、补查及最终适用性理解，RAG 返回各项差值、原始/归一化/融合分数和执行原因供内部 Trace，Host 取得必要版本及时间状态。

### D8. 失败、期限和后台任务

Dense/BM25 两路都失败 -> `error`；两路成功但无候选 -> `no_knowledge`。一条失败而另一条有结果 -> 默认允许单路降级并返回 `degraded=true`、失败阶段及实际路径。Rerank 运行失败 -> 保留融合次序并标明降级；时间计算失败 -> 保留相关性次序并标明降级；明确缺 QET/DET 属于可解释的阶段跳过而非故障。回退不能返回编造的分数或仍声称完整策略已运行。

一路失败而另一条成功但为空时返回error：尚不能证明整个授权知识库没有命中。Rerank失败时跳过依赖该分数的时间融合、保留融合次序，不造重排分数。现有_perform_search吞异常问题已通过注入双路故障复现，必须改内部阶段结果类型和所有四工具的错误投影，而非只在最外层catch中换文案。CoreReranker和工具层不能在时间融合之前按最终top_k截断。

CrossEncoder当前timeout字段并未约束model.predict。实现需有界worker/实际结束回调管理Embedding/Rerank许可；HTTP返回超时或取消后仍计算的worker不得提前释放槽位或无界堆积新工作。长条款、重复chunk_id、空来源、非有限score和过大完整响应要明确拒绝，异常细节映射为安全错误码，不把str(exc)原样传给远端。

构建和评测保存在jobs，由单worker执行；Dashboard/CLI返回job ID并轮询。幂等键含库、文档/语料和配置哈希；重启先核对实际Milvus写入，不凭状态发布。清理失败保留tombstone。HTTP调用方停止、系统期限、连接断开分别记录，不把断开等同已停止所有计算，不新增平台用户整轮取消。

### D8.1 同步工具与恢复边界

平台固定tool_calls逻辑身份和参数，MCP调用在PG事务外，结果返回后将调用成功及证据同事务保存。恢复优先复用平台已保存证据；响应尚未可靠保存就断连/宕机时，原只读查询允许预算内必要重试，可能重新检索，不承诺RAG服务器必然已持久化结果或零重算。

服务保持每调用授权/期限/索引快照和在用pin隔离；在途计算结束前不回收资源，重启后不恢复不存在的HTTP连接或独立检索任务。构建/评测jobs仍按原自有持久恢复规则处理。

认证沿用HTTPS与服务API Key/知识库grant，不增加Kafka凭据或HMAC消息签名。MCP工具适配层负责有界整次检索重试，总期限包含全部尝试；RAG内部阶段不再独立叠加重试，按原降级/错误规则返回。HTTP/MCP协议request ID与平台逻辑call_key分开，具体可信传输关联由SDK适配核对，不扩大模型参数Schema。

### D9. 消融与完成证据

后台实验可选择 Dense-only、BM25-only、Hybrid、Rerank 开关、时间开关和不同 chunk set；正常查询指针不受实验改变。首轮重点是同一混合候选生成配置下 Rerank × 时间的四组，再比较分块与两路召回。所有组固定 Agent 改写后的 Query、QIT/QET、语料快照、模型及评测集版本；报告实际降级，不能把失败/缺失组剔除后宣称全部有效。

gold 标注使用 document_version + canonical block/字符范围，chunk ID 只作同一索引内辅助。报告候选 Recall、证据范围覆盖、nDCG/MRR、最终错时间版本比例、p50/p95 延迟及阶段失败率。若使用候选池评分，固定候选池大小并记录 pool hash，避免把召回变化误归因于时间融合。时效收益与质量阈值依据固定人工 gold 报告，不预承诺优于现有系统。

## Risks / Trade-offs

- [本地缺少 Milvus 与 Rerank 模型] -> 实施前锁定依赖并核验健康、模型可加载及中文数据；单元测试通过不等于真实检索验收。
- [首版 SQLite 单写入能力有限] -> 单构建 worker、短事务、WAL；不承诺 RAG 多副本写入，未来扩容单独设计。
- [每构建一个 collection 有资源成本] -> 后台显式清理未引用构建，保留活动/在用/评测 pin；不按文档滥建集合。
- [辅助时间距离惩罚晚入库历史资料] -> 保留 matched 状态、提供 lambda 消融与错误版本指标，允许后台调整，不把时间分数当合规判定。
- [中文分词与模型截断影响实验] -> 分词、词典和 tokenizer 版本入构建清单，验证原文覆盖与输入长度。
- [原有路径耦合 Chroma] -> 改造 Dashboard、列表、摘要、删除和评测等所有读取者；对照测试保护已有四工具契约。
- [同步检索占专业槽] -> 用共享检索并发、短总期限和调用计量约束；验证连接池、断连/服务重启与已存结果复用。General→Travel仍可靠异步交接；不偷偷重加RAG Job或结果Outbox。

## Migration Plan

1. 盘点现有未提交修改和文档清单，备份管理记录，保留旧数据库/索引/原文件，先完成正式实施计划再 apply。
2. 引入新的管理库迁移和服务配置，导入可信原文件及时间信息；不猜测缺失版本或原文。
3. 构建隔离 Milvus collection，核验片段来源、两路语料一致、模型与时间字段。
4. 运行现有单元测试、MCP/数据库集成测试、跨进程并发、更新删除、失败恢复、消融与真实依赖报告。
5. 显式发布默认混合 profile 和通过校验的活动索引，再启用远端入口；stdio 仅按运维配置继续使用。
6. 回退时恢复已有健康索引/profile 指针和服务配置，保持 tombstone；旧 Chroma 回退另用保留配置，不能静默伪装当前 Milvus 成功。

## Validation Strategy

- MCP：四工具 schemas/v1 完整正文、HTTP 401/403/限流/协议错误、JSON-only 生命周期、旧参数、新可选字段、多进程授权隔离。
- 平台同步接入：Travel保持running/续租并占槽、无RAG Job/Topic；API Key/越库、连接池并发、调用总期限、断连/服务重启、平台证据提交前后故障、重试次数及结果复用；对应Agent工程协议V2/V6/V8/V10。
- 索引：同文件不同处理配置、不同临时路径、模型维度/版本隔离、中文 BM25、构建中断和恢复、切换 pin、删除后新查询与回退。
- 检索：默认/未知 query_type 都走同一混合；候选不会按 final_k 提前截断；阶段失败真实降级；时间缺失、不相交、区间、同分和晚入库历史资料。
- 评测：gold 原文范围跨分块稳定，Query/时间/profile/语料清单可重放，四组消融及失败计入；指标有实际输出证据。
- 详细实施命令在后续writing-plans按任务/文件/依赖展开。本轮已运行66项现有单元基线、隔离HTTP SDK探针及错误复现，范围/限制见[复核记录](../refactor-agent-kernel-plan-execute/engineering-review.md)；未运行真实Milvus、Reranker、迁移、检索消融或产品OpenSpec verify。

## Open Questions

精确候选版本/模型revision和固定SDK接法已记录；新增依赖尚未安装编译，镜像digest、完整依赖锁、模型本地路径/可加载性及隔离环境未验收。服务地址、TLS与Key由部署配置注入，缺失时显式不可用，不自动回退stdio或改算法。上述环境和实际验证门槛不改变既有产品行为；若发现兼容范围需调整，先协调规格再实施。

## References

- [MCP 2025-11-25 transport](https://modelcontextprotocol.io/specification/2025-11-25/basic/transports)：本机 Python SDK 的验收协议基线。
- [Milvus 数据模型](https://milvus.io/docs/schema-hands-on.md)、[BM25 Function](https://milvus.io/docs/bm25-function.md)：能力参考，部署版本须复核。
- [TimelyRAG 论文](https://arxiv.org/abs/2609.11572)、[作者时间评分实现](https://github.com/kaist-dmlab/TimelyRAG/blob/main/time_scores.py)：区分论文公式、作者实现与本服务工程适配。
- [BGE Reranker 模型卡](https://huggingface.co/BAAI/bge-reranker-v2-m3)：模型候选能力参考，尚未在本机加载验证。
