# 执行协议、调度与可靠性

版本：2026-11-06架构基线v1。返回[总览](README.md)。正式依据为原[MQ＋MCP工程协议](../../openspec/changes/refactor-agent-kernel-plan-execute/mq-mcp-dispatch-protocol.md)和[执行/恢复规格](../../openspec/changes/refactor-agent-kernel-plan-execute/specs/)。本文整合已讨论规则，不代表产品代码已实现；选型与参数状态以总览为准。

## 1. 必须区分的对象

| 对象 | 所属服务 | 含义 |
| --- | --- | --- |
| Run | 平台 | 一次用户问题，固定总期限、活动配额和累计预算 |
| 入口Job | 平台 | 本轮入口Agent的一段可认领执行，持有自己的现场和租约 |
| Delegation | 平台 | 一次专业委派，固定参数、request_key、父调用/断点及远端Task映射 |
| Dispatch操作 | 平台 | 可靠完成MCP提交的短操作，MQ确认后仍可从PG恢复 |
| TravelTask | Travel | 专业任务的执行身份、租约/现场及最终结果，不是第二个用户Run |
| Thread | 各所属服务 | 跨Run持续的模型上下文；每份TripPlan对应独立TravelThread |
| 工具逻辑调用/物理尝试 | 实际执行服务 | 一个稳定调用可发生有限次数网络尝试，分别计账 |

## 2. 完整交接链路

用户提交先在平台接纳边界处理：同授权范围内同幂等键同参数返回原Run，不重复写用户消息；同key不同参数冲突。同会话已有非终态Run时，不同新请求返回run_conflict，waiting与故障恢复期间同样互斥。新请求不追加到旧Run，也不自动取消旧Run。

```mermaid
sequenceDiagram
    participant E as 平台入口Job
    participant P as 平台PG
    participant O as Outbox Publisher
    participant K as Kafka
    participant D as Dispatch Worker
    participant T as Travel MCP服务
    participant C as 结果协调
    E->>P: 原调用 / checkpoint / Delegation / Outbox / waiting联合提交
    Note over E: 清执行租约，退出并释放入口槽
    O->>P: 短事务认领到期Outbox
    O->>K: 事务外发送命令
    K-->>O: broker确认
    O->>P: owner/epoch围栏标已发
    K->>D: 分发子Agent调用
    D->>P: 校验并持久认领Dispatch操作
    D->>K: 提交连续安全offset
    D->>T: MCP以稳定request_key提交
    T-->>D: 本服务事务受理后返回task_id
    D->>P: 保存远端映射
    Note over T: 本地任务执行池运行、续租、保存业务及结果
    C->>T: MCP查询状态 / 取得原结果
    C->>P: 保存真实结果 / 满足依赖 / 父ready联合提交
    E->>P: 新epoch认领，从原调用恢复
```

平台只经MCP操作Travel专业任务；Travel不直接写平台父Job。MQ只负责调用分发，不同时向Travel另发一份直接执行命令。父Job恢复读取平台已经可靠收集的结果，不持MCP长连接等待整个规划完成。

## 3. MQ接纳与Outbox

业务事务写入唯一Outbox后，Publisher后台轮询。首版不做Redis Pub/Sub、MCP广播或额外MQ Topic来主动唤醒Outbox。按索引读取到期记录，有界批量；积压时在容量内连续取批，无工作再等下一周期，实例可错峰。

认领不能只有SELECT，也不能持数据库行锁发网络请求：短事务SKIP LOCKED选择候选，写publisher_owner/lease_expires_at并递增publisher_epoch，提交后再发送，broker确认后按原owner/epoch及有效租约标已发。超过发布租约必须续持或放弃标记，不让旧实例覆盖新认领者。

发送成功但标记前崩溃允许重发原message_id，消费端必须幂等。业务发布资格已失效的执行消息不再发布；必要终态/审计交付应与新执行命令区分。

Kafka消费者有本地容量后，短事务核验当前Run/委派/dispatch_seq/权限/期限并持久接手，再确认连续安全offset。不等Travel完整执行结束才确认。DB不可用不确认；永久协议错误可靠记录并协调失败；旧消息/已接手消息核对后确认跳过。

确认offset后退出的恢复责任在PG持久Dispatch操作，不是Kafka未确认消息。后台需扫描未提交或提交结果未知的操作，按原身份继续MCP查询/提交。容量不足时暂停相关抓取，保持必要poll/心跳，不把长工具调用塞进Kafka接纳循环。

Kafka的Topic按分区保存追加日志，每个分区有自己的offset；同一delegation_id作key只能给出分区内顺序，不能替代dispatch_seq、幂等或业务版本判断。同group内分区分配给消费者，2分区便于2个Dispatch实例分摊，增加到更多实例不会让所有实例同时拥有这2个分区；远端已受理任务的运行并发另由Travel池控制。

建议显式控制offset确认，不能让自动确认越过尚未持久接手的消息。Producer采用broker确认及客户端幂等发送方向，精确acks/重试/客户端配置在锁定版本时核对；客户端幂等也不能消除“发出后PG标记前崩溃”造成的应用重发。单机RF/minISR=1/1只有本机集成意义，不宣称跨机器容灾或端到端exactly-once。

## 4. MCP协议与业务任务接口

MCP是协议，SDK Client嵌在平台Worker/协调进程，Server嵌在独立Travel服务。采用远端Streamable HTTP，SDK负责初始化/能力协商和JSON-RPC；服务由部署独立启动，客户端不再使用stdio启动子进程。

平台按服务地址与可信服务身份复用Client/session/HTTP连接池，不为每次任务创建新进程或把所有用户的身份混为同一业务授权。模型选择允许的能力及业务参数，程序执行真实网络调用并注入可信scope/期限；服务凭据不出现在模型参数。

业务上必须有提交、状态查询、结果取回，以及按稳定业务key找回受理结果的能力。它们由我们定义工具Schema，不是MCP自动提供的持久任务保证；首版不默认依赖实验性MCP Tasks。

## 5. 同key受理与并发

平台在第一次生成Delegation时产生并保存request_key，重投/超时/重启不换key、不重新生成业务参数。MCP request ID和trace ID只关联一次传输，不作为业务幂等键；新的用户业务委派才用新key。

Travel按可信调用方及授权主体限定幂等作用域，同key同hash返回原Task，同key不同hash报冲突。caller身份不随API Key轮换改变。input_hash使用版本化确定性编码覆盖固定业务参数，不含认证头、传输ID、尝试号等变化项。

服务用数据库唯一约束裁决并发，例如逻辑上UNIQUE(caller_id,subject_id,request_key)。插入成功者创建Task；冲突者读取原Task并比hash，不能用ON CONFLICT UPDATE覆盖参数或把运行状态重置。事务提交后才返回受理；后台执行池只认领持久任务，不每次RPC都起一份执行。

提交超时不代表远端没受理。已有task_id就查询原Task；没有则按key查询或同key重提。暂未查到也不能证明原请求不会稍后到达。远端queued/running时继续等待，不创建另一份规划。用户已确认：最终明确failed则收集真实失败及可用部分结果交回父Agent，不自动新建专业Task；同key仍返回原失败任务，不靠换key绕过次数限制。父已过期/终态则不能恢复。

数据库实现可用唯一约束＋INSERT ... ON CONFLICT DO NOTHING RETURNING，冲突后在READ COMMITTED下用后续语句读取原已提交行并比较hash；不是先查后无约束插入，也不使用冲突UPDATE覆盖原Task。精确列、事务隔离与清理保留协议在实施前锁定。

幂等映射和结果保留覆盖协议恢复窗口；清理后迟到请求必须有明确过期处理，不能当成从未提交过。同key只保证受理不会重复创建任务，物理执行/外部调用仍需租约、围栏、调用账和副作用幂等。

## 6. 时间窗口与租约

| 时间/身份 | 负责什么 | 是否可续长 |
| --- | --- | --- |
| global_deadline_at | 默认接纳时刻＋300秒，包含排队/等待/重试/父结果表达 | 不因挂起、恢复、续租而延长 |
| 子任务deadline | 当前专业任务的上限，不晚于祖先窗口 | 只能在全局限制内约束，不因重试重置 |
| 工具逻辑deadline | 包含全部实际尝试与退避 | 每次请求取剩余时间，不能重新给完整窗口 |
| lease_expires_at | 当前执行者的有效期 | 仅原有效owner/epoch可续，不超过任务deadline |
| not_before | 最早允许下一次调度的绝对时刻 | 同一次调度保存后不因扫描重新推后 |

时间建议用PG timestamptz，由数据库时间计算/比较。参数示例可用每5秒续租、租约20秒；实际值是配置，不把“心跳存活”当业务有进展。

认领记录owner和新lease_epoch。续租仅在running、owner/epoch一致、旧租约未过期且deadline允许时成功，保持原epoch。失去执行权或确认续租失败则停止新的有效推进；网络结果未知时只能依据最后确认租约保守处理，不能假定无限持有。

父Job保存waiting/现场/依赖/Outbox后清执行租约，退出释放槽位，不再续租；Run仍非终态并继续计时。恢复为ready后重新认领新epoch。同步等待RAG/API工具的Travel仍running、占专业槽并续租。协调器轮询只读远端状态，不替父Job或Travel续租。

后台按running租约到期查恢复候选，waiting按依赖查结果；不把waiting无租约当宕机。旧Worker恢复后不能续回过期代次，即使暂时没人接管，也须重新经过认领判断。

例如12:00:00接纳Run，总截止为12:05:00；12:00:10父Job等待并释放槽，12:02:00子结果回到平台，父重新认领后只剩180秒，而不是再获得300秒。若子结果直到12:05:01才到，只能按规则留审计，不能唤醒已超时父Run。此例不表示子Task可以一直用完父全部窗口，实际需为收集/父表达预留余量。

## 7. 三次退避及工具调用

初次执行加最多三次重试，总尝试最多四次。同一实际网络调用只有一个重试责任方，SDK/HTTP/任务恢复/MQ重投不能层层相乘。重启及换实例不清次数；记录实际尝试和未知usage，预算不凭没有响应自动退款。

```text
第一次重试：U(0,1秒)
第二次重试：U(0,2秒)
第三次重试：U(0,4秒)
```

这是full jitter，窗口是上限而非固定等待。持久调度将本次随机结果转成not_before保存一次；不每次扫描重新随机。合法Retry-After作为最小等待要求，但等待后不能落入剩余期限则提前停止。

子Agent提交的持久延迟已选定PG not_before＋Outbox轮询，到期再发Kafka。失败事务直接写带not_before的唯一Outbox；若采用单列retry操作表，扫描转换为Outbox需在同一事务CAS完成，不让两个循环各自产生新尝试。保持原request_key，不把提交重试变成创建新专业任务。

工具短重试已确定使用本地可取消timer，不走MQ、不需要Redis，继续持任务执行槽/租约，不持PG事务。恢复所需调用身份/次数/结果仍持久保存；若必须保留跨重启的原等待时刻，则保存下一次时间，而不是要求Redis接管所有工具调用。

参数/授权错误、正常空结果、能力缺失和已终态不重试；外部写结果未知先核对，不自动重放。有效流式delta已展示后断流，不把第二份调用输出透明拼接成原成功结果。现有providerhttp代码仍最多三次总尝试，改为四次留后续实施。

## 8. 巡检、熔断与状态保护

巡检覆盖全局到期Run、失权running任务、未发/发布租约过期Outbox、已接手但MCP未知提交的Dispatch、已受理远端Task、待收集父依赖及到期重试记录。全局到期可靠终结，不能重试续命；局部attempt超时/租约丢失则先核对剩余预算、次数及安全恢复点。

version用于状态CAS，必须核对旧值并检查更新行数；epoch用于拒绝旧执行者；request_key用于受理幂等；dispatch_seq用于分发资格；Task版本/generation用于结果适用性。它们不能互相替代。

Run完成与超时竞争通过同一Run行锁或CAS裁定一次合法终态，状态、最终消息和事件在本地事务一致提交。跨服务无法用SQL事务包住网络；前置检查后状态仍可能变化，发布/接纳/实际调用/业务提交/回填都要校验。不承诺立即停止已发出的远端物理计算。

熔断先沿用进程内provider/account/endpoint维度，候选60秒窗口5次暂时故障打开60秒、半开单探测、连续2成功关闭；多实例半开仍受共享资源许可。阈值是工程起点，不是实测最优。熔断等待不延长deadline，认证错/参数错/无命中不当作供应方暂时故障。
