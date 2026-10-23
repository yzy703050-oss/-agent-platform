# 差旅 Go 重构实施记录

计划：docs/superpowers/plans/2026-10-23-travel-agent-go.md。正式规格：openspec/changes/refactor-travel-agent-go。2026-10-23 用户批准计划并选择单代理，按精简测试策略实施；本记录不替代 OpenSpec 任务状态。

## 基线

- 现有 Go 全套回归在 REQUIRE_DATABASE_TESTS=1 下通过，包含真实 PostgreSQL 集成；日志 .cache/reports/travel-baseline.log。
- 当前项目内工作，未建立第二个 Go module、工作树或 Python 服务，未读取原 Python .env 或业务数据库。

## Task 1：恢复技术探针

- 将 General 已有 TurnLoop 与 checkpoint envelope 提取到 agent/einoruntime，General 保留兼容入口，停止确认规则不变。
- 旧运行适配测试随实现迁移到新包，原断言保留。General 自己的执行与流式测试继续运行，不重复一套相同测试。
- Travel 使用 Eino ChatModelAgent Handlers，阶段与累计调用计数序列化到 Eino SessionValues，构造内核不调用模型。
- 新增一个聚合恢复测试，包含正常持久化恢复与保存失败子场景，同时检查版本指纹不兼容、已完成写工具不重复和待执行查询继续。
- RED：新恢复包/内核未实现时测试失败；GREEN：`go test ./internal/agent/agents/general ./internal/agent/einoruntime ./internal/agent/agents/travel -count=1` 通过，日志 .cache/reports/travel-task1.log。
- Ruling：Eino 停止后可以保存完整模型响应及尚未执行的工具调用；恢复先执行该工具，再调用模型。测试分别断言待执行工具看到 querying/计数 2、后续模型看到 validating/计数 2。代价：此断言绑定已固定 SDK 的实际恢复语义，升级必须重新验证。
- Ruling：共享运行适配的测试引用私有停止确认字段，应随被测实现迁移，不能用模型 context.Err 替代停止确认；SDK 的安全点取消允许当前模型调用完成。原停止确认断言完整保留。

尚未证明真实业务修订的幂等、完整 Travel Execution 或真实供应方/RAG 接入，这些继续在后续任务实施，不能将此探针描述为差旅功能完成。

## Task 2：行程业务规则

- 新增不依赖 Eino 的行程、分段、活动、约束、查询证据和候选引用类型；金额使用整数分，空价格保持未知。
- Trip Service 分离纯预览规则和消费者侧 Store；实际行程写入经 Service 到事务接口，后续由 PostgreSQL 实现。
- 支持多段/返程、缺失条件、日期/住宿关系、硬软约束、显式局部补丁、关联选择失效、候选归属/时效/条件校验。
- 一个聚合规则测试含 11 个关键子场景；没有复制原 Python 的测试套件。全局和分段预算冲突先出现失败，再修复为不会被 partial 状态覆盖。
- 验证：`go test ./internal/service/trip ./internal/agent/agents/general ./internal/agent/einoruntime ./internal/agent/agents/travel -count=1` 及 `go vet ./internal/service/trip ./internal/model` 均通过；日志 .cache/reports/travel-task2.log。
- 这是业务规则层完成，尚未开放差旅入口；数据库事务、工具编排、政策检索和真实服务仍继续实施。

## Task 3：PostgreSQL 事务、决策和回执

- 新增 004 增量迁移和行程、证据、决策、操作、回执及记忆表，001–003 保持不变；开发库未应用新迁移。
- 行程与偏好执行共用可信身份/会话、Run、attempt 和 running 状态围栏；事务按会话→Run→资源加锁，行程修订和回执原子提交。
- 模型决策保存原响应及稳定逻辑操作标识；标识不含变化的 attempt。相同请求回读原结果，不同请求摘要冲突。
- 一个数据库测试组的三个子场景验证：003 升级及迁移失败回滚、并发行程修订/重放/隔离/围栏、决策跨 attempt 保持及回执故障整笔回滚。
- 验证：真实 PostgreSQL 下 `go test ./tests/integration -run 'TestTravelTransactions|TestMigrationIdempotent|TestConcurrentIdempotentAdmission|TestCompleteTransactionRollback' -count=1 -timeout 120s` 及 Repository/Trip 的 vet 均通过；日志 .cache/reports/travel-task3.log。
- 不将数据库层幂等通过等同于完整 Eino 故障恢复；OpenSpec 2.2 的端到端提交后断点前场景继续由 Task 6 验证。

## Task 4：查询与客户端（进行中）

- 已实现规范化查询、同执行范围并发合并、共享有界并发、5 分钟证据缓存、独立查询到行程查询的重新绑定，以及数据库修订/时效检查。
- 新增聚合车次、高德酒店地点和 wttr.in 天气客户端；响应体限制、超时/取消和错误脱敏由小型 providerhttp 包共用，错误不回传带 Key 的请求 URL。
- 车次金额按有理数转整数分，不经过 float64；只有来源时长能证明时才推算跨日到达时间。酒店参考消费值不映射为房价、库存或可预订状态。
- 一个协议测试组覆盖三个供应方的关键映射及非法返回，使用本机 httptest，不调用真实付费 API。查询组复用真实 PostgreSQL fixture，验证缓存、证据选择和迟到结果拒绝。
- RED→GREEN：缺少查询/客户端实现时失败；非法天气响应被误判为无日期数据的 partial 已由协议断言捕获并修复为协议错误。
- 验证：`TestProviderContract|TestTravelQueries|TestTravelTransactions` 及相关 Service/Client/Repository vet 均通过，日志 .cache/reports/travel-task4-providers.log。
- 默认沙箱无法访问本机 httptest 回环连接，使用已授权用户环境运行后通过；这不是改用测试替身掩盖真实 API 问题。
- 尚未完成真实供应方冒烟、网页检索供应方核验及完整查询韧性，因此本任务未标为全部完成。接下来可继续偏好、工具和执行贯通，不因等待外部配置停止所有工作。

协议参考（2026-10-23 核验）：[聚合数据](https://www.juhe.cn/docs/api/id/817)、[高德 POI 2.0](https://developer.amap.com/api/webservice/guide/api-advanced/newpoisearch)、[wttr.in JSON 输出](https://github.com/chubin/wttr.in#json-output)。聚合页面直接读取返回 405，官方搜索索引可定位接口；解析细节仍参考原实现，真实服务验收未完成。

### Task 5 — 来源隔离与预算（已验证的部分）

真实 PostgreSQL 的 TestTravelMemoryIsolation 通过：偏好修订幂等、被替代来源排除、更早有效值回退、混合摘要全部来源检查、明确撤回和推断冲突保护。TestTravelContextBudget 先失败后通过，超长可选摘要让位于当前 Run，历史按整个 Run 保留，不拆分工具配对。相关 memory/preference/repository vet 通过。

低频自动偏好分析及自动摘要生成尚未接入，不将 Task 5 或 OpenSpec 7.3 整体勾选完成。当前摘要可安全保存与读取；预算不足明确返回限制。代码不访问 Python 数据库。
