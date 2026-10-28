# 差旅提示词分层验收

日期：2026-10-28。执行：用户选择单代理，当前代理实施、TDD及整体自审，无实施或独立评审子代理。

## 最终结果

- 完整后端：436项PASS（含子测试）、0失败、8项外部依赖opt-in Skip；REQUIRE_DATABASE_TESTS=1，真实PostgreSQL集成执行通过。
- go vet ./...、server构建均exit 0。最新输出见本机.cache/reports/travel-prompt-layers-*-final.*。
- 最终工作副本第13轮真实模型＋PostgreSQL：同一代码整组9/9通过，未从不同轮挑选样本。每类1次，不代表长期稳定性。
- 当前指纹下局部活动修改：14:00→16:00实际持久化，原车次引用保留，供应方查询0次，模型1次，Usage完整。
- 冻结五份角色/公共提示、辅助golden、工具schema及TripArgumentHelp的原SHA测试通过；这些原文与原schema无diff。未运行Python模型对照，也未改写Python源项目。

## 最终模型样本

| 场景 | 通过 | 实际模型调用 | 根参数拒绝 | 重复选择 | 总Token | 执行毫秒 |
| --- | --- | ---: | ---: | ---: | ---: | ---: |
| sparse | 是 | 2 | 0 | 0 | 19141 | 8003.1 |
| explicit_defaults_override | 是 | 2 | 0 | 0 | 16822 | 4181.1 |
| memory_defaults | 是 | 4 | 2 | 0 | 34416 | 14327.0 |
| normal_roundtrip | 是 | 2 | 0 | 0 | 19685 | 7521.0 |
| multicity | 是 | 2 | 0 | 0 | 21309 | 7843.8 |
| query | 是 | 2 | 0 | 0 | 16484 | 2842.8 |
| policy | 是 | 4 | 0 | 0 | 26363 | 8450.7 |
| replan | 是 | 3 | 0 | 0 | 34304 | 12437.2 |
| hotel_gap | 是 | 2 | 0 | 0 | 19181 | 7040.7 |

最终9例无主动历史读取、无重复选择、无无理由额外调用、无非政策Main重入。记忆场景有两次真实参数校正、共4次调用，不能表述为全场景零拒绝或全部两次；正常非政策非中转的7例中6例两次，政策4次、中转3次。模型Usage来自供应方响应，不用静态字数当实际Token。

局部修改总Token=8964，调用=1。

完整当前证据：[九场景](reports/travel-prompt-layers-interaction-2026-10-28.json)、[局部修改](reports/travel-prompt-layers-source-2026-10-28.json)。包含实际输入、工具描述/schema、阶段、Usage、回执、保存计划与失败；从交互第6轮起补录实际模型响应参数。第1–5轮仅保留当时已有输入/事件/结果，未伪造缺失的响应。

## 历史轮次全部保留

| 轮次 | 完整通过 | 证据 |
| --- | --- | --- |
| 1 | 7/9 | [完整JSON](reports/travel-prompt-layers-interaction-round1-2026-10-28.json) |
| 2 | 8/9 | [完整JSON](reports/travel-prompt-layers-interaction-round2-2026-10-28.json) |
| 3 | 8/9 | [完整JSON](reports/travel-prompt-layers-interaction-round3-2026-10-28.json) |
| 4 | 8/9 | [完整JSON](reports/travel-prompt-layers-interaction-round4-2026-10-28.json) |
| 5 | 8/9 | [完整JSON](reports/travel-prompt-layers-interaction-round5-2026-10-28.json) |
| 6 | 8/9 | [完整JSON](reports/travel-prompt-layers-interaction-round6-2026-10-28.json) |
| 7 | 9/9 | [完整JSON](reports/travel-prompt-layers-interaction-round7-2026-10-28.json) |
| 8 | 8/9 | [完整JSON](reports/travel-prompt-layers-interaction-round8-2026-10-28.json) |
| 9 | 6/9 | [完整JSON](reports/travel-prompt-layers-interaction-round9-2026-10-28.json) |
| 10 | 7/9 | [完整JSON](reports/travel-prompt-layers-interaction-round10-2026-10-28.json) |
| 11 | 8/9 | [完整JSON](reports/travel-prompt-layers-interaction-round11-2026-10-28.json) |
| 12 | 8/9 | [完整JSON](reports/travel-prompt-layers-interaction-round12-2026-10-28.json) |

第7轮曾9/9，之后仍有失败，均保留；最终第13轮是在分开创建恢复、按Main请求阶段分工具说明，并明确活动归属/ID/组件路径后通过。不能把多轮试验说成首轮通过或全程零错误。逐次根因及RED→GREEN见[实施记录](travel-prompt-layers-execution.md)。

局部修改首轮因旧计划未加载进入创建schema失败，保留[source round1](reports/travel-prompt-layers-source-round1-2026-10-28.json)。补齐当前明确ID的受权快照后，旧通过轮保留[source round2](reports/travel-prompt-layers-source-round2-2026-10-28.json)，最终在当前分层指纹下重新执行通过。

## 命令与证据边界

从backend执行：

```powershell
go test ./... -json -count=1 -timeout 180s
go vet ./...
go build -o ../.cache/bin/travel-prompt-layers-server.exe ./cmd/server
go test ./tests/integration -run '^TestTravelInteractionModel$' -v -count=1 -timeout 20m
go test ./tests/integration -run '^TestTravelSourceModel$/^modify_activity$' -v -count=1 -timeout 180s
```

数据库设置REQUIRE_DATABASE_TESTS=1；真实模型分别显式开启REQUIRE_TRAVEL_INTERACTION_MODEL_TEST和REQUIRE_TRAVEL_SOURCE_MODEL_TEST，重复数1，完整JSON输出路径见上。完整普通suite中的两个模型Skip不计作模型通过，实际通过来自显式执行。其余未启用的在线MCP、供应方、通用搜索等不在本轮验证范围；固定供应方/政策夹具不证明在线供应方能力。未做部署、浏览器UI或新的Linux race/CI验收。

## 单代理整体审查与人工规格核对

| 要求/风险 | 实现及证据 |
| --- | --- |
| 原版提示与工具契约保真 | source_fidelity_test、source_contracts_test原SHA保留；运行资源独立版本化并有逐段映射 |
| 查询/规划/创建恢复隔离 | composePromptPrefix、按请求阶段的工具描述；TestTravelPromptLayers、TestRuntimeToolInstructions、TestRuntimeMainRequestInstructions、真实Eino恢复回归 |
| Main失败后不遗留旧层 | replacePromptPrefix只移除受管规则，数据和工具配对逐字保留；question/parameter恢复与journal真实hash回归 |
| 停止不重开规划 | stopped专用层，既有停止profile与预算保护；停止Eino输入和TestStoppedGenerationCannotRebindPlanningTools |
| 当前明确ID的局部修改 | sourceInput仅识别本轮常见中文编辑语句中的标准行程UUID，沿用Inspect权属/来源核验；否定/假设/旧Run/Resume及mixed-negative测试，lookup不修改，真实PG修改保留原车次 |
| 硬条件与跨任务活动 | service规则未改；硬日期/晚数/预算和活动保护回归、多城/中转真实结果；纠正归属保留硬活动含义与日期时段 |
| 指纹与恢复 | prompt-layers-v2、tool-instructions-v4、明确ID上下文版本及模式材料入指纹；旧不兼容断点按原规则拒绝，重放及回执集成通过 |
| 正常调用与真实收尾 | 当前整组9例的实际调用、拒绝、重复、最终修订均核对；局部修改一次模型、零重查；无放宽判据 |

ID识别用于补齐数据，不是授权判据；其他表达沿用既有流程，所有加载仍经原Inspect。普通历史查询的权限过滤未扩大或统一禁用，既有回归保持。

整体自审未遗留本次修改的重大规格/质量问题。重要技术调整D7/D8及工具单一维护文本已回写design和计划；没有派独立评审代理。

OpenSpec CLI已安装spec-driven，命令列表无verify；没有声称运行OpenSpec verify。已人工对照本change及travel-planning/travel-execution逐项核对需求、场景、任务和上述测试证据，strict validate单列执行。归档证据在执行记录追加，归档前不勾选归档任务。

归档已成功：`openspec/changes/archive/2026-10-28-refactor-travel-prompt-layers`。主规格同步新增2条、修改1条；`openspec validate --all --strict`实际12项通过、0失败。
