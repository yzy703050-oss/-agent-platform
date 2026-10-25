## ADDED Requirements

### Requirement: 原版收尾原因的组合优先级
差旅业务层 MUST 按冻结Python trips/service.py的确定性收尾分支处理相同operation与校验结果；暂停、取消、有效提交及部分结果不得因后续独立if无意覆盖既定结论，原因分类不得绕过原有业务校验。

#### Scenario: 政策缺口和关闭修复同时出现
- **WHEN** select、amend或confirm只剩政策阻断，且请求同时关闭局部修复
- **THEN** 保留policy_unverified；select/confirm的allow_repair=false不将其覆盖成repair_disabled

#### Scenario: 其他部分结果组合
- **WHEN** 存在酒店能力缺口、硬日期冲突、其他硬校验问题、普通待选择或显式暂停取消
- **THEN** 按原源码的条件和互斥顺序选择停止原因，硬日期冲突保留，酒店能力缺口先于关闭修复；所有原校验问题及阻断结果仍保留，选择停止原因不等于允许提交不合规结果

### Requirement: 原版执行状态转换约束
Travel内部执行状态 SHALL 通过统一转换函数执行Python runtime/state.py的9阶段转换规则。同阶段转换应幂等，未知阶段和非法边应返回结构化错误，失败不改变原状态。平台Run状态和TripPlan业务状态保持各自职责。

#### Scenario: 正常执行和工具拒绝
- **WHEN** 执行模型决策、工具批次及其校验
- **THEN** 按deciding或repair_required到executing、executing到validating的合法边执行；批次失败观察进入repair_required，成功观察回到deciding或进入合法收尾

#### Scenario: 终止状态不可重新执行
- **WHEN** completed、stopped或paused状态请求继续执行工具或跳转其他活跃阶段
- **THEN** 拒绝非法转换并保留原状态，不执行工具或产生额外业务写入；合法的平台恢复从兼容的执行快照恢复，不通过清空停止原因重开已终止状态

#### Scenario: 收尾和恢复
- **WHEN** 已保存结果需要停止、生成最终答复，或加载兼容快照
- **THEN** 收尾按stopping到reply_ready到stopped，正常结束按reply_ready到completed；快照加载检查阶段有效性，不把历史快照重建当作一次任意活跃跳转；新指纹拒绝旧不兼容快照且历史仍可查看
