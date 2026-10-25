## 1 停止原因对齐
- [x] 1.1 组合回归复现政策与关闭修复优先级错误，覆盖暂停取消、有效提交、硬日期、一般硬冲突及酒店能力缺口。
- [x] 1.2 按原互斥顺序修复，相关Service回归通过，记录RED/GREEN证据。

## 2 状态转换对齐
- [x] 2.1 原转换表与终态保护综合回归先失败，再实现统一转换入口并接入所有活跃阶段赋值。
- [x] 2.2 对齐批次错误和最终收尾路径，更新指纹；现有Eino、协议错误、拒绝和恢复回归通过。

## 3 验收及PR
- [ ] 3.1 完整Go/真实PostgreSQL、vet/build及Linux race CI通过；单代理整体自审与人工规格核对、strict validate完成。
- [ ] 3.2 完成归档和说明，从codex/travel-stop-phase-parity向feat/agent-platform-mvp创建PR，附本任务。
