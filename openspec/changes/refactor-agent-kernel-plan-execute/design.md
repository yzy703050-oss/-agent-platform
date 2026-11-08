## Context

更新：2026-11-06。本设计收拢2026-11-02至11-06的有效结论；重复讨论和已撤回方案的原稿不再保留。阅读入口为[整体架构](../../../docs/architecture-final/README.md)，本周关键取舍见[精简决策记录](../../../docs/architecture-final/03-decisions-and-alternatives.md)。正式行为以本change的[specs](specs/)为准。

当前Go工程已有Gin、Cookie/PG Session/CSRF、Eino运行封装、差旅领域服务、PG迁移及React前端。Eino固定v0.9.0-beta.1；新Kafka/Outbox/Dispatch、独立Travel可靠链路及多实例装配尚未实现。现有单实例锁、启动迁移和stdio RAG需要适配，不能以文档更新代替实现证据。

## Goals / Non-Goals

- 保留工程底座，按双入口、共享查询和独立专业服务的边界替换旧控制流程。
- 建立可靠异步交接、版本化规划、证据复用和有界恢复；新版本产生的原始用户及业务数据按规格保存；旧数据库按2026-11-07用户决定不保留。
- 本轮仅整理设计，不改产品代码、执行DDL或安装服务；不升级Eino。
- 不提供活动Run补充、用户暂停/继续/整轮取消；页面离线不停止执行。业务安排的取消仍通过受控计划Patch处理。
- 不新增动态Agent发布平台、预订/支付能力或每个内部角色的微服务。

## Decisions

### 1. 服务与执行边界

General与差旅助手保留两个会话入口，共用身份、Run、记忆及查询底座。两者可以直接查询酒店、交通、天气、网页和RAG；纯查询不建立TripPlan或计划工作线程。创建/修改正式计划、保存候选选择进入同一Travel专业能力，差旅入口不先经过General。

子Agent调用链为：平台本地事务保存父checkpoint、原调用、Delegation、Outbox与waiting → Kafka → 平台Dispatch Worker → MCP持久提交 → 独立Travel。Travel自己拥有Task、执行池、现场、租约及结果；平台通过MCP收集真实结果，再本地提交依赖满足及父ready。不跨服务业务联合事务，Travel不直接消费另一份平台执行消息或修改父Job。

平台入口Job和父ready由PG执行池认领；Kafka仅分发子Agent调用，不为Planner/Executor/Replanner、RAG或父续跑增加Topic。建议2 API、2平台Worker、2 Travel、1 Maintenance；entry与Dispatch分池。槽位及连接池等起点见整体架构参数表，均需在实际环境验证。

### 2. 任务身份、事务与恢复

一个用户问题对应一个Run，同会话已有非终态Run时不同新请求返回run_conflict；同幂等键同参数返回原Run。waiting及系统恢复不解除会话互斥，不新增用户Run。

- 父Job保存完整兼容现场、原调用、依赖、委派、Outbox及waiting的事务成功后，清执行租约、退出释放槽位，不续租；ready后重新认领新lease_epoch。
- Outbox只后台定时轮询。Publisher短事务SKIP LOCKED认领并增加publisher_epoch，事务外发送，broker确认后按owner/epoch和有效租约标已发。重复发送沿用message_id。
- Dispatch在PG校验并持久接手后可以确认连续安全offset，MCP调用在事务外；巡检必须恢复已接手但未提交/提交未知的操作，不能只依赖Kafka重投。
- Delegation第一次创建即保存稳定request_key和不可变输入/hash。Travel用可信caller、主体和key的唯一约束汇合并发，同key同hash返回原Task，不同hash冲突；受理事务提交后才返回凭据。
- MCP提交超时先查询或同key重提，不能推断远端未执行。已受理queued/running继续查原Task，accepted不能当作真实专业成功。
- 用户已确认：远端Task最终failed，收集失败及可用部分结果交回父Agent，不自动新建Task或换key重做。父已终态、期限或授权失效则不恢复。
- checkpoint保存位置，Thread保存上下文，正式业务/证据/成功回执保存事实。恢复先核验兼容指纹和当前业务状态，优先复用已存结果；不承诺零重算或外部调用恰好一次。

完整发布、受理、offset、幂等、租约与巡检协议见[MQ＋MCP附录](mq-mcp-dispatch-protocol.md)。

### 3. 时间、重试与资源

用户确认Run默认总期限300秒，接纳时固定global_deadline_at；排队、waiting、退避、恢复和父结果表达均计入。子deadline不晚于祖先窗口，工具逻辑deadline覆盖全部尝试及等待；续租不延长业务期限。发布、接纳、MCP受理、新调用、正式提交、结果汇聚及父恢复均核验状态/授权/期限/代次。

有效running执行者独立续租；续租保持epoch，要求当前owner/epoch、旧租约未过期及deadline允许。过期即使无人接管也不能自行续回。巡检以锁/CAS复核候选后才能接管，旧代次写入被围栏拒绝。waiting无执行租约正常；同步RAG/API等待仍占所属槽位并续租。

最多3次重试、4次总尝试，full jitter窗口U(0,1s)、U(0,2s)、U(0,4s)。次数及预算持久保留，不因换Worker、重投、恢复而清零；SDK/HTTP/Job不能叠乘同一网络调用的重试。Retry-After与剩余期限共同限制，最终失败、参数/授权错误、正常空结果及未知外部写不盲目重放。

- 子Agent提交的持久延迟：PG not_before＋Outbox轮询，到期再投Kafka；资格、次数、稳定身份、下一时刻与唯一Outbox同事务保存。
- 工具短退避：可取消本地timer，不经MQ、不需Redis；保持running租约/槽位，等待不占PG连接。调用身份、次数、结果、预算仍持久化；严格保留跨重启退避时刻时保存next_retry_at。
- 全局deadline到期可靠终结，不巡检重试续命。局部超时/失权仅在剩余次数、预算、授权和安全恢复允许时恢复。
- Redis作缓存/共享预准入及SSE提示，PG调用账、资源约束与执行围栏守正式硬上限；Redis故障不得退回每实例各自满额。熔断先沿用进程内provider/account/endpoint维度，阈值作为可配置起点。

### 4. Travel编排、上下文与计划

保留AgentAsTool与Plan-Execute/LoopAgent方向，内部Planner、确定性Executor、Replanner不各拆服务。规划准备按需检索政策；Planner只提定义，Executor读取正式已提交Task执行查询，Replanner提出受控Patch。固定转换、校验、提交及投影优先Chain/Lambda，工具分派ToolsNode；领域事务仍由Service/Repository完成，框架状态不代表正式提交。

一份TripPlan持续对应一个TravelThread，新计划新线程，后续修改/原调用恢复复用。首次传必要父上下文快照，之后加载原子线程历史＋本次新要求＋相关主线程增量，保留来源。入口只接收相关计划目录、必要摘要和真实结果；模型结合上下文判断新建/继续，不因关联不明确固定追问。程序继续校验目标归属、线程关系及版本。

| 对象/操作 | 设计 |
| --- | --- |
| Stop/Leg | 稳定ID表示一次停留及前后关联，同城市重访不混用 |
| Task/Item | Task保存完整查询定义，Item仅为查询动作及真实数据依赖 |
| 版本/失效 | Task有效定义含关联路线及适用要求；该Task变化使全部Item重查，无关Task不因整体升版失效 |
| selection/refresh | 选择不增加查询定义版本；显式refresh增加执行generation |
| Patch | 稳定ID业务操作，expected_revision、受限set/unset，私有副本顺序应用后完整图校验，整次原子提交 |
| 正式事实 | JSONB版本快照、证据、执行报告与actions成功结果；不单设travel_decisions或第二份成功回执事实 |

政策＞用户＞记忆供模型规划和排序，不做hard/soft候选业务过滤。程序保护结构、权限、真实引用、金额和版本；未知价格为null，不编造报价/预订能力。必需查询参数缺失不发猜测请求，政策未核验可保存已知范围最小计划并说明未知。call_status、业务目标完成程度及coverage分别表达。

详细DTO、Patch与错误见[组件契约](contracts/README.md)，数据与迁移映射见[数据库附录](database/README.md)和[迁移设计](database/migration.md)。跨服务物理映射必须复核，不按旧固定表数生成DDL。

跨服务证据传输快照在接收方evidence_imports保存，按原service/id/revision/hash核验，使用受控命名空间引用；不制造假本地工具调用，不把原tool_call_record_id建为本地FK。详细映射见数据库5.2，来源失效/撤回和新鲜度仍须核验。此为原共享证据要求的物理实现，不增加另一份可修改事实。

用户于2026-11-06确认Travel最多8个“Executor＋Replanner”轮次，连续2轮无实质进展结束。首版Planner不算一轮，其首次正式提交作为比较基线；一次轮次的执行报告及Replanner决定完成后原子推进计数，网络重试不增加轮次，恢复不重复推进。比较正式定义、当前有效证据内容/适用版本及选择；忽略解释文本、回执重复、时间戳和纯审计修订。同值刷新不能仅凭新证据ID算进展。实质变化才重置连续计数；定义来回改动仍受8轮/累计预算/300秒限制。收尾从正式事实生成partial/blocked及原因，不强行再调用模型耗尽总期限。

### 4.1 Eino运行接缝复核结论

本机隔离探针已验证固定v0.9.0-beta.1的`StatefulInterrupt -> AgentAsTool CompositeInterrupt -> Runner CheckPointStore.Set`保存嵌套现场，重建Runner后`ResumeWithParams(Targets)`恢复原调用；两次连续委派均正确配对，错误target保持等待，Set失败可观测。证据及局限见[技术复核](engineering-review.md)。

入口使用同进程轻量RemoteTravelBridge Agent包装远端能力，再经`NewAgentTool`暴露；桥的首次Run只形成不可变委派提案并中断，不先发MCP。最外层Store捕获完整序列化bytes，执行者排空事件并确认无checkpoint错误，再由Repository把bytes、原调用/interrupt target、Delegation、Outbox、Job waiting及清租约一次提交。提交失败无发布资格；不能把叶子state或已生成Interrupted事件当作联合提交成功。中断事件不交旧Manager当正常完成，也不走用户暂停/页面租约路径。

父恢复只以可靠收集的原结果定向恢复叶子target；桥Resume校验原身份后输出确定性序列化`TravelCallResult`，不再次创建委派。SDK默认取最终消息文本，不能仅设置CustomizedOutput。`NewAgentTool`共享父SessionValues的键空间，所有可变状态按job/call/plan命名，不放连接、密钥、可变全局计划或不可序列化对象；真正Travel在独立服务拥有自己的Runner/线程。工具并行导致多个中断时先聚合全部依赖，未满足的叶子重中断，不使用无差别Resume放行。

### 5. 数据、身份、事件与记忆

用户确认平台/Travel共用PG实例，按不同schema、数据库角色和权限隔离；服务不直接读写对方业务表。迁移角色与运行角色分开，公共授权/预算账采用明确受控边界，不隐含授予整个schema访问权。RAG管理库与业务PG分开。

技术复核采用受限control schema函数承担同步授权guard、scope及唯一调用账，白名单函数基于数据库登录角色限定服务身份；Session/Run/上下文变化与其guard本地同事务，业务提交持guard锁裁决撤回竞争。没有跨平台/Travel业务联合事务或直接表访问。具体字段、接口、角色及锁序见[公共协议2.1](engineering-protocol.md#21-公共授权与调用账的受控边界)。部署需显式给有限预算上界，不在实现中猜付费额度。

A03实施细化（2026-11-08）：control增量002提供授权、guard及调用账白名单，platform增量002提供Session创建/撤销包装；包装以platform owner操作本地Session，在同事务调用control入口，登录身份始终使用session_user。运行账号继续无Session写权限。scope不可变定义由数据库生成SHA-256摘要；预算四维使用非负整数model_attempts/tool_attempts/token_upper_bound/cost_upper_micros，前台按source_run_id汇总、后台按operation_id汇总。结算保留尝试次数，只有已证实未发送才全额释放；未知用量保留预占。当前租约校验由各服务仓储在准入同事务执行，control不直接访问业务表。Redis重建在PG resource行锁内取原调用占用并递增generation，Lua携generation拒绝旧持有者，不把Redis作为正式授权账。

control增量003补齐control_rpc：仍在原outbound_calls登记、受资源硬并发和scope绝对期限约束，供应方四维预占均为0；其独立轮询频率与传输重试由Dispatch/Collector负责，不冒充模型或工具尝试。002已在本地应用，因此不改写其SQL或摘要，以003增量调整。Run的固定deadline与Session绝对到期独立保存；接纳不因Session剩余时间较短而改写Run窗口，每次授权仍检查Session真实到期。

A04实施细化：接纳先持原Session/context guard及`run:active_per_user`配置行的共享锁，再锁platform.users额度行、conversation和Run/Job。额度从非终态Run核对，含waiting；该资源行必须由部署显式配置，不按API副本各给一份。接纳内部新建的Run guard/scope只涉及本事务新身份以及已经持有的guard，不反向等待其他既有control行。时间取锁后PG clock_timestamp，created_at与deadline使用同一接纳时刻；请求hash在服务端按原始content的SHA-256生成，保留既有16000字节正文/200字节幂等键边界。

认领、续租及终态属于入口控制事务，从开始就持Run guard UPDATE锁，避免完成时SHARE→UPDATE升级与预算准入/终态竞争。JobLease补充RunVersion与接纳时LimitSnapshot，FinishInput显式携ExpectedRunVersion；持有真实worker/epoch、当前未过期租约且版本匹配才能提交。系统失效收尾使用只对platform/maintenance开放的受控入口，按真实deadline/撤回/上下文失效判定，不能凭调用者声明超时结束有效Run。失权Job只有匹配内核/配置、版本及payload摘要的持久断点才能认领；无可验证现场则可靠interrupted。断点正文的Eino解析及定向恢复仍由A08负责，认领不代替恢复执行验证。

A04入口事务显式使用READ COMMITTED，不能依赖连接默认隔离设置；用户行锁序列化后，额度COUNT和幂等回读须看见前一事务刚提交的Run。失权running接管累计原Job.retry_count，最多三次恢复，正常waiting→ready不额外算故障重试；无有效限额快照则可靠interrupted。终态消息/事件写入后再次核验授权、固定deadline和租约，随后关闭Run guard并提交，存储等待跨过期限时整体回滚。

A05实施细化：platformpg/travelpg以各自连接委托固定platform/travel分支的runtimepg存储公共逻辑，统一授权/租约、调用账、证据及模型输出，不能传任意schema或取得对方业务权限。outbound_calls增补不可变输入摘要与持久next_retry_at，仍为唯一物理调用账；受控函数只向本服务返回相应scope的历史。Run预算附带原LimitSnapshot/兼容指纹，后台scope在签发时包含独立限额，Travel通过control读取该投影而不读取platform表。

A05证据传输采用scope.source_scope.evidence数组逐项授予`origin_service/origin_id/origin_revision/content_hash`，服务Bearer映射稳定peer身份；源服务仅通过peer_evidence_context核对其本地事实的精确授权，不能把请求中的service字符串当身份。GET `/internal/v1/evidence/:id/versions/:rev`携执行scope ID/hash与预期内容摘要，返回JCS规范化的原记录；跨服务导入每次复用均重新向源端校验，网络前后各核验调用方自己的Task/Job围栏。imports保留原payload及授权来源，不创建远端事实对应的本地tool_calls。首次保存的返回值从PG读取，确保微秒时间精度与后续传输摘要一致。模型输出允许后台operation归属，run_id与operation_id互斥，仍保存在本服务messages并不可变。

运行时模型由NewRuntime提供单次HTTP传输与运维验证过的输入硬上界，再由NewMetered包装Generate/Stream/WithTools；绑定工具冻结，指纹覆盖原输入、实际通用生成选项、工具Schema、模型配置和原执行配置。只转发受支持的Eino通用选项；供应方专用setter不进入实际请求。正数费用预算须配置明确的每百万token微额单价，缺失用量保持未知，不采纳SDK合成的零值。HTTP已知可重试状态使用持久1/2/4秒全抖动窗口及Retry-After，最多四次；未知物理完成状态保留原hold，不透明重发。完整流先入库再EOF，半流仅输出草稿并失败。MCP显式固定2025-11-25，禁独立SSE与自动重连；复用服务会话/连接池但禁止请求body重放。网页运行时继续保留官方解析器和注册Schema，只取一次页面响应，抑制SDK自动翻页，以免在一次记账内追加物理请求；旧入口的客户端在后续装配切换前保持原路径。

A06实施细化：计划提交使用持授权guard和本地Task/Thread的短事务，在私有副本上完成稳定ID操作、最终图校验和有效Task版本比较，原子保存版本指针、完整修订、执行安排及action回执。已有scope预算/租约控制沿用A05，不跨网络持事务。计划内容版本、Task定义版本和执行generation各自维护，查询结果不能改写Task定义；未知输入保持blocked。为验证跨服务复用的输入，新增证据在snapshot.query_context保留程序从原tool_call写入的domain/mode/provider/arguments/input_hash/coverage，供应方不能覆盖这些字段；旧记录缺少该证明时拒绝按输入复用，不更改已应用迁移或改写旧事实。新执行固定使用`exec:<execution_step_id>`调用键；Task变更或显式刷新后，旧查询事实不能通过相同参数摘要满足新代次。查询失败ErrorDetail须符合原正式枚举，在A06接入时增加落库失败体的严格协议回归。

A06将逐项完整ExecutionResult存入travel.execution_steps.result，允许首次填入后保持不可变，旧代次转stale不改写原结果。落实公共协议已定义的read_related_memory：control函数核验memory能力与`memory:<domain>`目标，再委托platform owner持有的私有投影函数；control owner只获该函数EXECUTE及namespace USAGE，不获业务表SELECT，Travel运行角色仍无platform表/namespace权限。投影持platform用户SHARE锁，与记忆写入用户UPDATE锁序列化，仅读最新有效条目；Travel另受签发scope.source_scope.memory_refs中memory_id/revision精确清单限制。用户要求核验本Thread可读user消息；政策来源核验已授权且有效的实际检索片段，不从要求文本推定来源等级。

平台messages增加互斥的operation归属，后台model_output不借用旧Run；operation/user/conversation/thread通过本地复合FK匹配。完整model_output按稳定key及输入/模型/工具指纹不可变保存，当前授权/来源仍须检查，之后才能返回可用完整结果。计量模型使用已注册的单次发送客户端及显式硬输入/输出界限；正费用预算还需要明确单位定价，未知用量不退款。流式增量只作草稿，完整输出保存失败或半流错误不能透明重开另一调用。

工具注册项固定domain/mode/provider、受控资源及查询参数适配；未配置能力不发送。每个逻辑调用第一次固定deadline，重试沿用该期限与原key，已完成的同key调用不因force_refresh再次执行，刷新须由上层生成新的generation/key。跨服务证据快照校验完整正文摘要、原身份、来源/上下文和时效，保留原candidate_id，仅在接收方使用受控remote命名空间。

实施细化：JCS使用已核对源码/API的`github.com/gowebpki/jcs v1.0.1`之`Transform([]byte)`，在其前增加重复键、非法Unicode、非有限/不安全数及工具Schema检查；标准库JSON排序不能替代JCS。该依赖尚未加入go.mod或运行项目编译，必须在实施计划的依赖与RFC向量测试中验证；来源为[固定版本源码](https://github.com/gowebpki/jcs/tree/v1.0.1)。新增运行代码使用独立platformpg/travelpg消费方仓储，现有postgres仅为尚未替换旧代码的回归基线，新部署不使用它读取旧数据库，避免同一个Repository凭据同时写两个业务schema。

Cookie/PG Session/CSRF沿用，首版不缓存认证正结果。Run固定原Session，撤销/过期后阻止新有效推进；跨服务核验与撤回传播必须落实，API Key不能代替用户执行授权。Maintenance后台记忆有独立服务授权、操作、租约和预算。

Run正式事件在PG按seq持久化，与状态/结果同事务。Redis只提示回读，SSE丢通知仍定时补读；临时delta按stream/epoch/序号处理，不推进持久游标，断线靠快照/最终消息恢复。前端重连不重复提交问题，慢连接不阻塞Worker。具体认证、Redis及SSE规则见[公共基础设施协议](engineering-protocol.md)。

短期记忆沿用20,000估算token预算、80%检查、每次最多三个旧完整轮次，保留当前Run及工具配对；摘要和覆盖一起版本提交，清除/撤回不经摘要重新引入。各Thread状态独立，不能用摘要替代checkpoint或业务事实。

长期记忆按user统一并区分领域；每入口主Thread新增10轮成功有效用户对话，由Maintenance检查必要性，需要才调用MemoryAgent，允许空更新。reviewed与summarized分开，失败不伪造覆盖；用户明确记住/修改/撤回由入口模型调用确定性工具，不等阈值、不做关键词识别。

### 6. 同步RAG与迁移

平台/Travel通过HTTP MCP同步取得完整证据；RAG不作子Agent，不建Job/请求结果Topic或Outbox。调用方保持running、续租和占槽，已有证据优先复用，未存只读查询允许预算内必要重算。检索、索引、管理、时间感知及评测的正式设计在[RAG change](../refactor-rag-mcp-retrieval-service/design.md)。

用户于2026-11-07确认从空库部署，旧数据库数据不要求保留或兼容。新部署不执行001～005，不导入旧身份、业务历史、记忆或断点；启用新链路前停止旧执行者。原迁移文件仅用于剩余旧代码的工程回归。新版本产生的数据继续按当前规格审计、隔离和恢复。

### 7. 技术复核与实施进入条件

2026-11-06技术复核已对照两change、实际Go/Python源码和活跃搜索变更完成一轮检查。Eino/HTTP MCP的依赖探针可行，旧代码不具备新事务/围栏；已修正文档中跨服务FK与共享消息序号的遗留描述。当前有效结论、依赖选型、实际检查与未关闭项统一见[engineering-review.md](engineering-review.md)，不新增讨论快照。任务勾选以具体证据为准，不因OpenSpec status显示材料齐全就把实施视为完成。

| 待完成项 | 完成证据 |
| --- | --- |
| Eino接缝 | 完整checkpoint捕获、原调用定向恢复、多次嵌套状态隔离与提交后回执复用 |
| 跨服务契约 | MCP Schema、固定输入/幂等、授权撤回、预算分配/结算、结果及幂等保留期 |
| 持久结构 | Task/Delegation/Dispatch/Outbox物理映射、约束/索引、锁序、schema grants及增量迁移 |
| 实际环境 | 精确依赖/镜像/模型revision、隔离PG、Kafka/Redis/MCP多实例及资源配置 |
| 详细计划 | writing-plans引用本specs/design/tasks，明确文件/接口、先失败测试、实现、验证命令和完成条件；用户选择执行方式 |

本机先用协议模拟模型/供应方验证真实PG/Kafka/Redis/MCP及恢复；真实RAG、模型和在线供应方单独验收。性能扫描选做，幂等、超时、接管、共享额度与事件补读必须有真实故障证据。具体场景见[本机验证](local-validation-scenario.md)，任务状态见[tasks](tasks.md)。

## Risks / Trade-offs

- Kafka引入重复投递及多段受理恢复成本；持久幂等和多阶段校验仍不可省略。
- 多实例可能继续旧物理计算；epoch与预算约束有效推进，不承诺立即停止远端或端到端exactly-once。
- 旧业务规则/框架SessionValues若越过正式提交边界会造成双重事实；按版本、回执和领域服务核对。
- Redis切换及跨服务授权/预算若只依赖临时状态会漏控；硬约束必须有持久依据。
- 同机多容器只验证协议，不证明跨主机高可用；模拟响应不证明真实检索/模型质量。

## Migration Plan

技术复核 → writing-plans及执行方式选择 → 单次apply/TDD → 任务证据/真实测试/代码审查/规格核对 → archive及分支收尾。当前CLI无独立verify工作流时明确报告并人工核对，strict validate仅检查材料，不能替代产品验收。活跃add-general-web-search的未完成事项仍保留，不因本次整理覆盖。

### Docker部署实施决定

A02实现细化：管理员通过独立`migration.Provision`入口创建带前缀的专用角色与schema；三个schema owner均NOLOGIN，各自迁移账号仅可SET ROLE到本owner，运行账号不继承owner。迁移账号可只读前序组件的版本元数据，用于验证control→platform→travel顺序，不获得对方业务表权限。`migration.Apply`拒绝管理员或运行账号冒充迁移账号，校验已应用SQL摘要并串行提交。按2026-11-07用户最新决定取消legacy导入、历史读取账号和兼容状态。

用户于2026-11-06要求代理准备数据库/Redis等依赖，并明确使用Docker部署。原先不自动安装服务的边界已由这次明确授权覆盖；仍保护现有业务库和原配置。依赖使用隔离Compose，PG/Redis/Kafka/Milvus及etcd/MinIO固定镜像；后续A13/A14将API/Worker/Travel/Maintenance/前端接入应用Compose。迁移保持独立串行入口，不让API容器启动时执行DDL。2026-11-07补全DockerDesktop4.91安装，WSL3.0.3后端已运行，六个基础容器健康且基础协议探针通过；业务角色/迁移、授权预算及真实模型仍按后续任务验收。操作说明见[Docker运行说明](../../../deploy/local/README.runtime-v1.md)。
