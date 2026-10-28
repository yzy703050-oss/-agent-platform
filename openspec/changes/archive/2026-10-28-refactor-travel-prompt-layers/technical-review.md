# 技术复核

日期：2026-10-28。当前代理只读核对代码后复核；无实施子代理或独立评审，未修改产品代码、未运行产品测试。设计和执行方式待用户审阅。

| 核对证据 | 问题及处理 | 追溯 |
| --- | --- | --- |
| `prompt.go`、`source_fidelity_test.go`、Python prompts.py及冻结manifest | 原文SHA是迁移证据；保留原文并另建运行资源，不能更新SHA掩盖改写 | 用户本轮“改”、主规格保真要求；D1 |
| `flow.go:453`、`prompts/no_followup.txt` | 四种角色都收到规划参数与修复指令；按职责拆分，summary不加载规划动作层 | 本change职责一致场景；D2 |
| `protocol.go:149`、`summary_recovery_test.go` | 重试只改首条并追加summary补充，可能遗留Main指令；统一整体替换、管理层标记、保留data消息 | 本change恢复场景；D3 |
| `batch_gate.go:50` | 无flow入口另有拼装，不可遗漏 | 无flow场景；D3 |
| `interaction_protocol.go:11` | 交互重试常量含整套规划与Main规则；收敛为失败事实与当前职责引用，默认规划仍由适用层提供 | 查询隔离和重试场景；D2、D3 |
| `planning_summary.go:65`、`registry.go:166` | 总结工具描述不含原TripArgumentHelp；迁移字段说明时必须补齐实际工具帮助；仅整理描述，冻结SourceProfile和schema不变 | 参数归属场景；D4 |
| `compaction_agent.go:40` | summary在未停止时现有profile映射为main；本次不顺手改变权限，比较实际工具集合与参数，并保留查询不写行程的业务回归 | 保持当前工具机制；D4 |
| `agent.go:88`、`journal.go` | 新资源或工具描述漏入兼容性指纹会允许不兼容恢复；统一版本材料，审计继续使用实际messages和实际工具 | 指纹及审计场景；D5 |
| `source_flow_test.go`、`source_scenarios_test.go`、`interaction_test.go` | 部分测试直接断言旧原文身份；只迁移该类运行断言，不删除冻结哈希或业务断言 | D6 |
| `travel_interaction_model_test.go`、`travel_source_model_test.go` | 脚本模型不能证明提示改写后的模型行为；九类交互各一次，并运行局部修改子场景，完整留证 | 现有迁移证据口径；D6 |

复核结果：未发现需要重新定义产品行为的问题；D3已补入停止专用答复层，避免将停止行程误作为独立查询。主规格中已确认的默认重庆、不追问、硬条件、政策前置、Main一次及唯一政策例外、当前计划范围及事实绑定继续适用。

OpenSpec环境：已用CLI创建change并读取proposal/specs/design/tasks指导；已安装schema仅`spec-driven`，CLI命令列表没有verify。实施后使用strict validate与人工逐项规格核对，不能声称运行verify。当前活动变更add-general-web-search和服务运行状态不在本次修改范围。
