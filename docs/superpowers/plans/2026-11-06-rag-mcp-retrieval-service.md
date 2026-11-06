# 独立 RAG HTTP MCP 与检索服务 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: 用户选择单代理时使用 superpowers:executing-plans；选择多子代理时使用 superpowers:subagent-driven-development。Steps use checkbox (`- [ ]`) syntax for tracking。用户已选择单代理，不派子代理。

**Goal:** 复用现有独立Python RAG，以远端同步MCP、自主管理文档/索引、统一混合检索、重排/可选时间融合及可追溯评测交付完整证据。

**Architecture:** RAG只管理自己的SQLite Catalog、原文件、Milvus构建、授权及评测；同步查询不建平台Job或Kafka链路。四工具共用每请求不可变身份/build/profile/visibility，完整候选在重排/时间融合后取Top-K；HTTP取消与原生worker/pin生命周期分离。

**Tech Stack:** Python3.12支持基线、MCP1.30.0/协议2025-11-25、uvicorn0.54.0、starlette1.7.0、SQLite WAL、Milvus2.6.26/PyMilvus2.6.17、已有Loader/Splitter/Reranker/EvalRunner与固定BGE模型revision。

**Spec:** [原specs](../../../openspec/changes/refactor-rag-mcp-retrieval-service/specs/)、[design](../../../openspec/changes/refactor-rag-mcp-retrieval-service/design.md)、[tasks](../../../openspec/changes/refactor-rag-mcp-retrieval-service/tasks.md)、[统一技术复核](../../../openspec/changes/refactor-agent-kernel-plan-execute/engineering-review.md)。平台侧按[Agent计划](2026-11-06-agent-runtime-travel.md)，本计划不重新定义平台运行协议。

日期：2026-11-06。状态：单代理实施中，R00配置代码已推进，完整锁及真实依赖仍未完成。原仓库为 `G:/简历项目/MODULAR-RAG-MCP-SERVER`，本次独立实现目录为 `G:/简历项目/.worktrees/rag-runtime-v1`，本计划及正式OpenSpec保存在小豆包仓库，外部RAG不初始化第二套OpenSpec。文件列表默认相对RAG根。Windows命令先设置 `$ragPython` 为已确认隔离venv的绝对解释器路径；当前已有解释器是 `G:/简历项目/MODULAR-RAG-MCP-SERVER/.venv/Scripts/python.exe`，R00新隔离环境建成后使用其路径。Linux CI用对应venv解释器逐条运行相同命令，不把系统python当验证环境。

## Global Constraints

- 独立同步HTTP MCP `/mcp`、JSON、API Key、远端HTTPS/显式回环开发HTTP；不增加OAuth、RAG SSE进度或公开写工具；stdio仅显式运维入口。
- 保留四工具名称、旧有效参数和政策schema_version=1/status=ok|no_knowledge|error；完整正文/真实source/location，不以摘要代证据。
- RAG不连接平台PG/Redis/Kafka，不写Run/Job/计划，不启RAG专业Agent或异步检索结果Outbox；Query改写/QET提取由Agent负责。
- top_k默认5、1..20；query最多4000字符，请求64KiB、完整响应1,000,000字节；超过明确失败、不截条款。
- 单HTTP服务、单构建worker；检索最多8，Embedding/Rerank各2，总查询期限30秒且受上游剩余期限约束。HTTP取消不代表原生计算停止或可以释放pin/槽。
- SQLite WAL自有Catalog；原文件归档，文档/版本/chunk_set/build分开，DIT不因重建改写；首版仅TXT/PDF。
- 默认recursive1000字符/重叠200，paragraph作对照，均遵守真实tokenizer输入上限。content为原文，retrieval_text可派生增强，不能静默截断。
- 每构建独立Milvus collection、Dense512/BM25同语料与权限；完成校验再CAS发布，查询固定build/profile/visibility并持pin；删除tombstone，回退不复活。
- 正常默认Dense20＋BM2520→RRF(k=60)至多40→Rerank→可选时间→最终top_k；已启用query_type都同Hybrid、未知回默认，Host不能覆盖算法。
- 时间为timelyrag-adapted-v1：四时间分开，三个lambda=0.1、gamma=2、w=0.3；缺QET/可靠DET、同分/单候选按design跳过；软分数不等于有效性。
- 两路失败error、两路成功空no_knowledge；一路失败另一条空仍error，有结果单路才降级；Rerank失败保留RRF且跳过依赖时间融合。完整实际路径记录。
- 评测保留失败/降级，gold为文档版本＋原文范围，四组固定Query/时间/语料/候选；不承诺论文收益或固定提速。
- 原WIP/Chroma/JSON BM25/SQLite/文件与旧配置保留；不覆盖用户改动，不运行有损迁移或自动下载模型/安装系统服务。

## Review Focus

1. HTTP等待被取消但native线程仍阻塞时，查询槽、模型许可及pin必须持续持有，不能无限排新线程。R04/R07。
2. 删除/回退与查询开始交错时，两路召回都排除新不可见文档，旧查询资源仍在。R03/R04/R05。
3. 中文标题/粗段落/PDF无页映射经过不同切分，证据保真且512 token边界真实，gold不随chunk ID漂移。R02/R09。
4. 第六名候选以及缺DET/晚入库历史政策不能因提前top5或用DIT代有效期而被误处理。R06/R09。
5. 用不同Key并发查询与列库/摘要/未知query_type时，所有工具遵守相同授权，异常不能变无命中或成功横幅。R05/R07/R08。

## 执行顺序与跨仓交付

- R00→R01→R02→R03→R04→R05→R06→R07构成可独立调用的完整服务；R08管理、R09迁移/消融、R10最终验收随后完成。
- Agent A15在R07通过后运行一次真实Travel→RAG联合故障测试，R10引用其报告；Agent A16在R10后收尾。RAG单元/索引/HTTP测试不依赖Agent A16，避免循环等待。
- R00依赖/模型真实就绪未完成时，只做无需该依赖的单元工作；带--require-integration的脚本必须把缺失/零用例/SKIP当失败。不得拿MCP固定fixture当Milvus或中文模型通过。
- 每任务先写测试并确认正确红灯，再最小实现、绿灯、整理和复测。数据库均使用临时测试Catalog/独立Milvus构建前缀；不指向用户已有data/db或旧集合。
- 原tasks是交付状态，本计划checkbox只是执行记录。任务完成保存实际命令/退出码/配置与语料hash/依赖/失败原始输出，证据在RAG docs/reports/runtime-v1，正式状态回写小豆包原change。
- 采用与Agent计划同一执行方式和隔离/WIP保护约束；不在RAG仓库添加另一套OpenSpec或竞争规格。每任务只提交相对起始基线的增量，先审diff/选择hunk，不把已有修改文件整份自动提交。
- 所有多条命令逐条执行并检查退出码；PowerShell任一失败立即停止，不让后一个成功掩盖前一个失败。最终全量检查只在前述任务有真实完成证据后进行。

## 实施任务

`$ragProfile`为R00生成/核验的 `G:/简历项目/MODULAR-RAG-MCP-SERVER/.cache/runtime-v1/config.yaml`，不默读旧config或.env。

### R00：独立环境、精确依赖与服务就绪门槛

**OpenSpec mapping:** 原RAG 1.1、2.1、7.1；rag-mcp-access/可复现远端接入。
**Depends:** 用户审阅两计划并选择执行方式；共用Agent A00的环境清单格式，不要求A00业务代码。

**Files:**
- Create: src/core/runtime_settings.py
- Create: tests/unit/test_runtime_preflight.py
- Modify: pyproject.toml
- Create: requirements-runtime-v1.lock
- Create: config/runtime_v1.example.yaml
- Create: scripts/runtime_preflight.py
- Create: scripts/verify_runtime.py
- Create: tests/unit/test_runtime_settings.py
- Modify: src/core/settings.py

**Interfaces:**
- Consumes: 原RAG design D2/D5/D6及Agent engineering-review固定SDK/镜像digest/模型revision；现有外部仓库WIP。
- Produces: settings.load_runtime_settings(path: Path) -> RuntimeSettings；RuntimeSettings包含catalog_path/archive_path/transport/allowed_hosts/allowed_origins/secret_env_refs/Milvus地址、默认KB/profile、模型路径/revision、tokenizer边界、资源/期限上限。runtime_preflight.py --config PATH --require milvus,embedding,reranker --report PATH；verify_runtime.py --config PATH --case R编号或All --require-integration，必做项缺依赖或SKIP为非零退出。

- [ ] **Step 1 — 先写有意义的失败测试：** test_runtime_requires_explicit_models_and_finite_limits；test_runtime_refuses_business_pg_or_kafka_settings；test_runtime_does_not_log_secrets。下列断言写入该任务测试；场景准备、屏障和故障点使用列出的fixture/消费接口，不访问真实用户数据。
```python
assert settings.query_timeout_seconds == 30
assert settings.query_concurrency == 8
assert settings.embedding_concurrency == settings.rerank_concurrency == 2
with pytest.raises(ValueError): load_runtime_settings(config_without_model_revision)
assert fake_api_key not in captured_output
```
- [ ] **Step 2 — 运行并确认红灯根因：**
```powershell
& $ragPython -m pytest tests/unit/test_runtime_settings.py -q
```
预期：FAIL，现有Settings没有catalog/HTTP授权/版本化模型与有界worker字段，或接受混入平台PG/Kafka及未界定模型配置；真实环境缺失单列，不混成业务红灯。
- [ ] **Step 3 — 最小实现：** 复用Settings加载但新增RuntimeSettings隔离新/旧profile；固定Python3.12支持基线、mcp1.30.0、uvicorn0.54.0、starlette1.7.0、PyMilvus2.6.17与Milvus2.6.26，保留已验证Embedding/Rerank库版本并在隔离venv解析兼容集合。pip安装报告记录轮文件/哈希，再freeze完整依赖锁；不把仅pyproject范围声明当精确锁。Embedding revision7999e1d3359715c523056ef9478215996d62a620、Reranker953dc6f6f85a1b2dbfca4c34a2796e7dde08d41e，核对本地文件清单、tokenizer与可加载性。预检只报告缺项，不自动下载模型/安装系统服务/改旧配置或把RAG接到平台业务库。
- [ ] **Step 4 — 使用Step 2同一命令复测并整理：** 目标测试PASS、无意外SKIP；仅在新变化/失败表明有必要时扩大回归范围。
完成条件：配置单元通过、隔离依赖可解析；真实Milvus/模型就绪分别记录。模型缺失保持未通过，纯单元任务可做，真实集成/默认profile发布不可虚报成功。
- [ ] **Step 5 — 审查与记录：** 核对Spec/接口/差异及实际输出，更新原tasks映射项和本计划步骤；按所选执行方式复审后，仅提交本任务增量（建议提交消息：`feat(rag): R00 独立环境、精确依赖与服务就绪门槛`）。

### R01：Catalog迁移、稳定文档身份与持久管理任务

**OpenSpec mapping:** 2.1；rag-knowledge-indexes/独立文档管理、稳定身份、四类时间、非破坏迁移。
**Depends:** R00依赖/配置门槛；SQLite不依赖平台库。

**Files:**
- Create: src/catalog/models.py
- Create: src/catalog/store.py
- Create: src/catalog/migrations/001_catalog.sql
- Create: src/catalog/jobs.py
- Create: src/catalog/archive.py
- Create: tests/unit/test_catalog_identity.py
- Create: tests/integration/test_catalog_migration.py

**Interfaces:**
- Consumes: R00 RuntimeSettings、design D4完整逻辑表及原始文件；旧SQLite/Chroma/JSON BM25只作为保留的legacy来源。
- Produces: Catalog.migrate() -> None；register_document(kb_id: str, source_key: str, archived_path: Path, content_hash: str, inserted_at: datetime) -> DocumentVersion；submit_job(kind: str, idempotency_key: str, arguments: dict) -> JobReceipt；claim_job(worker_id: str) -> JobLease | None。models定义KnowledgeBase/DocumentVersion/ProcessingProfile/RetrievalProfile/CorpusManifest/BuildSnapshot/JobReceipt/JobLease/QueryPin，字段逐一对应D4/D5的身份、版本、状态及快照。Archive.store(path: Path) -> ArchivedFile{content_hash,path,size,display_name}。 Catalog.register_chunk_set(document_version_id: str, processing_profile_id: str) -> str以不可变双版本组合返回稳定chunk_set_id，供R02消费。

- [ ] **Step 1 — 先写有意义的失败测试：** test_same_file_different_profiles_not_skipped；test_temp_paths_do_not_change_identity；test_dit_survives_rebuild；test_catalog_migration_and_job_claim_idempotent。下列断言写入该任务测试；场景准备、屏障和故障点使用列出的fixture/消费接口，不访问真实用户数据。
```python
assert first.document_version_id == second.document_version_id
assert first_profile_chunk_set != second_profile_chunk_set
assert rebuilt.document_inserted_at == original.document_inserted_at
assert len(concurrently_claimed_unique_jobs) == 1
```
- [ ] **Step 2 — 运行并确认红灯根因：**
```powershell
& $ragPython -m pytest tests/unit/test_catalog_identity.py tests/integration/test_catalog_migration.py -q
```
预期：FAIL，现有file_hash/临时路径模型无法区分文档版本/处理配置，且无目标Catalog迁移/持久任务。
- [ ] **Step 3 — 最小实现：** 创建独立rag_catalog.db，WAL、外键和短写事务、每线程连接；迁移版本幂等。原文件按内容hash归档，逻辑document由KB/source_key定位，内容相同与显式更新保留既定身份；chunk_set由document_version＋不可变processing profile唯一，job key含库/语料/配置hash。DIT首次成功登记一次，发布/重建时刻另存。jobs管理导入/构建/清理/评测，不为同步检索创建任务。持久claim/失败原因/恢复依据，未知阶段先对账。
- [ ] **Step 4 — 使用Step 2同一命令复测并整理：** 目标测试PASS、无意外SKIP；仅在新变化/失败表明有必要时扩大回归范围。
完成条件：相同原文不同分块、不同临时路径、并发受理/worker退出、DIT保留、迁移重跑及原文件未丢失均通过。
- [ ] **Step 5 — 审查与记录：** 核对Spec/接口/差异及实际输出，更新原tasks映射项和本计划步骤；按所选执行方式复审后，仅提交本任务增量（建议提交消息：`feat(rag): R01 Catalog迁移、稳定文档身份与持久管理任务`）。

### R02：原文保真、TXT/PDF与tokenizer受限分块

**OpenSpec mapping:** 2.2；rag-knowledge-indexes/可选处理策略和原文定位、四类时间。
**Depends:** R01；真实tokenizer来自R00已核对本地模型。

**Files:**
- Create: src/ingestion/canonical.py
- Create: src/ingestion/processing.py
- Create: src/libs/splitter/paragraph_splitter.py
- Create: src/ingestion/token_budget.py
- Create: tests/unit/test_canonical_chunks.py
- Create: tests/integration/test_tokenizer_chunk_bounds.py
- Modify: src/libs/loader/text_loader.py
- Modify: src/libs/loader/pdf_loader.py
- Modify: src/libs/splitter/splitter_factory.py
- Modify: src/ingestion/chunking/document_chunker.py
- Modify: src/ingestion/pipeline.py

**Interfaces:**
- Consumes: R01 DocumentVersion/ProcessingProfile/Archive；已有Loader.load(file_path)->Document与SplitterFactory；default recursive1000字符/重叠200；canonical条款来源与可信时间。
- Produces: CanonicalDocument{document_version_id,blocks,text,normalization_version}；CanonicalBlock{block_id,start_char,end_char,page_or_none,section_or_none,text}；ProcessingService.process(version: DocumentVersion, profile: ProcessingProfile) -> ChunkSetResult；TokenBudget.split_to_fit(text: str, tokenizer: object, prefix: str = '') -> list[CanonicalSpan]。CanonicalSpan保持原文字符范围；ChunkSetResult各chunk含content/retrieval_text/location及可信temporal_facts。

- [ ] **Step 1 — 先写有意义的失败测试：** test_full_text_and_offsets_survive_both_splitters；test_pdf_without_page_map_does_not_invent_pages；test_title_prefix_counts_toward_512_tokens；test_unsupported_format_is_rejected_before_archive_job。下列断言写入该任务测试；场景准备、屏障和故障点使用列出的fixture/消费接口，不访问真实用户数据。
```python
assert chunk.content == canonical.text[chunk.start_char:chunk.end_char]
assert len(tokenizer.encode(chunk.retrieval_text, add_special_tokens=True)) <= 512
assert page_without_reliable_mapping is None
```
- [ ] **Step 2 — 运行并确认红灯根因：**
```powershell
& $ragPython -m pytest tests/unit/test_canonical_chunks.py tests/integration/test_tokenizer_chunk_bounds.py -q
```
预期：FAIL，旧片段没有稳定canonical范围、paragraph未注册，或仅按字符长度导致真实tokenizer截断；红灯不接受下载模型失败作为证据。
- [ ] **Step 3 — 最小实现：** 保留TXT/PDF，canonical先归档/规范化并保存offset映射，再递归或段落切分；段落过长继续按token边界拆，确保全文覆盖不丢字符并保留重叠。标题/章节只进入retrieval_text且计入512输入上限，content始终原文，禁旧LLM refine改写证据。PDF无可靠页映射只用真实block/字符范围。QIT/QET由查询提供，DIT保留版本登记时间，DET仅来自可证明条款/元数据/人工确认并保留位置/精度/方法；未知不补永久有效。
- [ ] **Step 4 — 使用Step 2同一命令复测并整理：** 目标测试PASS、无意外SKIP；仅在新变化/失败表明有必要时扩大回归范围。
完成条件：固定长中文/混合字符/PDF/TXT经两种切分都能回原文并满足真实tokenizer；原文gold不绑定新chunk_id。
- [ ] **Step 5 — 审查与记录：** 核对Spec/接口/差异及实际输出，更新原tasks映射项和本计划步骤；按所选执行方式复审后，仅提交本任务增量（建议提交消息：`feat(rag): R02 原文保真、TXT/PDF与tokenizer受限分块`）。

### R03：不可变Milvus构建存储与中文双路语料

**OpenSpec mapping:** 3.1；rag-knowledge-indexes/Milvus索引包含一致语料。
**Depends:** R01、R02及R00真实Milvus/PyMilvus。

**Files:**
- Create: src/libs/vector_store/milvus_store.py
- Create: src/ingestion/storage/milvus_builder.py
- Create: tests/unit/test_milvus_schema_contract.py
- Create: tests/integration/test_milvus_hybrid_build.py
- Modify: src/libs/vector_store/vector_store_factory.py

**Interfaces:**
- Consumes: R01 CorpusManifest/BuildSnapshot，R02 canonical chunks、固定Embedding模型/revision；既有BaseVectorStore上层契约。
- Produces: MilvusStore.create_build(snapshot: BuildSnapshot) -> None；upsert_build(snapshot, records: list[dict]) -> BuildStats；search_dense(snapshot, vector: list[float], visible_versions: list[str], limit: int=20) -> list[RetrievalResult]；search_bm25(snapshot, text: str, visible_versions: list[str], limit: int=20) -> list[RetrievalResult]；validate_build(snapshot) -> BuildStats。沿用BaseVectorStore.upsert/query/get_by_ids有效接口并显式绑定不可变build，不用全局current_collection。 本任务在milvus_store.py定义BuildStats{chunk_count,dense_count,bm25_count,manifest_hash,model_revision}及ModelMismatch(ValueError)，供R04发布验证消费。

- [ ] **Step 1 — 先写有意义的失败测试：** test_milvus_schema_rejects_mixed_dimension_or_model；test_real_chinese_bm25_and_dense_share_manifest；test_upsert_replay_preserves_count_and_sources。下列断言写入该任务测试；场景准备、屏障和故障点使用列出的fixture/消费接口，不访问真实用户数据。
```python
assert dense_version_ids == bm25_version_ids == manifest.version_ids
assert reupserted.chunk_count == first.chunk_count
assert result.metadata["index_build_id"] == pinned_build_id
with pytest.raises(ModelMismatch): write_other_model_to_build()
```
- [ ] **Step 2 — 运行并确认红灯根因：**
```powershell
& $ragPython -m pytest tests/unit/test_milvus_schema_contract.py tests/integration/test_milvus_hybrid_build.py -q
```
预期：FAIL，现有Chroma/JSON BM25与VectorStoreFactory没有目标Milvus schema、BM25 Function及中文analyzer；只有mock返回成功不算此任务通过。
- [ ] **Step 3 — 最小实现：** 每完整KB构建一collection，不按文档建库；显式字段存身份/原文/检索文本/来源/时间/向量，禁任意dynamic field。Dense512维与BM25 Function同一manifest/可见范围，中文jieba analyzer纳入build hash并实际验证。record_id从build/chunk稳定生成，幂等upsert；模型维度相同但revision不同仍隔离。发布前flush、索引就绪和load，正常查询Strong一致性；对实际API签名/中文分析/维度/完整来源做服务集成，不改成Milvus Lite冒充目标服务。
- [ ] **Step 4 — 使用Step 2同一命令复测并整理：** 目标测试PASS、无意外SKIP；仅在新变化/失败表明有必要时扩大回归范围。
完成条件：真实Milvus中文BM25和Dense查询、两路语料一致、重复upsert、错误模型隔离与元数据/数量核对全部通过。
- [ ] **Step 5 — 审查与记录：** 核对Spec/接口/差异及实际输出，更新原tasks映射项和本计划步骤；按所选执行方式复审后，仅提交本任务增量（建议提交消息：`feat(rag): R03 不可变Milvus构建存储与中文双路语料`）。

### R04：构建发布、查询pin、删除回退与worker恢复

**OpenSpec mapping:** 3.2；rag-knowledge-indexes/构建完成后发布、删除与历史清理、非破坏迁移。
**Depends:** R01～R03。

**Files:**
- Create: src/catalog/builds.py
- Create: src/catalog/pins.py
- Create: src/catalog/process_lock.py
- Create: src/ingestion/build_worker.py
- Create: scripts/rag_worker.py
- Create: tests/unit/test_build_state_machine.py
- Create: tests/integration/test_build_publication_recovery.py
- Create: tests/integration/test_query_pin_process_lifetime.py
- Modify: src/ingestion/document_manager.py

**Interfaces:**
- Consumes: R01 Catalog/jobs、R03真实Milvus构建；design D5发布/可见性及OS进程锁收敛。
- Produces: BuildService.publish(build_id: str, expected_active_id: str | None) -> BuildSnapshot；rollback(kb_id: str, build_id: str, expected_active_id: str) -> BuildSnapshot；tombstone_document(kb_id, document_id: str) -> int；Catalog.pin_query(kb_id: str, query_id: str, service_boot_id: str) -> QueryPin；release_pin(pin: QueryPin) -> None；cleanup_build(build_id: str) -> CleanupResult。ProcessGuard.acquire_nonblocking(path: Path) -> GuardHandle，句柄持有至实际worker全部退出。

- [ ] **Step 1 — 先写有意义的失败测试：** test_partial_build_never_published；test_publish_and_pin_are_one_catalog_transaction；test_delete_then_rollback_does_not_resurrect；test_http_timeout_does_not_release_native_pin；test_dead_boot_pin_reclaimed_only_after_os_lock。下列断言写入该任务测试；场景准备、屏障和故障点使用列出的fixture/消费接口，不访问真实用户数据。
```python
assert active_build_id == (previous_id if partial_build_failed else validated_id)
assert running_query.index_build_id == old_pinned_id
assert deleted_document not in new_query.visible_versions
assert cleanup_while_native_worker_alive.status == "blocked"
assert stale_boot_reclaimed_only_after_process_exit
```
- [ ] **Step 2 — 运行并确认红灯根因：**
```powershell
& $ragPython -m pytest tests/unit/test_build_state_machine.py tests/integration/test_build_publication_recovery.py tests/integration/test_query_pin_process_lifetime.py -q
```
预期：FAIL，当前没有持久build发布CAS/pin/tombstone；仅内存pin不能阻止另一构建worker清理，过期lease也不能证明旧原生线程已退出。
- [ ] **Step 3 — 最小实现：** 构建queued→running→validating→ready→published，失败为failed；单构建worker用OS锁和持久claim，重启检查Milvus实际写入再恢复/验证，不凭状态直接发布。查询读build/profile/visibility并登记pin同一SQLite短事务，发布/删除/清理核对同一catalog。tombstone先提交再异步物理清理；删除后的回退仍过滤当前tombstone，不破坏评测pin。HTTP单服务OS锁＋service_boot_id证明旧进程是否结束，worker另锁；超时/取消不先删pin。Windows msvcrt/POSIX fcntl标准库封装，关闭时join实际线程；进程死后OS锁释放才可回收旧boot pin。
- [ ] **Step 4 — 使用Step 2同一命令复测并整理：** 目标测试PASS、无意外SKIP；仅在新变化/失败表明有必要时扩大回归范围。
完成条件：真实发布中断/指针CAS/查询版本固定/删除回退/清理失败重试和杀进程pin保护通过；SQLite写事务不包Milvus网络。
- [ ] **Step 5 — 审查与记录：** 核对Spec/接口/差异及实际输出，更新原tasks映射项和本计划步骤；按所选执行方式复审后，仅提交本任务增量（建议提交消息：`feat(rag): R04 构建发布、查询pin、删除回退与worker恢复`）。

### R05：每请求混合检索、完整证据与真实错误

**OpenSpec mapping:** 4.1、4.4；rag-retrieval-strategies/默认混合、不可变策略、Query改写归调用方；rag-mcp-access/证据/错误。
**Depends:** R01～R04。

**Files:**
- Create: src/core/retrieval_types.py
- Create: src/core/query_engine/context.py
- Create: src/core/query_engine/evidence.py
- Create: tests/unit/test_query_context_and_failures.py
- Create: tests/integration/test_query_scope_isolation.py
- Modify: src/core/query_engine/hybrid_search.py
- Modify: src/core/query_engine/fusion.py
- Modify: src/core/query_engine/query_processor.py
- Modify: src/mcp_server/tools/query_knowledge_hub.py
- Modify: src/mcp_server/tools/query_policy_evidence.py
- Modify: src/mcp_server/tools/list_collections.py
- Modify: src/mcp_server/tools/get_document_summary.py
- Modify: tests/unit/test_policy_evidence.py

**Interfaces:**
- Consumes: R04 QueryPin/BuildSnapshot及授权可见集合；R03双路适配；现有四工具v1名称与完整证据契约。
- Produces: retrieval_types定义Principal{key_id,tool_grants,kb_grants}、QueryRequest{query,collection,top_k,query_type,time_context}、QueryContext{principal,pin,profile,deadline,query_issued_at,target}、StageResult{status,candidates,error_code}、CandidateSet{candidates,actual_stages,degraded}、EvidenceResponse{schema_version,status,collection,fragments,execution}。RetrievalService.retrieve(request: QueryRequest, principal: Principal) -> EvidenceResponse；HybridSearch.search_context(context: QueryContext, request: QueryRequest) -> CandidateSet；EvidenceProjector.policy/display/summary明确投影同一事实。

- [ ] **Step 1 — 先写有意义的失败测试：** test_dual_failure_is_error_not_no_knowledge；test_single_failure_empty_is_error；test_request_collection_cannot_escape_grants；test_unknown_query_type_uses_default_hybrid；test_full_evidence_size_and_finite_score。下列断言写入该任务测试；场景准备、屏障和故障点使用列出的fixture/消费接口，不访问真实用户数据。
```python
assert both_failed.status == "error"
assert both_ok_empty.status == "no_knowledge"
assert dense_only.execution.degraded is True
assert candidate_count_before_rerank <= 40
assert unknown_type.execution.routing_source == "default"
assert long_policy_fragment == original_fragment
```
- [ ] **Step 2 — 运行并确认红灯根因：**
```powershell
& $ragPython -m pytest tests/unit/test_query_context_and_failures.py tests/integration/test_query_scope_isolation.py -q
```
预期：FAIL，先用已复现的HybridSearch抛ConnectionError路径写test_dual_failure...；旧_perform_search会给no_knowledge，应因状态断言失败而红，不能只mock最外层正确返回。
- [ ] **Step 3 — 最小实现：** 去掉共享_current_collection/全查询RLock，检索器缓存按不可变build/profile键；每请求固定授权KB和pin，Query中的collection/过滤语法不能扩大范围或变更默认算法。统一Dense20＋BM2520，chunk_id去重、RRF k=60，最多40候选保留到R06。所有正常类型绑定同一默认Hybrid，Host传profile/model/weights等未知字段拒绝。阶段类型区分成功空/失败/降级：两路失败error、两路成功空no_knowledge、一路失败另一路空也error；有结果单路可降级并报路径。完整source/location/版本/正文及有限score，拒重复chunk_id/空来源/过大wire响应，安全错误码替代str(exc)。 远端source使用稳定文档引用及真实展示文件名，metadata白名单不暴露服务器绝对路径。现有测试中对旧全局锁/私有current_collection的断言改为公开的每请求隔离断言，保留身份/完整正文/空与错区分等保护，不为让测试绿删掉回归。stdio运维入口由显式配置生成受限本地operator Principal，HTTP不能回退成该身份。
- [ ] **Step 4 — 使用Step 2同一命令复测并整理：** 目标测试PASS、无意外SKIP；仅在新变化/失败表明有必要时扩大回归范围。
完成条件：真实并发两KB不串身份/索引/策略；底层异常不再变无命中，四工具来源/列表/摘要边界一致，长条款完整。
- [ ] **Step 5 — 审查与记录：** 核对Spec/接口/差异及实际输出，更新原tasks映射项和本计划步骤；按所选执行方式复审后，仅提交本任务增量（建议提交消息：`feat(rag): R05 每请求混合检索、完整证据与真实错误`）。

### R06：候选全量重排、时间融合与真实模型边界

**OpenSpec mapping:** 4.2、4.3；rag-retrieval-strategies/融合后精细重排、可选时间融合。
**Depends:** R05；真实模型部分依赖R00模型就绪。

**Files:**
- Create: src/core/query_engine/temporal.py
- Create: src/core/query_engine/scoring.py
- Create: tests/unit/test_rerank_temporal_pipeline.py
- Create: tests/unit/test_temporal_scoring.py
- Create: tests/integration/test_reranker_chinese_runtime.py
- Modify: src/core/query_engine/reranker.py
- Modify: src/libs/reranker/base_reranker.py
- Modify: src/libs/reranker/cross_encoder_reranker.py
- Modify: src/libs/reranker/reranker_factory.py

**Interfaces:**
- Consumes: R05 CandidateSet/QueryContext；R00固定bge-reranker-v2-m3/tokenizer，原design D7算法及参数。
- Produces: ScorePipeline.rank(context: QueryContext, candidates: CandidateSet, top_k: int) -> EvidenceResponse；TemporalScorer.score(context: QueryContext, candidates: list[ScoredCandidate]) -> TemporalResult。ScoredCandidate保留原RRF序/finite rerank score/归一化R及真实时间；TemporalResult含executed/skipped_reason、各gap/delta/z/T/final和temporal_match；CrossEncoder适配对所有候选评分，不预取final_k。

- [ ] **Step 1 — 先写有意义的失败测试：** test_sixth_candidate_can_enter_final_five；test_rerank_failure_skips_dependent_temporal_score；test_temporal_formula_interval_timezone_and_missing；test_equal_or_single_known_delta_skips；test_chinese_model_input_and_output_are_finite。下列断言写入该任务测试；场景准备、屏障和故障点使用列出的fixture/消费接口，不访问真实用户数据。
```python
assert correct_initial_rank_six in final_five
assert known_z == [0.0, 1.0]
assert temporal_scores == pytest.approx([1.0, math.exp(-2.0)])
assert fused == pytest.approx(0.7 * normalized_r + 0.3 * temporal_t)
assert missing_det.temporal_match == "unknown"
assert no_qet.actual_stages["temporal"] == "skipped_no_target"
```
- [ ] **Step 2 — 运行并确认红灯根因：**
```powershell
& $ragPython -m pytest tests/unit/test_rerank_temporal_pipeline.py tests/unit/test_temporal_scoring.py tests/integration/test_reranker_chinese_runtime.py -q
```
预期：FAIL，旧重排在前五截断、timeout字段不约束predict、未实现时间模块；第六候选用固定评分器验证真实流水线的截断位置。真实模型缺失明确FAIL该集成项。
- [ ] **Step 3 — 最小实现：** 复用Factory/BaseReranker，评分转换版本化，有限值min-max；同分稳定次序，raw CrossEncoder分数不当概率。D7区间相交gap0，否则最近边界；QIT/QET/DIT/DET分开，delta四项辅助lambda均0.1，z后T=exp(-2*z)，final=0.7R+0.3T。无QET跳过、无DET标unknown并保留R；不足两个可信不同delta时non_discriminative跳过；不把DIT当有效期或过滤晚入库历史政策。默认软排序，明确不匹配单列状态；硬过滤只内部实验。Rerank失败保留RRF并跳过依赖它的时间融合，时间失败保留相关性且degraded。输入query＋正文按真实tokenizer核验，不静默截条款。
- [ ] **Step 4 — 使用Step 2同一命令复测并整理：** 目标测试PASS、无意外SKIP；仅在新变化/失败表明有必要时扩大回归范围。
完成条件：可手算数值/区间/跨时区/晚入库/未知/同分/阶段异常及第六候选回归通过；真实中文模型加载、输入长度和有限排序分数有单独报告，不预承诺质量提升。
- [ ] **Step 5 — 审查与记录：** 核对Spec/接口/差异及实际输出，更新原tasks映射项和本计划步骤；按所选执行方式复审后，仅提交本任务增量（建议提交消息：`feat(rag): R06 候选全量重排、时间融合与真实模型边界`）。

### R07：有界HTTP MCP、API Key授权与实际worker生命周期

**OpenSpec mapping:** 5.1、5.2、5.5；rag-mcp-access全部；Agent A15的对接前置。
**Depends:** R00～R06；HTTP基线使用固定SDK1.30.0。

**Files:**
- Create: src/mcp_server/http_app.py
- Create: src/mcp_server/auth.py
- Create: src/mcp_server/execution.py
- Create: src/mcp_server/limits.py
- Create: scripts/manage_keys.py
- Create: tests/unit/test_http_auth_and_limits.py
- Create: tests/integration/test_http_mcp_processes.py
- Create: tests/integration/test_native_worker_timeout.py
- Modify: src/mcp_server/server.py
- Modify: src/mcp_server/protocol_handler.py

**Interfaces:**
- Consumes: R05 Principal/QueryRequest/完整EvidenceResponse，R06评分，R04 pin/OS service锁，R00 Host/Origin/Key环境注入及限额。
- Produces: create_http_app(settings: RuntimeSettings, retrieval: RetrievalService) -> ASGIApp；KeyStore.authenticate(raw_key: str) -> Principal；authorize(principal,tool_name,kb_id) -> None；ExecutionLimiter.submit(context: QueryContext, fn: Callable[[], EvidenceResponse]) -> Awaitable[EvidenceResponse]，完成回调持有pin和许可直到实际Future结束。CLI明确--transport streamable-http或stdio，构建由独立rag_worker.py，不是检索Job。

- [ ] **Step 1 — 先写有意义的失败测试：** test_http_401_403_413_429_and_get_405；test_four_tools_old_and_optional_parameters；test_concurrent_clients_key_kb_isolation；test_cancelled_http_retains_native_slot_and_pin；test_key_rotation_does_not_leak_secrets。下列断言写入该任务测试；场景准备、屏障和故障点使用列出的fixture/消费接口，不访问真实用户数据。
```python
assert missing_key.status_code == 401 and forbidden_kb.status_code == 403
assert oversize_request.status_code == 413 and full_capacity.status_code == 429
assert get_mcp.status_code == 405
assert text_response.schema_version == 1
assert active_native_workers_after_http_timeout == 1
assert key_text not in response_or_logs
```
- [ ] **Step 2 — 运行并确认红灯根因：**
```powershell
& $ragPython -m pytest tests/unit/test_http_auth_and_limits.py tests/integration/test_http_mcp_processes.py tests/integration/test_native_worker_timeout.py -q
```
预期：FAIL，当前只有stdio和无授权全局工具；模拟取消后即释放许可的实现应在受阻原生线程测试失败，单独测asyncio取消不够。
- [ ] **Step 3 — 最小实现：** 原create_mcp_server/四工具注册复用，StreamableHTTPSessionManager stateless/json_response、ASGI lifespan唯一实例、显式GET405、Host/Origin不全局关闭；仅明确回环开发HTTP，远端HTTPS/代理TLS。Key高熵摘要/恒时比较、key_id审计、工具/KB grants逐调用校验，列表/摘要同边界；支持先新key后撤旧key。工具query≤4000字符、top_k默认5/1..20、request64KiB、完整SDK响应1,000,000字节，超限安全错误不截证据。最多8检索、Embedding/Rerank各2、有界线程池，超额立即429；总期限30秒并取调用剩余期限。同步计算用shield/实际Future回调，HTTP超时不释放真实线程/模型槽/pin；禁止无界to_thread排队。所有身份/KB/元数据来自每请求context，原str(exc)不外泄，普通检索key无管理写权限。 若工具返回structuredContent与TextContent双份机器正文，限制必须按最终SDK序列化wire字节检查；不能仅检查一份content。启用完整四工具grant的key发现四工具，其他key按工具grant过滤/拒绝，列库和摘要同样授权。
- [ ] **Step 4 — 使用Step 2同一命令复测并整理：** 目标测试PASS、无意外SKIP；仅在新变化/失败表明有必要时扩大回归范围。
完成条件：两个独立官方客户端进程完成初始化/发现/JSON调用、权限隔离/轮换/断连/原生迟到/重启；真实HTTPS路径有证据，回环HTTP不能冒充TLS验收。
- [ ] **Step 5 — 审查与记录：** 核对Spec/接口/差异及实际输出，更新原tasks映射项和本计划步骤；按所选执行方式复审后，仅提交本任务增量（建议提交消息：`feat(rag): R07 有界HTTP MCP、API Key授权与实际worker生命周期`）。

### R08：CLI与Dashboard接入Catalog，失败不得显示成功

**OpenSpec mapping:** 6.1；rag-knowledge-indexes/独立文档管理、删除与历史清理。
**Depends:** R01～R07。

**Files:**
- Create: src/observability/dashboard/services/administration.py
- Create: tests/unit/test_administration_job_status.py
- Create: tests/integration/test_admin_cli_dashboard.py
- Modify: scripts/ingest.py
- Modify: src/ingestion/pipeline.py
- Modify: src/ingestion/document_manager.py
- Modify: src/observability/dashboard/pages/ingestion_manager.py
- Modify: src/observability/dashboard/pages/data_browser.py
- Modify: src/observability/dashboard/pages/overview.py
- Modify: src/observability/dashboard/services/data_service.py

**Interfaces:**
- Consumes: R01 Archive/Catalog/jobs，R04 build publish/tombstone，R07与检索分离的管理边界；旧CLI --path/--collection/--config有效参数。
- Produces: AdministrationService.ingest(path: Path,kb_id: str,profile_id: str,source_key: str | None) -> JobReceipt；publish/rollback/delete操作委托R04；job_status(job_id: str) -> JobView；列表/详情/摘要由Catalog读取。CLI --processing-profile和显式--publish是管理参数，不开放给MCP Host。

- [ ] **Step 1 — 先写有意义的失败测试：** test_pipeline_failure_never_shows_success；test_cli_and_ui_support_only_txt_pdf；test_upload_archives_before_temp_file_removed；test_lists_summary_delete_use_active_catalog_and_grants。下列断言写入该任务测试；场景准备、屏障和故障点使用列出的fixture/消费接口，不访问真实用户数据。
```python
assert failed_job.status == "failed" and success_banner_count == 0
assert upload_options == {"txt", "pdf"}
assert archived_original.exists() and deleted_temp_path_not_used_as_document_id
assert tombstoned_document not in listed_documents
```
- [ ] **Step 2 — 运行并确认红灯根因：**
```powershell
& $ragPython -m pytest tests/unit/test_administration_job_status.py tests/integration/test_admin_cli_dashboard.py -q
```
预期：FAIL，用返回PipelineResult(success=False)且不抛异常的fake跑真实管理服务/页面适配；旧页面会出现成功文案，应明确断言失败。
- [ ] **Step 3 — 最小实现：** 上传先归档再创建导入job，UI展示受理/构建/校验/发布/失败，不把accepted显示成已索引。CLI与页面同一AdministrationService，仅TXT/PDF；保留文件展示名、不保留临时路径作身份。列表/统计/数据浏览/摘要/删除均从Catalog＋活动build读取，保留显式legacy只读入口，不依赖Chroma全局集合。发布/回退为受控管理动作，普通MCP key无权调用；管理界面仅回环或明确受控运维入口。
- [ ] **Step 4 — 使用Step 2同一命令复测并整理：** 目标测试PASS、无意外SKIP；仅在新变化/失败表明有必要时扩大回归范围。
完成条件：管理服务测试、实际CLI与Streamlit页面人工检查通过；失败/逻辑删除/回退状态一致，原文件保留，普通key无写能力。
- [ ] **Step 5 — 审查与记录：** 核对Spec/接口/差异及实际输出，更新原tasks映射项和本计划步骤；按所选执行方式复审后，仅提交本任务增量（建议提交消息：`feat(rag): R08 CLI与Dashboard接入Catalog，失败不得显示成功`）。

### R09：原文范围gold、消融与非破坏迁移证据

**OpenSpec mapping:** 6.2、7.1；rag-retrieval-strategies/后台消融和原文证据评测；rag-knowledge-indexes/非破坏迁移。
**Depends:** R00～R08。

**Files:**
- Create: src/observability/evaluation/evidence_gold.py
- Create: src/observability/evaluation/ablation.py
- Create: scripts/migrate_runtime.py
- Create: tests/fixtures/runtime_v1/gold.jsonl
- Create: tests/unit/test_evidence_gold_metrics.py
- Create: tests/integration/test_retrieval_ablation_replay.py
- Create: tests/integration/test_non_destructive_migration.py
- Modify: src/observability/evaluation/eval_runner.py
- Modify: src/observability/dashboard/pages/evaluation_panel.py
- Modify: scripts/evaluate.py

**Interfaces:**
- Consumes: R02 canonical版本/范围；R04不可变build/profile/visibility pin；R05/R06实际阶段和分数；保留原文件及旧存储清单。
- Produces: EvidenceGold{document_version_id,canonical_ranges,relevance,target_interval_or_none}；EvidenceEvaluator.evaluate(gold:list[EvidenceGold],response:EvidenceResponse) -> EvidenceMetrics；AblationRunner.run(snapshot: EvaluationSnapshot, variants: list[Variant]) -> EvaluationReport。EvaluationSnapshot固定Query/QIT/QET/语料/分块/模型/profile/候选池hash，variants含Hybrid下Rerank×时间四组及Dense/BM25/分块对照；migration.run(manifest: Path,publish: bool=False) -> MigrationReport。

- [ ] **Step 1 — 先写有意义的失败测试：** test_gold_survives_chunk_boundary_change；test_failed_and_degraded_variants_remain_in_report；test_ablation_uses_same_candidates_and_query_snapshot；test_missing_original_blocks_migration_without_deletion。下列断言写入该任务测试；场景准备、屏障和故障点使用列出的fixture/消费接口，不访问真实用户数据。
```python
assert cross_chunk_evidence_coverage == expected_original_range_coverage
assert report.variant_count == 4 and report.failed_variant_count == injected_failures
assert len({v.query_snapshot_hash for v in report.variants}) == 1
assert missing_original.status == "blocked" and old_chroma_files_unchanged
```
- [ ] **Step 2 — 运行并确认红灯根因：**
```powershell
& $ragPython -m pytest tests/unit/test_evidence_gold_metrics.py tests/integration/test_retrieval_ablation_replay.py tests/integration/test_non_destructive_migration.py -q
```
预期：FAIL，现有评测依赖expected_chunk_ids，换分块后gold身份不稳；故意让一组失败时旧聚合可能漏组或仍宣称全策略成功。
- [ ] **Step 3 — 最小实现：** 扩展而非删除旧GoldenTestCase/Trace消费，gold用真实规范化文档版本＋原文范围，记录人工标注依据。报告候选Recall/原文覆盖/nDCG/MRR/错误时间版本比例/p50p95/阶段失败和实际路径；四组用同一Hybrid候选生成，排序/时间开关变化不改Query。不同分块/召回另做对照，不混raw分数。不剔除失败/降级、不承诺优于旧系统。迁移从原文件构建隔离Milvus并核对来源/版本/片段/旧v1消费者，通过才显式发布；缺原文件/模型/依赖保持blocked，保留旧Chroma/JSON BM25/SQLite/文件，不直接迁旧向量冒充新模型。
- [ ] **Step 4 — 使用Step 2同一命令复测并整理：** 目标测试PASS、无意外SKIP；仅在新变化/失败表明有必要时扩大回归范围。
完成条件：真实模型/Milvus的四组及对照原始报告可复现，失败组保留；升级/发布/回退/删除兼容证据具备，旧数据未删除。
- [ ] **Step 5 — 审查与记录：** 核对Spec/接口/差异及实际输出，更新原tasks映射项和本计划步骤；按所选执行方式复审后，仅提交本任务增量（建议提交消息：`feat(rag): R09 原文范围gold、消融与非破坏迁移证据`）。

### R10：跨仓联合验收、全量审核与RAG归档

**OpenSpec mapping:** 5.3、5.4、7.2；全部RAG需求；Agent 3.3联合接入。
**Depends:** R00～R09及Agent A15一次联合报告；不依赖Agent A16归档。

**Files:**
- Create: tests/e2e/test_runtime_retrieval_contract.py
- Create: docs/runtime-v1-runbook.md
- Create: docs/reports/runtime-v1/verification.json
- Modify: scripts/verify_runtime.py
- Modify: README.md
- Create: .github/workflows/ci.yml
- Modify: G:/简历项目/小豆包/openspec/changes/refactor-rag-mcp-retrieval-service/tasks.md

**Interfaces:**
- Consumes: R07 HTTP服务/原生资源边界、R09评测与迁移报告、Agent A15真实Travel运行/续租/无RAG Job及证据提交前后崩溃报告。
- Produces: verify_runtime.py --config PATH --case All --require-integration报告，引用A15报告hash/用例，不重复造平台实现；原RAG change的任务/场景/测试证据矩阵及实际archive结果。

- [ ] **Step 1 — 先写有意义的失败测试：** test_runtime_retrieval_old_v1_and_complete_evidence；test_runtime_key_revocation_and_native_cleanup；test_runtime_rollback_keeps_tombstone_and_history。下列断言写入该任务测试；场景准备、屏障和故障点使用列出的fixture/消费接口，不访问真实用户数据。
```python
assert policy.schema_version == 1 and policy.status in {"ok", "no_knowledge", "error"}
assert not runtime_report.required_skips and runtime_report.all_required_passed
assert joint_report.travel_holds_lease_during_rag and joint_report.rag_job_count == 0
assert rollback_did_not_restore_deleted_document
```
- [ ] **Step 2 — 运行并确认红灯根因：**
```powershell
& $ragPython -m pytest tests/unit -q
if ($LASTEXITCODE -ne 0) { throw '上一项验证失败' }
& $ragPython scripts/verify_runtime.py --config $ragProfile --case All --require-integration
```
预期：FAIL，先写最终兼容/撤回/回退回归，未实现路径应在对应行为断言红；验收审查不人为制造失败。缺A15联合报告明确未完成，不用纯Python单元替代平台接缝。
- [ ] **Step 3 — 最小实现：** 审核原tasks每项代码/测试/真实依赖/输出，不只看checkbox；运行所有相关单元、集成、进程、页面及真实检索评测/迁移检查，读取输出，修复重要问题再重验。按所选执行方式完成代码审查。回到小豆包唯一正式OpenSpec核对设计/规格/任务，CLI有verify工作流时实际运行，无则说明并人工逐条核对，不以validate替代。RAG完成后在原项目归档原RAG change，确认同步文件进入项目工作分支；外部RAG代码保留独立提交/commit hash，两者互相引用。之后Agent A16完成整体交付/归档；最后按用户选择合并/PR/保留分支，不自动合并或推送。
- [ ] **Step 4 — 使用Step 2同一命令复测并整理：** 目标测试PASS、无意外SKIP；仅在新变化/失败表明有必要时扩大回归范围。
完成条件：RAG全部任务有真实证据且无必要失败/遗漏，联合接入被A15证明，规格核对和代码审查完成后才archive；未运行项明确未验证，不归档成完成。
- [ ] **Step 5 — 审查与记录：** 核对Spec/接口/差异及实际输出，更新原tasks映射项和本计划步骤；按所选执行方式复审后，仅提交本任务增量（建议提交消息：`feat(rag): R10 跨仓联合验收、全量审核与RAG归档`）。

## 规格覆盖与计划自审

下表逐项对应原change的有效Requirement；具体场景的红灯测试、边界和完成条件见对应任务。它是执行索引，不替代原specs。

| 原能力与Requirement | 实施及验证任务 |
| --- | --- |
| [rag-knowledge-indexes](../../../openspec/changes/refactor-rag-mcp-retrieval-service/specs/rag-knowledge-indexes/spec.md) / 独立文档管理 | R01、R08 |
| [rag-knowledge-indexes](../../../openspec/changes/refactor-rag-mcp-retrieval-service/specs/rag-knowledge-indexes/spec.md) / 文档和处理结果具有稳定身份 | R01、R02 |
| [rag-knowledge-indexes](../../../openspec/changes/refactor-rag-mcp-retrieval-service/specs/rag-knowledge-indexes/spec.md) / 可选处理策略和原文定位 | R02 |
| [rag-knowledge-indexes](../../../openspec/changes/refactor-rag-mcp-retrieval-service/specs/rag-knowledge-indexes/spec.md) / Milvus 索引包含一致语料 | R03 |
| [rag-knowledge-indexes](../../../openspec/changes/refactor-rag-mcp-retrieval-service/specs/rag-knowledge-indexes/spec.md) / 构建完成后发布 | R04 |
| [rag-knowledge-indexes](../../../openspec/changes/refactor-rag-mcp-retrieval-service/specs/rag-knowledge-indexes/spec.md) / 删除与历史清理 | R04、R08 |
| [rag-knowledge-indexes](../../../openspec/changes/refactor-rag-mcp-retrieval-service/specs/rag-knowledge-indexes/spec.md) / 四类时间分别记录 | R01、R02、R06 |
| [rag-knowledge-indexes](../../../openspec/changes/refactor-rag-mcp-retrieval-service/specs/rag-knowledge-indexes/spec.md) / 非破坏迁移 | R09 |
| [rag-mcp-access](../../../openspec/changes/refactor-rag-mcp-retrieval-service/specs/rag-mcp-access/spec.md) / 平台同步检索工具 | R07、A05、A15 |
| [rag-mcp-access](../../../openspec/changes/refactor-rag-mcp-retrieval-service/specs/rag-mcp-access/spec.md) / 远端 MCP 与 JSON 响应 | R07 |
| [rag-mcp-access](../../../openspec/changes/refactor-rag-mcp-retrieval-service/specs/rag-mcp-access/spec.md) / API Key 和知识库授权 | R07 |
| [rag-mcp-access](../../../openspec/changes/refactor-rag-mcp-retrieval-service/specs/rag-mcp-access/spec.md) / 复用四个只读工具 | R05、R07 |
| [rag-mcp-access](../../../openspec/changes/refactor-rag-mcp-retrieval-service/specs/rag-mcp-access/spec.md) / 完整政策证据契约 | R05、R07 |
| [rag-mcp-access](../../../openspec/changes/refactor-rag-mcp-retrieval-service/specs/rag-mcp-access/spec.md) / 错误和降级可区分 | R05、R06 |
| [rag-mcp-access](../../../openspec/changes/refactor-rag-mcp-retrieval-service/specs/rag-mcp-access/spec.md) / 取消与并发隔离 | R04、R07 |
| [rag-retrieval-strategies](../../../openspec/changes/refactor-rag-mcp-retrieval-service/specs/rag-retrieval-strategies/spec.md) / 知识库默认混合策略 | R05 |
| [rag-retrieval-strategies](../../../openspec/changes/refactor-rag-mcp-retrieval-service/specs/rag-retrieval-strategies/spec.md) / 策略具有不可变版本 | R01、R04、R05 |
| [rag-retrieval-strategies](../../../openspec/changes/refactor-rag-mcp-retrieval-service/specs/rag-retrieval-strategies/spec.md) / Query 改写归调用方 | R05 |
| [rag-retrieval-strategies](../../../openspec/changes/refactor-rag-mcp-retrieval-service/specs/rag-retrieval-strategies/spec.md) / 融合后精细重排 | R06 |
| [rag-retrieval-strategies](../../../openspec/changes/refactor-rag-mcp-retrieval-service/specs/rag-retrieval-strategies/spec.md) / 可选时间融合 | R06 |
| [rag-retrieval-strategies](../../../openspec/changes/refactor-rag-mcp-retrieval-service/specs/rag-retrieval-strategies/spec.md) / 后台消融和原文证据评测 | R09 |

自审结果：21项有效Requirement均有映射；本change没有REMOVED要求。

- 已核对所有任务都有文件、消费/产出接口、命名测试及关键断言、正确红灯原因、最小实现、验证命令和交付门槛；计划初次交付时实施checkbox均未开始；当前状态见各步骤及OpenSpec tasks。
- 已核对跨任务类型归属与方法名；A10先定义ContextReader、A12实现，A05统一模型/工具调用计量及证据导入，避免向后依赖造成接口空洞。
- 依赖图无环：R07→A15→R10→A16；每个Review Focus都落到对应任务测试，服务缺失与SKIP不能算完成。
- 本节为实施前的计划自审记录；用户已选择单代理。当前代码/测试/环境进度以OpenSpec tasks和docs/reports/runtime-v1为准，不能将计划材料验证当成新链路验收。

计划材料验证（2026-11-06）：两change的`openspec validate --strict --no-interactive`均通过；原契约校验43份正例/47项反例通过；28个任务共140个未开始步骤、84项Requirement映射、链接/文件归属/先创建后修改及Markdown结构检查通过。这些结果不代表产品实现、数据库迁移或真实服务验收通过。

