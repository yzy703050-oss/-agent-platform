# OpenSpec 场景证据核对

变更：`build-agent-platform-mvp`。正式行为来源是该变更的五份 `spec.md`。本文件只映射证据，不另立规格。

当前 OpenSpec CLI 无 `verify` 子命令，项目也没有额外启用 verify 工作流；以下为人工逐项核对，**不是已运行 OpenSpec verify 的声明**。`openspec validate --strict` 只证明文档结构有效。

自动测试使用真实 PostgreSQL，以及真实 Eino + 可控模型。2026-10-23 已补齐真实模型 API 和浏览器任务验收，详见 real-model-acceptance.md。Linux race 尚无结果，8.1 保持未勾选，变更未归档。

## 身份与会话：8 个场景

| 场景 | 代码/测试证据 | 当前结果 |
| --- | --- | --- |
| 登录并登出 | TestLoginLogoutRevokesSession、TestCSRFMissing | 自动测试通过 |
| 注册标识冲突 | TestDuplicateAccount | 自动测试通过 |
| 跨用户操作 | TestCrossUserConversationAccess、TestCrossUserCheckpointDenied、TestUnauthorizedSSEDenied、TestCrossUserLifecycleDenied；前端 switchingAccountClearsPreviousWorkspace | 自动测试通过 |
| 历史分页 | TestStableHistoryCursor、TestMessageHistoryCursor、TestCompletedAnswerAdvancesPartialHistoryCursor | 自动测试通过 |
| 删除有活动任务的会话 | TestDeleteActiveConversationConflict | 自动测试通过 |
| 切换与返回历史 | testSwitchEntryCreatesConversation、testHistoryOpensOriginalAgent；本机浏览器真实 API 操作 | 自动与基础浏览器检查通过 |
| 差旅尚未接入 | TestTravelUnavailable、testTravelHasNoFakePlan；真实界面未接入状态 | 通过，无模拟行程 |
| 设置隔离 | TestSettingsIsolation、TestGetMySettingsIgnoresForgedUserID、TestRevokedSessionCannotReadSettingsToolData | 自动测试通过 |

## Agent 执行：8 个场景

| 场景 | 代码/测试证据 | 当前结果 |
| --- | --- | --- |
| 工具调用 | TestEinoChatPersistsToolsAndCompletedOutput、TestExecutionEmitsActualStreamChunks；model-smoke.ps1 与浏览器工具结果 | 框架、数据库和真实模型通过 |
| 不支持的工具 | TestUnknownToolDenied | 自动测试通过 |
| 并发重复提交 | TestConcurrentIdempotentAdmission、TestSubmitIdempotentStartsOnce | 真实数据库并发通过 |
| 幂等键误用 | TestSameKeyDifferentBody、TestSameKeyDifferentPageReturnsOriginalOwner | 自动测试通过 |
| 同会话并发 | TestSingleNonterminalRun、TestConcurrentConversationConflict、TestHTTPPageLeaseAndManualRecovery 中 run_conflict 检查 | 自动测试通过 |
| 完成与结束竞争 | TestCompletionCancelRaceHasOneTerminalOutcome、TestCompleteTransactionRollback、TestFinalPersistFailureNotSuccess | 事务竞争与失败检查通过 |
| 用户结束任务 | TestHTTPPageLeaseAndManualRecovery、TestCancelBeforeStartNeverCallsModel、TestLateOldRunCannotPolluteNew | 自动测试通过 |
| 执行达到限制 | TestIterationLimit、TestExecutionTimeoutDistinct、TestToolDeadlineDoesNotReportSuccess、TestUserRunQuota | 自动测试通过 |

## 任务恢复：9 个场景

| 场景 | 代码/测试证据 | 当前结果 |
| --- | --- | --- |
| 页面内切换 | TestPageSwitchKeepsRun、testPageSwitchKeepsHeartbeat、TestSSEDisconnectDoesNotPause | 自动测试通过 |
| 关闭发起页面 | TestOwnerPageExpiryPauses、TestOtherPageCannotHeartbeatOwner、TestNewDocumentGetsDistinctPageIdentity；真实 pagehide→paused、实际关闭后 completed/interrupted 两条结果 | 自动与实际浏览器检查通过；不保证任意停止都有安全断点 |
| 成功暂停 | TestCheckpointResumeAcrossExecutionInstances、TestStreamingSafeCheckpointResume、TestRecoveryEinoAcrossPostgresInstances、TestHTTPPageLeaseAndManualRecovery；真实模型 API/界面手动恢复 | 真实框架、数据库和模型安全边界通过 |
| 保存失败 | TestCheckpointSaveFailureNotResumable、TestPauseCheckpointFailureInterrupted、TestPauseMissingSavedCheckpointInterrupted、TestFinalizePauseRequiresCheckpoint、TestStreamingEscalationNotResumable | 不虚报 paused、不保存强制截流不安全断点的检查通过 |
| 重开会话 | testPausedOpenDoesNotResume、reopensLastConversationWithoutStartingTask、TestHTTPPageLeaseAndManualRecovery | GET 不执行与前端显示通过 |
| 重复继续 | TestResumeCASOnlyStartsOnce、TestResumeTransfersPageOwner | CAS 与页面转移通过 |
| 有效断点 | TestRestartWithCheckpointWaitsForUser、TestRecoveryEinoAcrossPostgresInstances | 重启判定与跨对象恢复通过 |
| 无有效断点 | TestRestartWithoutCheckpointInterrupted、TestIncompatibleCheckpointDoesNotExecute、TestResumeMissingCheckpoint | 自动测试通过 |
| 外部结果未知 | 人工检查 GeneralAgent 仅注册 get_my_settings，只读且经 SettingsService；无外部写入工具 | 本轮不提供外部写能力，约束已核对；不声称测过外部写回滚 |

## 上下文隔离：5 个场景

| 场景 | 代码/测试证据 | 当前结果 |
| --- | --- | --- |
| 发问而非继续 | TestSupersedeAtomicity、TestSupersedeRollbackPreservesOldTask、testNewQuestionSupersedesPausedRun；真实界面新问与数据库旧 Run cancelled/断点无效 | 自动与真实模型浏览器检查通过 |
| 上下文隔离 | TestSupersededRunVisibleButExcluded、TestLateOldRunCannotPolluteNew | 历史可见、模型输入排除通过 |
| 保留更早的有效历史 | TestSupersededRunVisibleButExcluded | 自动测试通过 |
| 迟到写入 | TestOldAttemptCheckpointWriteRejected、TestLateOldRunCannotPolluteNew、ignoresDeltasFromPreviousAttempt | 存储与前端围栏通过 |
| 点击继续 | TestResumeWithoutApprovalPayload、TestRecoveryEinoAcrossPostgresInstances、testResumeRequiresClick | 原 Run 恢复与手动触发通过 |

## 流式与交付：6 个场景

| 场景 | 代码/测试证据 | 当前结果 |
| --- | --- | --- |
| 订阅切换 | TestSSEDisconnectDoesNotPause、TestPageSwitchKeepsRun | 自动测试通过 |
| 断流后查询 | TestPartialOutputSavedOnPause、TestCompletedAnswerAdvancesPartialHistoryCursor、TestEinoChatPersistsToolsAndCompletedOutput | 已有输出与权威状态通过 |
| 慢客户端 | TestSlowSubscriberBounded、TestSubscribePersistRaceNoDuplicate、TestSSEReplayConcurrentCompletionOnce；SSE 写入时限和有界 Hub 人工检查 | 自动与代码核对通过 |
| 模型错误 | TestModelErrorRedacted、TestAuthStorageFailureRedacted、TestCSRFStorageFailureRedacted；日志只记录安全代码 | 自动与代码核对通过 |
| 未安装数据库 | TestDatabaseRequired、verify.ps1 强制测试库；正式 bootstrap 无内存回退 | 自动与启动代码核对通过；本机现已安装 PostgreSQL |
| 无模型凭据 | TestReleaseReadyWithoutModelAndRefusesSecondInstance；真实浏览器 model_unavailable | 自动与真实基础界面通过 |

## 尚未取得的证据

- Linux CI race 结果与 Compose/镜像实际构建。已提供配置，当前没有执行证据。

Linux race 按 design D11 和实施计划仍需取得实际结果后才归档；Compose 属于本轮不安装 Docker 的可选路径，继续明确未验证。测试夹具响应不作为产品真实模型回答。
