## Context

动机见 `proposal.md`。当前工作区干净，分支为 `codex/travel-normal-flow`，项目已初始化 OpenSpec。正式行为以本 change 的 `specs/travel-source-fidelity/spec.md` 及主规格 `travel-planning`、`travel-execution` 为准。

已核对 Python `G:/简历项目/差旅出行助手-v0/trip_assistant/agents/prompts.py` 及冻结 SHA；Go 的 main/review/summary/reply/rail 五个原文均匹配冻结基线。问题在运行层：`flow.go` 统一叠加 correction、reply、interaction；`protocol.go` 在重新生成时只替换首条角色消息并继续追加 planning_summary；`batch_gate.go` 的无flow入口独立拼接另一套前缀。`planningSummaryProfile` 自定义短描述还未复用冻结的参数帮助，不能简单删除角色中的参数说明。

## Goals / Non-Goals

**Goals:** 当前有效规则有明确维护位置；仅注入适用层；正常、恢复及重试的真实角色与提示一致；保留已确认业务行为和可核验证据。

**Non-Goals:** 不改 Python、不删除冻结文本、不重写政策/偏好/压缩辅助模型，不改工具名、参数schema、profile筛选、校验器、事务、预算，不新增执行状态机或放宽授权。不部署、重启或自动整合其他活动变更。

## Decisions

### D1. 冻结证据与运行资源分开

保留 `prompts/main.txt`、`review.txt`、`summary.txt`、`reply.txt`、`rail.txt`、辅助提示、冻结schema/fixture及原SHA。新增 `prompts/runtime/` 存储整理后的有效提示；旧 correction/no_followup/planning_summary 作为历史可追溯输入保留，但不继续全文加载。`TestTravelSourcePrompts` 仍验证原文；运行时测试改验有效规则和角色隔离。

不采用修改冻结原文并更新SHA，也不采用在原文尾部继续追加“本段覆盖”补丁。后两者分别破坏审计或保留矛盾。

### D2. 按职责分层并维护规则映射

| 维护位置 | 规则归属 | 适用角色 |
| --- | --- | --- |
| 原 `replyPolicy` | 原请求优先、数据不可信任为指令、证据/已保存事实、简洁中文、不追问 | 全部 |
| `prompts/runtime/planning.txt` | 默认优先级与软标记、完整路线/活动/逐晚住宿、政策前置、直接使用注入记忆 | main、planning_summary、verifier |
| `prompts/runtime/review.txt` | 完整需求核对、实际候选联合衔接、最小完整修复、缺口与无进展收尾 | planning_summary、verifier |
| `prompts/runtime/main.txt` | 首次整趟规划、唯一政策续规划、独立动作同批、明确偏好更新和历史定位边界 | main |
| `prompts/runtime/planning_summary.txt` | 当前计划直接修复与选择、失败创建恢复、不得交回Main/跨计划/写记忆 | planning_summary |
| `prompts/runtime/verifier.txt` | 旧Verifier范围及其原有重规划交接 | verifier |
| 原 `summaryInstruction` | 只总结独立查询，不创建或修改行程 | summary |
| 工具层运行时描述 | 字段路径、元数据叶键、ID归属、route_tasks完整路线、reply语法及参数纠错 | 仅实际可见的对应工具 |
| 动态上下文与观察 | 日历/时区、默认值数据、有效偏好/记忆、原请求、计划/候选/错误 | 保持现有注入与隔离 |
| 独立恢复层 | 协议未执行事实、交互违规、停止答复要求 | 对应状态触发 |

实施时在 `docs/travel-prompt-layers.md` 按原段落记录保留/合并/迁移/已授权替代及目标层。不能只保留表中概括而删除住宿、活动、候选身份、硬一晚中转等具体边界。语法实例归工具层，业务判断归规划/审查层，不复制同一段指令。动态默认值数据可由多个角色看到，但不能给summary追加规划动作指令。

### D3. 一个前缀组装入口，切换时整体替换

在 `prompt.go` 定义 `promptState{Role string; Stopped, ProtocolRecovery, InteractionRecovery bool}`、`composePromptPrefix(state promptState) []*schema.Message` 和 `replacePromptPrefix(messages []*schema.Message, state promptState) []*schema.Message`。前缀按“角色→公共回复→适用规划/审查→必要恢复”固定顺序生成；分层文本带稳定标记，角色始终在第一条。

`replacePromptPrefix` 仅移除上述入口管理的角色/规则/恢复消息，保留标为数据的系统上下文、会话摘要、用户/assistant/tool消息及其顺序；不能用“删掉所有system”处理。重复调用幂等，切换main→planning_summary整体换掉旧层；停止状态仍以程序实际工具绑定为准。

`flow.beforeModel`、`protocol.prepareGeneration`、协议/交互恢复和 `batch_gate` 无flow入口都调用此入口。重试不能仅换第一条、遗留Main权限指令，也不能追加第二份规则。恢复提示只陈述本次失败及遵守当前职责，不再复制整套默认规划。`Stopped=true` 只加载新增 `prompts/runtime/stopped.txt` 与公共回复边界，依据已有事实作答，不继续加载规划动作层，也不把已停止的行程总结冒充独立查询总结。错误观察原样保留；拒绝旧工作流断点继续复用现有指纹机制。

### D4. 工具描述在运行时副本整理

保留 `SourceProfile` 及冻结schema输出不变。工具包新增 `runtime_instructions.go` 与 `schemas/runtime_write_trip_help.txt`、`schemas/runtime_query_policy_help.txt`；`Registry.Profile` 完成现有权限筛选后调用 `withRuntimeToolInstructions(profile string, tools []*schema.ToolInfo) []*schema.ToolInfo`，仅复制并整理可见工具的描述。

write_trip运行描述将原 `TripArgumentHelp` 与补充层有效字段规则完整合并为单一运行文本，按实际profile说明允许操作；原文继续冻结但不整段注入，规划总结描述不能漏掉参数帮助。真实模型暴露的混合amend组件语法归入同一工具说明；工具组装版本为tool-instructions-v4；共享字段、创建语法、修改语法分开维护，planning_recovery仅加载创建帮助，planning_summary/旧Verifier仅加载修改帮助，Main在已有NewPlanningRequest/PlanContextComplete状态下只注入新规划或明确修改所需说明，普通情况按原profile保留两类说明；仅描述分层，不改变工具名称/参数/过滤权限。query_policy描述提供政策前置，错误码解释放对应工具的帮助，字段级失败仍由原观察提供。不改工具名字、参数或校验，也不改变 `profileForState` 现有映射和权限。

### D5. 指纹覆盖有效资源与组装版本

新增 `runtimePromptMaterial() string` 和工具包导出的 `RuntimeToolInstructionMaterial() string`，稳定顺序收集所有运行时文本、公共回复及恢复文本，含 `prompt-layers-v1` 组装版本。`agent.NewAgent` 在保留其他现有指纹因子的基础上纳入两者；审计继续hash真实messages及实际工具。辅助提示仍入指纹。测试覆盖任一资源或版本变化时材料及指纹改变。

冻结证据哈希独立验证，不能用运行时新SHA替换迁移SHA。旧断点拒绝属于既有兼容规则，不伪称可沿用旧提示恢复。

### D8. 创建恢复独立于已有计划审查

真实模型发现，Main失败且无已保存计划时，沿用“审查后选择并绑定reply”的层会诱导propose附非法reply。新增runtime/planning_recovery.txt及promptState.PlanningRecovery，仍使用planning_summary可信角色和planning_recovery既有工具profile；只注入公共回复、规划规则和创建恢复职责，不加载已有计划review动作层。正常构造和重新生成均从当前Plans是否为空推导，不新增持久状态或权限。规则组装版本升级prompt-layers-v2，新增资源入指纹，真实恢复测试禁止无计划时加载review层。

### D6. 验证区分静态结构与真实模型行为

先用表驱动单测验证四角色、工具帮助、重试幂等、数据保留和冻结基线；用真实Eino输入测试验证Main→总结及恢复、工具hash与实际输入一致。保留既有正常调用次数、硬条件/范围/政策/记忆/部分结果回归，允许调整只断言旧提示文本身份的测试，不放松业务断言。

提示改写不能仅凭脚本模型证明语义不变。实施后运行现有真实模型＋PostgreSQL交互验收的九类场景，每类一次，完整记录失败和实际调用；以及来源验收中的活动局部修改，检查候选保留。固定工具夹具不证明真实供应方在线能力。缺必要依赖时报告未验证并保留任务未完成，不当作通过。

## Risks / Trade-offs

实施补充D7（真实验收发现）：明确ID的旧行程修改需要在Main首轮前加载受权快照。只读read后直接交总结会落入创建schema；即使事后收养计划，总结对旧硬活动的保护仍正确拒绝改时。因此采用最小输入补齐：sourceInput仅从当前请求的明确非否定/非假设修改语句提取“行程 UUID”，优先于隐式历史ID；Resume继续只用断点。沿用原beforeModel的Inspect核验用户、来源、attempt并加载快照，Main首轮依据本轮明确授权直接amend。lookup不自动加载，原请求以外和否定分句ID不纳入。仅本轮明确ID编辑或已进入规划时，已有完整快照才向既有profile过滤提供PlanContextComplete=true；普通历史查询保持原过滤行为，不等待首次写入才标完整，避免再次read；输入识别与版本进入指纹。不增加Main例外，不修改总结硬要求保护、DecideRole、Registry过滤规则或工具schema；无ID定位仍沿用现有流程。

- 合并导致规则遗漏 → 原段落映射、业务场景回归与真实模型验收共同核对，不以字数减少替代正确性。
- 源文身份测试与运行测试耦合 → 冻结原文测试不改SHA；仅迁移运行时身份断言，保留所有业务断言。
- 切换丢失数据或重复前缀 → 入口标记精确管理、两次替换幂等、完整工具配对与data消息逐字保留测试。
- 工具说明被丢弃或越权 → 基于已有profile副本，仅改描述；比较名称/参数/筛选与重试实际工具集。
- 旧断点无法恢复 → 指纹明确变化；历史保留；实施不主动中断或部署运行服务。
- 真实模型非确定性 → 全部样本留证，不承诺字面输出相同或固定两次适用于所有纠错路径。

## Migration Plan

文档审阅及用户执行方式选择 → 按实施计划TDD整理资源/工具说明 → 接入统一前缀和指纹 → 全量后端验证及真实模型验收 → 自审或所选独立评审 → strict validate及人工规格核对 → archive同步 → 分支收尾由用户选择。不因纯文档准备归档或宣称实现已完成。

不修改本地Python项目或原冻结资源。发布时新执行使用新提示；旧不兼容断点按既有机制拒绝。回退需要整组还原运行提示、组装及指纹，不能单独回退一个补充文件。
