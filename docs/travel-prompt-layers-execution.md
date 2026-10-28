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
