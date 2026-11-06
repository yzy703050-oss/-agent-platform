## Purpose

使独立RAG通过标准远端HTTP MCP提供受控、可引用检索，平台将其作为同步工具而非Agent委派，复用四工具契约。RAG自管知识与构建数据，不写平台Run/Job/计划表，不引入平台检索Kafka请求/结果链路。

## ADDED Requirements

### Requirement: 平台同步检索工具
服务 SHALL 通过远端MCP响应平台只读检索，直接返回完整真实证据或明确错误；RAG不是平台专业Agent，不引入独立RAG Job、Kafka请求/结果或结果Outbox。平台固定原工具调用身份并保存已收到的证据，服务不要求通过后台任务受理来取得普通检索结果。

#### Scenario: Travel需要检索证据
- **WHEN** Travel通过MCP调用RAG
- **THEN** 服务在授权、并发和剩余期限内直接返回结果，Travel在当前Job内同步等待后继续，不使用RAG委派或回调消息

#### Scenario: 断连且结果尚未可靠保存
- **WHEN** HTTP连接或RAG进程在结果可靠保存到平台前中断
- **THEN** 调用方可在同一逻辑调用及累计预算内有界重试只读检索，服务可能重新计算，不承诺零重算或恰好执行一次

#### Scenario: 迟到计算
- **WHEN** 调用已超时或调用方执行权失效，底层计算才结束
- **THEN** 不将其报告为当前有效成功，原生计算资源在实际退出后释放，不因连接断开就声称计算已停止

### Requirement: 远端 MCP 与 JSON 响应
服务 SHALL 提供 Streamable HTTP `/mcp`，正常工具调用返回 JSON；业务状态 MUST 独立于连接持久化。远端部署 SHALL 使用 HTTPS，可由反向代理终止 TLS。首版不要求 SSE 推送或 OAuth，保留 stdio 作为显式运维兼容入口。

#### Scenario: 多进程调用
- **WHEN** 多个客户端进程同时连接相同远端地址
- **THEN** 它们分别建立客户端并获得各自结果，不共享进程内连接对象，不因连接重建丢失已提交的文档或构建任务

#### Scenario: 仅 JSON 的工具执行
- **WHEN** 客户端完成协议初始化后发现并调用工具
- **THEN** 服务按所支持的 MCP 版本完成交互，以 JSON 返回工具结果，不要求客户端持有进度流

### Requirement: API Key 和知识库授权
服务 MUST 在 HTTP 入口校验 API Key，并根据调用身份校验工具与知识库权限；查询、集合列表和文档摘要 MUST 使用相同的授权边界。密钥不得进入模型参数、工具结果、策略快照或普通日志。

#### Scenario: 未授权访问
- **WHEN** 请求未携带有效凭据或指定未授权知识库
- **THEN** 分别返回认证失败或权限不足，不执行检索、不返回片段或集合统计

#### Scenario: 列表不泄露其他知识库
- **WHEN** 身份仅获授权知识库 A 并调用集合列表工具
- **THEN** 返回仅包含 A 的逻辑集合信息，不暴露其他知识库或 Milvus 物理集合

### Requirement: 复用四个只读工具
服务 SHALL 保留 `query_knowledge_hub`、`query_policy_evidence`、`list_collections`、`get_document_summary` 的名称及已有有效入参。`collection` SHALL 表示逻辑知识库；省略时使用服务端配置且已授权的默认知识库。首版对 Host 不新增导入、删除、构建或任意策略覆盖工具。

#### Scenario: 旧查询参数仍可使用
- **WHEN** 客户端使用已有 `query`、`collection`、`top_k` 调用政策证据工具
- **THEN** 服务执行该知识库默认策略并返回兼容的 v1 证据，不要求调用方传入算法、模型或物理存储参数

#### Scenario: 摘要范围受控
- **WHEN** 客户端指定 `doc_id` 查询摘要
- **THEN** 服务在所选已授权逻辑知识库中定位文档；不存在则明确报告不存在，不跨库猜测或返回其他文档摘要

### Requirement: 完整政策证据契约
`query_policy_evidence` MUST 保留 `schema_version=1`、`status`、`collection`、`fragments`，状态为 `ok`、`no_knowledge` 或 `error`；每个片段 MUST 有一致且唯一的 `id/chunk_id`、完整 `content` 与真实 `metadata.source`。新增追踪信息 SHALL 使用可选字段，展示工具的摘要不得替代机器证据正文。

#### Scenario: 长条款保真
- **WHEN** 命中包含审批例外或时间条件的长片段
- **THEN** 政策工具返回完整片段及来源位置，不按展示摘要长度截断；超过响应上限时明确失败，不返回损坏的部分条款

### Requirement: 错误和降级可区分
服务 MUST 区分参数/协议错误、认证/授权错误、检索失败、无命中和阶段降级。单路召回或重排失败时如允许回退，MUST 返回真实执行路径和降级原因；两路均失败不得转换为无命中。

#### Scenario: 底层检索异常
- **WHEN** Dense 与 BM25 均发生服务异常
- **THEN** 工具返回错误状态和安全错误码，不返回 `no_knowledge`

#### Scenario: 单路降级
- **WHEN** 混合查询仅 Dense 成功且知识库允许降级
- **THEN** 可返回 Dense 证据，但标明 BM25 失败、实际策略降级；评测记录不得记作完整混合成功

### Requirement: 取消与并发隔离
服务 MUST 对调用设置期限与资源限额，并隔离并发请求的身份、知识库、索引和策略。到期后的迟到计算结果不得作为本次成功响应；取消已执行查询不得破坏其他调用。工具调用结果 SHALL 带可关联的请求标识。

#### Scenario: 不同知识库并发
- **WHEN** 客户端并发检索知识库 A 与 B
- **THEN** 每次调用只使用自身绑定的索引及策略，返回内容与追踪记录不串库

