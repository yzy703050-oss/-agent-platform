## Why

南京中转样本的第二次模型仅提交request_replan，第三次Main才提出修复，形成可合并的两轮。用户明确要求Main只规划一次，只有差旅政策前置允许再次Main；后续结果、纠错与重排由总结Agent接手。

## What Changes

- Main首次提出计划及查询地图，程序工具完成后自动handoff规划总结Agent；交接不新增模型调用。
- 规划总结在同一次模型响应直接修改当前路线与受影响查询，不再以request_replan回Main；结果返回后继续总结审查并绑定真实选择。
- 政策前置最多允许一次Main续规划；参数/交互重新生成由规划总结恢复，不能借错误重新规划Main。
- 独立查询Summary保持原范围；原版提示及schema保留为基线，授权扩展使用独立运行profile及提示、指纹。

## Capabilities

### New Capabilities

无。

### Modified Capabilities

- `travel-execution`: Main一次与政策例外、总结直接重排和实际角色计数。
- `travel-source-fidelity`: 用户授权的总结阶段能力扩展与原版基线分离追踪。

## Impact

差旅流程状态/提示/协议恢复、原生profile与预检、可信业务角色及硬条件保护、journal输入/tool hash与恢复、模型阶段计量、相关单元/PG和真实模型测试。无对外HTTP变更，不增加轮数、改用户硬条件或写入伪造记忆。
