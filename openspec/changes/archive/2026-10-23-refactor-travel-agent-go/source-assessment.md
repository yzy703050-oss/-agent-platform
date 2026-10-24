# Python 差旅源码迁移映射

本文件记录探索证据与迁移覆盖范围。正式行为以本 change 的 specs/ 为准，技术方案以 design.md 为准。原项目不修改，不复制其 .env、用户数据、SQLite 或执行断点；本次未运行 Python 测试。

源码位置：G:\简历项目\差旅出行助手-v0\trip_assistant。目录名不是当前能力版本的可靠依据，判断基于实际代码。旧 OpenSpec 中可能存在历史阶段规则，迁移时以当前代码、相关测试和用户本次确认共同核对，不批量照搬旧文档。

## 能力与去向

| 源模块 | 已观察到的职责 | Go 去向 | 验收重点 |
| --- | --- | --- | --- |
| agents/main.py、runtime/loop.py | 原生工具调用、单顶层执行循环、规划/校验角色切换 | agent/agents/travel | 阶段受限，内部角色不等于额外平台入口 |
| runtime/state.py、review.py、trip_jobs.py | 执行阶段、自动查询、校验与修正 | Travel 的 Eino 编排 | 阶段状态不混同 Run 和行程状态 |
| trips/service.py、contracts.py、conditions.py | 行程创建、选择、修改、结构化条件 | service/trip、model | 多段、返程、必要条件和业务修订 |
| trips/validation.py、constraints.py、strengths.py | 日期、预算、硬软约束 | service/trip | 不能由模型自行降级硬约束 |
| trips/patches.py、dependencies.py、activities.py | 局部修改、依赖失效、活动安排 | service/trip | 保留无关结果，活动未知状态诚实展示 |
| trips/query_execution.py、providers/base.py | 自动查询和独立查询共用能力 | service/trip、client | 去重、候选刷新、迟到结果保护 |
| providers/contracts.py、candidates.py、validation.py | 来源、候选、金额、时效校验 | model、service/trip | 查询归属和结果修订绑定，未知值不作零 |
| providers/juhe_train.py、amap_hotel.py | 车次和酒店地点适配 | client/train、client/hotel | 协议核验和真实服务冒烟 |
| providers/public_query.py | 天气及网页查询 | client/weather、client/search | Python DDGS 不直接翻译，真实接入单独核验 |
| providers/modular_rag.py、agents/policy.py | MCP 片段解析、政策理解 | client/mcp、service/trip、差旅工具 | 现有 RAG 服务契约核验，不建另一套 RAG |
| tools/registry.py、trip_write.py、memory_write.py | 工具 schema、授权边界与 Service 桥接 | agenttools/travel | 身份可信、参数可验证、业务写入不绕 Service |
| runtime/actions.py、recovery.py、storage/actions.py | 决策、操作状态、回执及恢复 | Travel 适配、repository/postgres | 已完成写入不重复，未知写入不盲目重放 |
| storage/trips.py、evidence.py、sqlite.py | 业务持久化与事务 | repository/postgres | 复用平台身份；新增迁移，不创建第二 SQLite |
| storage/sessions.py、identity.py、runtime/logging.py | 原 CLI 会话、身份与日志 | 复用平台现有实现 | 不迁入重复身份、Conversation 或 Run |
| storage/preferences.py、agents/preference.py | 明确偏好、撤回、低频历史推断 | service/travelpreference、agent/memory | 修订、来源、撤回和任务替代隔离 |
| memory/context.py、planning.py、compaction.py | 时间、记忆注入及上下文压缩 | agent/memory、Travel 上下文适配 | 完整工具配对，来源过滤先于压缩 |
| presentation/trip_reply.py、facts.py | 实际保存结果与最终事实绑定 | service/trip 结果、Travel 输出适配 | 不靠模型措辞虚报已保存、合规或可预订 |
| cli.py、bootstrap.py | CLI 界面和旧组件组装 | 复用 React/Handler/bootstrap | 不迁移 Rich CLI 作为新平台入口 |

## 行为覆盖矩阵

本矩阵用于检查能力是否迁移，不用于扩充测试数量。按用户要求，不逐项移植原 Python 的上千个测试；多个矩阵项可以由同一个综合场景、表驱动用例或现有平台测试覆盖。具体取舍遵循 design.md D11，避免在 Agent、Service、Repository 三层重复断言同一业务规则。

| 编号 | 场景 | 对应正式规格 | 预期验证层 |
| --- | --- | --- | --- |
| B01 | 多段规划、显式返程、日期和住宿关系 | travel-planning | 规则单测 + 工作流 |
| B02 | 只修改活动，保留交通/住宿 | travel-planning | 规则单测 + 查询调用次数断言 |
| B03 | 条件改变，仅使关联候选失效 | travel-planning、travel-evidence | Service + Repository |
| B04 | 硬约束、金额精度和未知价格 | travel-planning、travel-evidence | 类型及规则单测 |
| B05 | 独立查询与自动查询复用 | travel-evidence | 契约 + 有界并发/缓存测试 |
| B06 | 候选跨用户、跨查询、过期或伪造 | travel-evidence | Service + PostgreSQL |
| B07 | 政策命中、无命中、不适用及超时 | travel-policy | 契约 + 真实 MCP 冒烟 |
| B08 | 校验后选择、局部修正、受限重规划 | travel-execution | 脚本模型工作流 + 真实模型 |
| B09 | 保存结果与展示事实一致 | travel-planning | 事务故障注入 + 前端 |
| B10 | 写入提交后但断点前退出 | travel-execution | 恢复探针 + PostgreSQL |
| B11 | 关闭页面暂停，手动恢复，页面内切换继续 | travel-execution + 现有 task-recovery | 集成 + 浏览器 |
| B12 | 新问题替代，旧任务与长期记忆均排除 | travel-execution、travel-memory | 实际模型输入断言 + PostgreSQL |
| B13 | 明确偏好、撤回与推断冲突 | travel-memory | Service + Repository + 上下文 |
| B14 | 超预算压缩与工具调用/结果完整配对 | travel-memory | 上下文单测 + 工作流 |
| B15 | 原平台普通聊天、历史、隔离、恢复回归 | 现有主规格 | 全套 Go/前端 + 真实 PostgreSQL |

## 明确的边界

- 当前 Python MainAgent 是差旅内部规划角色，不是平台 general。两个平台入口的要求不禁止差旅内部使用多个执行阶段。
- 酒店地点数据不是报价、库存或订单；活动建议不代表已核实营业、预约或票务。
- 旧项目的真实模型评测记录不等于此次 Go 迁移已通过，也不证明所有供应方已真实接通。
- 原 Python 执行断点不转换；按用户最新答复，不导入旧业务与记忆数据。
- 原有测试用例仅用于理解关键行为和边界，不按测试数量或文件数量迁移；不直接复制 Python 运行框架或已过时规格，实施时按矩阵记录可合并、可复用的 Go 证据。
