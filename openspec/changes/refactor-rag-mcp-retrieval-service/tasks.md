## 1. Technical review and implementation plan

- [x] 1.3 按用户确认同步原Agent/RAG两change：Travel通过HTTP MCP同步取得证据，不建立RAG Job/请求结果Topic/自有异步结果Outbox；保留API Key、完整证据、期限、并发和结果复用。仅文档协调，校验见Agent engineering-review.md追加记录，未修改产品或外部仓库。

- [x] 1.4 完成外部RAG源码/固定SDK接缝复核，66项现有单元基线和Go→Python HTTP探针通过，复现检索异常被误报no_knowledge；处置已写design，统一记录在原Agent engineering-review.md。只证明所列范围，不替代1.1真实依赖与中文检索验收。

- [ ] 1.1 文档/源码及MCP JSON/无状态生命周期复核已完成（见1.4）；精确版本/revision候选已记录，仍需依赖解析/锁、真实Milvus/中文BM25、Rerank模型加载及管理库/时间接缝验证。此项不因离线基线通过而勾为完成，重要发现先协调specs/design/tasks。
- [x] 1.2 两计划已交付并自审；用户于2026-11-06选择单代理执行，按同一方式进入apply。真实依赖准备1.1保持未完成。

- [x] 1.5 已交付并自审[详细实施计划](../../../docs/superpowers/plans/2026-11-06-rag-mcp-retrieval-service.md)：11项任务逐项映射21项有效Requirement，包含文件/接口、先失败测试、最小实现、验证命令和完成条件；用户已选择单代理并进入apply；当前R00配置代码阶段已推进，完整依赖锁及真实模型验证未完成。

## 2. Knowledge ownership and lifecycle

- [ ] 2.1 实现 RAG 自有管理库版本化迁移、知识库与不可变策略、文档/版本/分块身份、原文件归档和任务幂等；覆盖相同文件不同策略、不同临时路径、重试及 DIT 保留测试，验证 `rag-knowledge-indexes` 的独立管理和稳定身份场景。
- [ ] 2.2 复用现有 Loader 和 SplitterFactory，完成 TXT/PDF、递归与段落分块、tokenizer 长度边界、正文/检索文本分离、原文位置和条款时间记录；用固定原文与不同切分结果验证内容保真和证据范围可回溯。

## 3. Milvus build and publication

- [ ] 3.1 增加 Milvus 存储适配、Dense/BM25 schema、中文 analyzer、相同语料清单和不可变构建集合；通过真实 Milvus 集成测试证明向量与关键词查询、维度/模型隔离、幂等写入和构建统计一致。
- [ ] 3.2 实现 queued/running/validating/ready/published/failed 构建状态、单 worker 恢复、活动指针 CAS、请求 pin、逻辑删除和清理重试；通过故障注入验证半构建不可见、发布中查询不串版本、删除后新查询排除文档且回退不复活。

## 4. Retrieval and evidence

- [ ] 4.1 改造检索上下文与缓存，固定每请求身份/索引/profile/可见性；知识库默认和所有首版类型绑定统一 Dense+BM25+RRF；并发与路由测试验证未知类型回默认、Host 无算法覆盖入口、Query 文本不能扩大范围。
- [ ] 4.2 复用 RerankerFactory 接入已锁定模型及分数转换，保留候选至时间融合后再输出 Top-K；先写“正确片段不在初排前五仍可进入最终结果”的失败测试，并通过真实模型中文样例核验输入长度及实际排序。
- [ ] 4.3 实现 `timelyrag-adapted-v1` 的四时间来源、区间距离、归一化、权重、未知/同分跳过、时间匹配标识与 Trace；以可手算样例、晚入库历史政策、缺失 QET/DET、跨时区和区间边界测试验证；不得宣称完整复现论文收益。
- [ ] 4.4 统一完整证据、展示结果和安全错误映射，保持政策 v1 兼容；测试长条款、真实来源、响应大小、双路失败、单路/重排/时间故障降级，证明异常不再被转换为无命中。

## 5. Remote MCP access

- [ ] 5.1 复用四工具注册，加入 stateless JSON Streamable HTTP、API Key 摘要与授权、Host/Origin、超时/并发限额、stdio 显式兼容入口；用官方 SDK 验证初始化、工具发现、旧/新参数、JSON 响应及 401/403/协议错误，并确认凭据不出现在结果或日志。
- [ ] 5.2 使用独立客户端进程并发连接服务，覆盖不同 key/知识库/策略隔离、客户端重连、请求取消及迟到结果；验证 SDK/协议锁定版本和多进程报告，本任务不修改小豆包单实例启动限制或 Agent Query 改写行为。
- [ ] 5.3 与原Agent change接通同步MCP工具，验证连接池复用、Travel保持running/续租/占专业槽、无RAG Job/Topic；平台tool_calls/evidence保存结果并优先复用，未保存只读查询允许预算内必要重试。
- [ ] 5.4 验证HTTP断连、RAG重启、平台结果提交前后宕机、期限/失权、超限和完整条款；确认外层MCP适配层唯一重试、总尝试/期限不重置，内部阶段不叠乘重试，不以静态validate代替跨进程验收。
- [ ] 5.5 验证HTTPS/API Key撤销/轮换、工具/知识库授权及跨用户来源边界；不传用户Cookie或密钥到工具参数，不新增Kafka/HMAC身份体系或平台PG写凭据。

## 6. Existing administration and evaluation

- [ ] 6.1 将现有 CLI、Dashboard、列表、摘要、数据浏览、删除和状态展示接入新管理库及 Milvus，修正格式选项、临时身份与 PipelineResult 成功判断；通过管理服务测试及页面人工检查证明一次失败不会显示成功，普通 MCP key 不获得管理能力。
- [ ] 6.2 复用 EvalRunner/Trace，增加原文证据范围 gold、配置/语料/Query/时间快照和实际执行路径，完成混合候选下 Rerank×时间四组及分块/Dense/BM25 对照；保存失败、候选召回、最终质量、错误时间版本和延迟原始报告，重跑可核对输入与结果归属。

## 7. Verification and delivery

- [ ] 7.1 从保留原文件执行非破坏迁移演练，核对来源、版本、片段、旧 v1 客户端兼容、活动切换和回退，记录真实 Milvus/Embedding/Rerank 依赖证据及限制；缺原文件、模型或依赖时如实报告，旧数据不删除。
- [ ] 7.2 对照本 tasks 和详细计划审核代码/测试证据，实际运行计划规定的单元、集成及端到端检查并完成所选执行方式的代码评审；运行 OpenSpec verify（若未启用则明确记录并人工逐项核对），重要问题修复后重新验证，再归档与分支收尾。

当前实施：R00的RuntimeSettings、显式配置示例及真实缺项/验证入口已写入独立RAG工作区，本次相关21项检查通过。全量单元发现31项失败；同解释器的干净HEAD基线有32项失败，无新增失败，另一个差异是既有时序测试。完整锁、Milvus/模型加载及原WIP整合仍待完成，1.1不勾选。平台依赖采用用户已选择的Docker，Windows后端等待重启。

R00代码检查点已提交至独立RAG分支codex/rag-runtime-v1，commit `7209c74`；配置与预检并不等于R00完整锁/真实模型完成，证据在外部仓库docs/reports/runtime-v1/R00-progress.md。
