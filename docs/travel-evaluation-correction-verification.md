# 差旅参数纠错与真实模型验收（2026-10-25）

本次对应 OpenSpec `fix-travel-evaluation-and-correction`，由当前单代理实施、自审。用户授权修改测试、定向提示和校验反馈；原 Main / Verifier / Summary、政策提取、工具、查询、校正和停止链路继续沿用。

## 改了什么

1. **普通材料贯穿测试链路。** 用户请求、政策提取输入、工具观察、最终回复不再出现模拟、演示或虚构数据说明。测试供应方独立于生产 demo 模式，给出正常格式的车次、报价、库存、天气及政策引用。来源信息只在本报告及 JSON 元数据里，不传给模型。
2. **前置步骤与字段位置可纠正。** 明确出差却尚无政策观察时返回 `POLICY_OBSERVATION_REQUIRED`，要求保留 `business` 并先单独 `query_policy`；不再用允许的 purpose 枚举引导模型改成 `visit`。错位的 `arrival_before` 返回任务下标和 `correct_path`，要求模型按正确结构重发，服务端不静默修改用户值。
3. **根因和连带失败分开。** 预检失败的原调用保留错误码及详情，同批其他调用返回 `BATCH_ABORTED`、`failed_call_id`、`executed=false`。全部仍有配对工具观察和 journal 记录，下一次真实 Eino `Generate` 收到这些信息；预检失败不产生业务写入。
4. **提示补充可追溯。** 五份原文及辅助提示 golden 保留。新增独立 `correction-v1`，覆盖政策顺序、用途保持、参数位置、根因修正及避免重复 select。有效模型提示已经改变，不声称全部提示未经修改。补充内容纳入 `flow-v5/batch-v5/correction-v1` 工作流指纹，旧不兼容断点拒绝恢复，历史保留。
5. **能力缺口及时收尾。** 与 Python `_capability_gap_only` 对齐，已选无报价酒店及其酒店政策证据缺口可确定性部分收尾，不再额外调模型重复选择。未选车次、无关政策问题或硬冲突不能误判成该类缺口。
6. **验收检查业务语义与成本。** 用途、路线、日期、住宿、活动、原候选引用参与判断；功能与效率分别报告，拒绝、连带取消、重复选择单独计数。活动修改必须保留原 `Train.Ref`，仅检查“仍有车次”不够。

## 两次完整实测均保留

配置为真实 `deepseek-flash` 模型及 PostgreSQL，外部工具为固定夹具。每轮四类场景各一次，串行执行；小样本不声称 p95 或长期成功率。原预算不增加：主循环8次、Run100秒、模型单次30秒、工具I/O5秒、额外网络重试2次；temperature=0.3、max_tokens=8192、thinking=disabled。

**run1：3/4通过。** 多城酒店失败来自新测试供应方未对齐现有物理查询：`PhysicalQuery` 为地点查询去掉入住/离店日期，夹具却要求日期，返回错误。完整调用记录保留，不将其算成正常路径或归咎于模型。离线测试先复现失败，再修正夹具。保留现有产品投影和缓存语义；本次所有住宿均为单晚，夹具采用固定城市单晚报价300元，不验证按日变价、多晚库存。真实高德地点 API 没有因此获得报价能力。

| run1 场景 | 语义/效率 | 执行秒 | 模型次数 | 总Token |
| --- | --- | ---: | ---: | ---: |
| 车次与天气查询 | 通过/通过 | 见原始JSON | 2 | 14,214 |
| 单程出差 | 通过/通过 | 见原始JSON | 4 | 23,077 |
| 多城出差 | 失败/失败（复核） | 18.82 | 7 | 65,079 |
| 活动14点改16点 | 通过/通过 | 见原始JSON | 2 | 15,177 |

run1原始JSON当时记为“效率通过”，但自审发现其两次相同选择之间插入amend，让原计数器清零，漏掉一次重复；现已用失败回归修正，不因中间出现amend就清除相同候选历史。复核判为语义、效率均失败（1次重复选择）。首轮JSON保持原样供核对，不隐瞒当时漏判；酒店查询失败也引发额外修复，整例失败。原始记录：[run1](reports/travel-evaluation-correction-2026-10-25-run1.json)。

**修正夹具后的 run2：4/4通过。** 一次完整整组结果如下，没有从多轮挑选样本拼成全绿。

| run2 场景 | 执行秒 | 模型次数 | 输入Token | 输出Token | 总Token | 拒绝批次 | 重复选择 |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 车次与天气查询 | 1.94 | 2 | 13,962 | 228 | 14,190 | 0 | 0 |
| 单程出差 | 6.78 | 4 | 21,641 | 1,246 | 22,887 | 0 | 0 |
| 多城出差 | 10.72 | 4 | 26,035 | 2,471 | 28,506 | 0 | 0 |
| 活动14点改16点 | 2.56 | 2 | 14,762 | 396 | 15,158 | 0 | 0 |

原始记录含每次真实模型输入（messages/tools）、各阶段 Usage/耗时、工具调用与返回、业务落库及最终回复：[run2](reports/travel-evaluation-correction-2026-10-25-run2.json)。

多城四次模型调用依次是：Main 调政策 → 政策辅助模型提取 → Main 提交三段规划并触发确定性查询 → Verifier 一次选择三段车次及两处酒店，绑定最终答复。最终渲染直接使用已保存结果，无第五次模型总结。局部修改两次均为 Main，不重新查询供应方。

计量从 `NewExecution + Start` 到最终 Run 持久化，排除迁移、夹具初始化及活动修改种子准备；包含本机数据库耗时，未包含浏览器/HTTP/SSE。Token仅来自供应方Usage；本轮均完整。模型次数统计实际Generate，SDK内部HTTP重试不另算。差旅为非流式，first_event不是首Token延迟。供应方提示缓存不受控。

## 验证证据及规格核对

| 需求/任务 | 对应代码或验证 | 结果 |
| --- | --- | --- |
| 原文提示保留、补充生效和指纹 | `TestTravelSourcePrompts`、实际模型输入、correction反馈测试 | 通过 |
| 正常材料不泄漏来源；用途/路线/日期/活动/refs | `TestTravelEvaluationContract`、run2四场景 | 通过 |
| 政策前置、错位路径及零副作用 | `TestTravelCorrectionFeedback` RED→GREEN | 通过 |
| 根因/连带观察经journal进入下一Generate | `TestTravelCorrectionReachesNextModel` RED→GREEN | 通过 |
| 酒店政策报价缺口及误判保护 | `TestSourceHotelPolicyCapabilityGap` RED→GREEN | 通过 |
| 保存能力结果后不再调用模型 | `TestCapabilityResultStopsWithoutAnotherModelCall` | 通过 |
| 测试供应方遵守实际物理查询 | `TestTravelEvaluationContract/hotel_fixture_accepts_real_physical_request` RED→GREEN | 通过 |
| 全部Go与真实PostgreSQL回归 | `REQUIRE_DATABASE_TESTS=1 go test ./... -count=1 -timeout180s` | 通过，integration37.845秒 |
| Go静态检查/服务端构建 | `go vet ./...` / `go build ./cmd/server` | 通过 |
| 前端 | `npm test -- --run` / `npm run build` | 15/15通过、构建通过 |
| 当前提交Linux race/真实PostgreSQL | GitHub Actions | 待运行 |
| 规格与任务一致性 | 人工逐项核对及OpenSpec strict validate | 待最终核验 |

未修改原Python项目；没有运行Python模型对照。本轮不包含真实聚合数据、高德、RAG MCP在线全链路、UI回归、部署、负载或生产稳定性验证。生产显式demo模式仍带来源说明，本次只改变性能测试夹具。公开报告不会把固定报价描述成真实可预订库存。

OpenSpec CLI没有verify命令，采用上表人工核对和strict validate，不声称运行OpenSpec verify。重要检查完成前不归档变更。
