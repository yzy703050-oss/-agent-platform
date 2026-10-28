# 差旅提示词分层实施记录

计划：`docs/superpowers/plans/2026-10-28-travel-prompt-layers.md`。OpenSpec：`refactor-travel-prompt-layers`。

2026-10-28，用户已审阅后要求“开始”，并选择单代理。当前代理实施、TDD和整体自审，不派实施或评审子代理。

## Setup

- 按已确认设计沿用`codex/travel-normal-flow`；规划文档为本任务创建，其他活动change不改。未创建或切换额外worktree。
- Pre-flight：任务1提供promptState/前缀与工具说明接口，任务2接入并提供指纹材料，任务3验证同一真实调用链；接口一致。
- Windows没有bash，执行技能的shell记账脚本以本记录和PowerShell命令等价维护；产品测试仍使用计划原始Go命令。
- 读取全部change文档、计划及apply指导；5项OpenSpec实施任务均未完成。

## Task 1

进行中：先添加角色分层、精确替换/数据保留与工具描述测试，冻结原文及其SHA不变。

- 基线：两个差旅package完整测试PASS。
- RED：新分层/替换/工具帮助测试编译失败，明确缺少composePromptPrefix、promptState、replacePromptPrefix、withRuntimeToolInstructions。
- GREEN：新测试及冻结SHA测试PASS；随后两个package完整测试PASS（travel 6.895s，agenttools/travel 3.338s）。
- 交付：运行层资源、精确替换、工具描述副本、逐段规则映射已完成；产品正常入口接入留给任务2。

## Task 2

进行中：接入真实模型输入、恢复和新指纹，保持实际工具profile与业务权限。

- RED：真实Eino输入回归的normal、lookup、without_flow、stopped四类均发现仍使用旧前缀；指纹回归明确缺少运行资源材料接口。
- GREEN：接入统一入口后，旧身份断言按计划改为运行角色断言，冻结SHA测试未改。travel、agenttools/travel、service/trip完整回归全部PASS（7.112s/3.062s/0.263s）。
- 指纹包含所有有效运行文本、恢复文本、工具帮助与组装版本；新增文本LF字节属性避免跨平台换行漂移。
- 正常、重试、停止及无flow真实Eino前缀回归PASS；已有恢复审计继续核对真实输入和工具hash，工具名称、schema、profile筛选、预算与业务状态机未变。
- vet和server构建PASS。完整后端及真实PostgreSQL验证进行中。

## Task 3

进行中：完整后端、真实模型验收及单代理整体自审。凭据配置存在，仅检查存在性，不输出值。

- 完整后端首轮发现1个旧提示身份依赖：TestTravelSourceRecoveryBatch仍按planning-summary-v1补充原文计数，实际已保持Main=1、恢复Main=0，错误地得到总结=0。按systematic-debugging追溯到测试模型计数标记；只改为有效角色标记，副作用/回执/查询/调用次数全部原断言保持。

- 全后端最终结构回归：422项PASS，0失败，8项外部依赖opt-in测试Skip；真实PostgreSQL为REQUIRE_DATABASE_TESTS=1，集成package PASS（43.330s）。opt-in交互/局部修改另显式运行，不把Skip算通过。
- 真实模型round1：7/9；缺省返程自由活动冲突、中转错返程及活动ID。按已有规格在规划/审查层补明固定日期优先和返回日规则，在工具层保留具体ID例子。
- round2：8/9；普通往返活动误归返程段，纠错又漏行程身份。恢复原始业务例子到规划/审查层，工具层明确身份外壳。
- round3：8/9；多城选择漏tripplan身份与版本。工具说明增加完整JSON外壳示意，并将针对性参数帮助置于冻结参数参考之前；不改schema、错误反馈或预算。
- round4：8/9；中转增加不必要南京住宿。审查层明确按固定返程和目的地硬晚数反推同日中转、已有可行组合不加中转停留。
- 每轮完整九类验收、每类一次，保存全部输入/结果/Usage；没有挑选不同轮的成功样本拼成通过，没有放宽断言或重置业务限制。round5进行中。

## 单代理代码自审

按requesting-code-review检查清单整体复核：运行角色仅注入适用规则，旧原文留证；replacePromptPrefix只移除受管system层，保留上下文/摘要和完整工具消息；重试整体换角色，journal仍匹配真实输入/工具；停止不重新绑定规划权限；tool副本仅改Desc，SourceProfile、schema、权限筛选和service保护均未改。原默认值、政策前置、硬要求、活动归属、逐晚住宿、ID与版本等规则逐段映射可追溯。

当前未发现代码级重大问题；真实模型验收仍是独立完成条件。自审由作者完成，未派独立评审代理，符合用户本次选择。

- round5：8/9；普通往返修活动时components仅content却附train选择，被现有component scope拒绝。没有改变校验器。
- Technical ruling：D4从“原TripArgumentHelp全文＋新补充”改为完整单一运行工具说明（tool-instructions-v2），冻结原文及SHA仍保留。原因：原参考中“返程通常为空/活动仅content”等概括与针对性规则分散，叠加导致模型漏身份和混合组件错误；完整迁移原字段语义并明确混合amend范围。成本：迁移遗漏风险由逐段映射、工具字段/原schema测试及整组真实模型验收核对。此为技术方案调整，不改变产品行为、schema、权限、预算。
- RED：工具帮助回归实际失败，缺混合组件说明并仍注入整段冻结参考。随后改为单一维护文本，不再在工具层重复“原文＋补充”模式。

- round6：8/9；模型把明确14:00写入description却将time粗化为下午。保留原精度规则在规划层和工具字段中明确；从本轮起报告含实际模型response和全部参数，记录器回归RED→GREEN。
- round7：同代码整组9/9。随后将新运行资源统一为LF字节；重新验证字节一致的最终工作副本，round8为8/9，普通往返再次活动归属/跨task活动ID错误。保留通过与失败两轮，未以round7覆盖之后失败或声称长期稳定。
- 局部修改真实验收首轮失败，完整记录在source报告；程序在只读精读后仍为planning_recovery，仅允许propose。确定性回归复现。
- D7技术复核：尝试在精读后收养计划虽修复schema，但总结正确保护旧硬活动时间，仍不能将14:00改16:00；该尝试已撤除。采用用户当前明确ID经现有Inspect加载给首次Main，保持总结硬保护和Main一次。当前输入不含请求ID时的首轮快照回归先RED；补齐后真实PG编辑与lookup回归GREEN，原车次引用及供应方调用不变。新增混合否定其他行程ID负例RED后限制仅非否定分句ID。
- Technical ruling：D7补齐明确ID输入数据，不放宽总结权限；如果ID解析范围错误会误加载其他计划，已有Inspect scope/来源校验及否定/假设/旧Run/Resume测试约束。用户本轮给出的明确ID优先于隐式历史，恢复不重扫用户文本。

- round9：6/9，Main首次参数被拒后，无计划的总结沿用已有计划review，反复propose附reply，触发INVALID_REPLY_REFERENCE；记录了全部9次请求，未扩大预算。D8新增创建恢复职责，实际Eino question/parameter恢复回归先RED（仍有review）后修改，版本prompt-layers-v2。
- 完整回归同时指出PlanContextComplete不能泛化到普通历史查询；原TestTravelReplanMainAlreadyHasFullPlan失败。已限制为正在规划或本轮明确ID编辑，保留原历史查询可读范围，没有放松原断言。
- D7最终真实模型局部修改PASS：14:00→16:00，Main一次，原车次引用保留，零供应方重查；全部Usage可用。

- D8工具层同样按允许操作拆分：planning_recovery只见创建帮助，planning_summary只见修改/选择帮助，共享字段语义单独维护；工具回归先RED，确认旧描述跨阶段暴露了相反示例，拆分后GREEN。tool-instructions-v3、prompt-layers-v2。
- D7/D8后完整travel、agenttools/travel、service/trip回归PASS（8.329s/3.320s/0.301s），冻结SHA与schema原样保持。

- round10：7/9；普通往返活动归属仍错，中转重复task_id。实际response显示review“硬活动不可删、移”与跨任务纠正归属含糊相冲突，已明确硬时间不可改、任务归属必须纠正；工具层把“新增段省略”写清为省略task_id键，并用无task_id的首段JSON身份示例说明。未改变业务保护或允许硬时间移动。

- round11：9/9业务语义通过，8/9完整验收；普通往返实际纠正归属完成后amend未附reply，又重复select，效率判据失败。没有放宽duplicate_selection或额外调用判据；工具修改帮助把“最后的content修复也需同次外层reply”收敛到首条，需新查询时仍不提前总结。创建恢复看不到这段修改帮助。

- round12：8/9，普通往返纠正归属时把components放到tripplan外层，随后跨task复用了旧活动ID；原错误回归与scope未放宽。工具层明确components完整路径；新规划/已有计划修改的Main同样按请求状态只显示创建或修改示例，保持所有实际schema和工具权限。Main请求阶段回归先RED（两种意图均收到无关示例）。

## 最终验收（当前工作副本）

- round13：完整九类同轮9/9，全部实际角色、输入、response、Usage和失败事件写入最终交互报告；memory_defaults存在2次真实参数拒绝、4次模型调用，未隐藏。其余普通七类中六类2次，中转3次、政策4次。此前12轮全部保留。
- 当前指纹局部修改再验：PASS，14:00→16:00已保存，原车次引用保留，模型1次，供应方查询0次，Usage总Token8964。此前局部失败和早期通过也保留。
- 最终完整后端：436项PASS（含子测试）、0 FAIL、8 opt-in Skip；REQUIRE_DATABASE_TESTS=1，真实PostgreSQL执行。go vet与server构建exit 0；命令及本机完整输出见验收文档。普通suite跳过的交互/局部模型测试已另显式运行通过。
- 整体作者自审复核D7当前输入边界、D8创建恢复隔离、v4工具描述按请求阶段提供、前缀替换、指纹与审计、原schema/权限/业务保护；未发现未解决重要问题。冻结原提示及schema无diff，其他活动change未修改。
- 按全部需求及场景逐项人工规格核对，证据矩阵见docs/travel-prompt-layers-verification.md。当前CLI无verify命令，不声称执行OpenSpec verify；openspec validate refactor-travel-prompt-layers --strict实际通过。
