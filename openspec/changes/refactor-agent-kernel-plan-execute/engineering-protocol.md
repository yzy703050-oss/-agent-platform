# 公共基础设施协议

更新：2026-11-06。此附录只保留当前Redis、身份/预算及SSE技术规则；执行事务、300秒期限与三次重试统一见[MQ＋MCP协议](mq-mcp-dispatch-protocol.md)，正式行为见[specs](specs/)。配置数值是可验证的工程起点，不表示已部署。

## 1. Redis 主从、缓存和共享额度

### 1.1 拓扑与访问

生产参考拓扑选择 1 主 2 从、3 Sentinel、quorum=2，部署在 3 个独立故障域；这是代理选定参考配置，用户未提供机器资源，不代表已部署。单机多个容器只验证协议，不提供主机故障容灾。业务只通过 Sentinel master name 和 Sentinel 地址发现主节点，首版所有业务 Redis 读写走主，暂不引入读从延迟。

本机使用两个显式运行模式：single用于日常功能联调，min-replicas-to-write=0，不对不存在的副本设写入门槛；sentinel用于主节点故障专项，数据节点1主2从、Sentinel 3个，quorum=2，min-replicas-to-write=1。Sentinel模式候选down-after-milliseconds=5000、failover-timeout=30000、parallel-syncs=1；这不是5秒完成切换的SLA。两从配合至少1个可用副本的写保护，避免只剩一个新主后持续因无副本拒写；切换期间仍允许保守阻塞。每数据节点起步maxmemory=128MiB、noeviction，属于数据内存目标而非进程RSS/持久化内存上限；容量不足应明确报错。Sentinel重写配置及数据卷需可持久化，发现返回的主节点地址必须从实际Go进程所在网络可达，不能只验证容器内部互通。

主从启用 AOF everysec，RDB 用于备份候选，Sentinel 配置可写且持久化；min-replicas-to-write=1、min-replicas-max-lag=5 为保护候选，失去复制条件宁可拒绝写。启用 ACL/TLS 或可信隔离网络，Sentinel 与 Redis 使用独立凭据。maxmemory-policy=noeviction，应用缓存有 TTL 及大小上限，避免共享许可键被逐出；容量不足按 Redis 不可用处理。

Sentinel/WAIT 不提供强一致保证。切换可能丢写、旧主可能短时可达；所有正式执行权、预算与授权均不得只由 Redis 计数/键存在决定。[Sentinel](https://redis.io/docs/latest/operate/oss_and_stack/management/sentinel/)、[WAIT](https://redis.io/docs/latest/commands/wait/)

### 1.2 键、缓存和通知

| 键/通道，均有环境及 schema 前缀 | 规则 |
| --- | --- |
| cache:thread:{user}:{thread}:{context_epoch}:{summary_version}:{history_upper} | 只缓存所需有效上下文，TTL 候选 300s，权限及当前版本由 PG 确认 |
| cache:checkpoint:{job}:{checkpoint_version}:{hash} | 不可变精确版本，TTL 候选 300s；PG 指针/兼容性有效才可用 |
| cache:memory:{user}:{memory_revision} | 用户最新正式快照的投影，TTL 候选 300s；先确认当前 revision 和条目来源，不回退旧值绕撤回 |
| notify:run:{run_id} | PG 提交后发布 run_id/latest_seq 提示；API 按自身已授权订阅回读 PG |
| permit:{resource_key} | Lua 原子共享预准入，成员为 outbound_call_id、到期时间；不能仅用整数 DECR 释放别人的许可 |

缓存回源后写精确版本键，不只依赖删除旧键；不缓存认证正结果或唯一正式指针。Redis 连接/命令失败限时回源 PG，不忙循环。通知允许遗漏，SSE 定时补读；不用 Redis Streams 再建一套调度。

### 1.3 额度切换闭环

1. 先取得 Redis 对资源账户/端点的共享许可，等待候选最多 2 秒且不超过剩余期限；没有许可不发网络调用。Lua 按稳定 call ID 幂等获取、续持及释放，未知执行脚本结果以同 ID 核对，不换 ID 重扣。
2. 短PG准入事务锁resource_limits，按 outbound_calls 中仍有效许可核对硬并发，同时预占 Run/后台预算，登记一次真实网络尝试。此登记本来就用于崩溃漏计保护，顺便守住 Sentinel 丢许可后的硬上限，不新增独立许可流水。
3. PG 准入失败释放本次 Redis 许可；PG 成功后立即在有效执行权和期限内发送。崩溃在登记和发送之间也按已预占保守计账，不能无证据退款。
4. 仅完成/确认未发送时幂等结算。结果未知保留预算上界，调用占用至少保留至本地请求硬截止与安全清理边界；不得因 Job 失权或 Redis 丢键立即放出同一许可。平台能控制新请求准入，不能保证供应方在本地断线后已停止物理计算。
5. Redis 不可用、角色不确定、切换重连期间停止新外部调用准入；已经发起的调用可在原期限内结束并保存结果，waiting 仍可接纳已有结果。连接新主并健康确认后，以 PG 占用重建许可集合再恢复；分布式重建互斥/版本使用 PG resource_limits 行，不用 Redis 自己的锁证明其正确性。
6. 旧主若仍接收许可，PG 硬上限仍限制实际新调用。Redis 故障不退回每 Worker 各满额，也不禁止已保存结果交接、状态查询和最终落库。共享速率平滑属于 Redis 能力，切换后不承诺精确不丢的供应方 RPS；供应方 429 继续按 Retry-After 处理。

上述是可靠性与写量的取舍。PG 登记每次实际外部尝试，心跳不按 token 写入，普通缓存命中不新增尝试。未来只有实测表明调用登记成为瓶颈才讨论放宽硬保证，不能在实现中暗改为 Redis 唯一权威。

## 2. 登录凭证和服务授权

- 沿用 32 随机字节会话凭证、独立 CSRF 值、PG token_hash/csrf_hash、HttpOnly Cookie；不改 JWT，不把原始 session 存 Redis。Cookie host-only、Path=/、SameSite=Lax，生产 Secure，开发 localhost 例外沿用现有限制。
- Session 绝对期限候选沿用现代码 7 天并集中配置；首版不增加滑动刷新。认证/CSRF 每次查 PG 有效且未撤销 Session，允许不同 API 实例处理请求；数据库不可用返回 auth_unavailable，不当作已登录。
- JSON 写接口使用 X-CSRF-Token，检查配置的精确 Origin；有 Cookie 的浏览器写请求缺 Origin 时以同源 Referer 复核，均无可验证来源时拒绝。登录/注册无 Session CSRF，仍受来源、JSON Content-Type 和入口限流保护。Cookie/CORS 配置使用同源反向代理部署，不开放任意带凭据跨域。
- 沿用前端现有 CSRF 保存/请求方式，CSRF 不是身份凭证；刷新后仍须 /me 验证当前 Session。退出在 PG 撤销后清 Cookie，旧 API 无正认证缓存可绕撤销；删除用户凭证不清除业务历史。
- Run 固定 origin_session_id；认领、发起新外部调用和有效提交复核该 Session 及当前用户/会话授权。撤销/过期后不再推进，维护扫到则以 auth_revoked/auth_expired 可靠失败，保留已提交效果和部分结果；不是用户取消。SSE 下个鉴权周期关闭。页面 page_leases 只作接入/存在性，不再参与执行活跃条件。
- Maintenance 周期记忆使用自己的 operation、lease 和服务身份，以用户/Thread/来源有效性及后台配置授权；不借前台已结束 Run 或浏览器 Session 续租。用户十轮累计仍按成功有效用户轮次，不按后台调用次数。

Kafka使用TLS/SASL及最小平台Topic/Group ACL，RAG不连接Kafka。MCP使用HTTPS与独立服务API Key，RAG自有库保存Key摘要及工具/逻辑知识库grant；凭据由部署配置注入，不使用用户Cookie，不进入工具模型参数、提示词、结果或日志。支持新Key发布/调用方切换/旧Key撤销。取消此前为RAG消息新增的方向独立HMAC envelope与签名密钥，无需自建第二套服务认证。平台在调用前和结果采用时仍复核用户权限。安全依据[OWASP Session](https://cheatsheetseries.owasp.org/cheatsheets/Session_Management_Cheat_Sheet.html)与[CSRF](https://cheatsheetseries.owasp.org/cheatsheets/Cross-Site_Request_Forgery_Prevention_Cheat_Sheet.html)。

### 2.1 公共授权与调用账的受控边界

2026-11-06技术复核选择：在同PG实例新增`control`基础设施schema，使用白名单数据库函数访问；平台/Travel运行角色不直接SELECT/UPDATE该schema表，也不取得对方业务schema权限。此选择利用已确认的共实例部署，避免一次HTTP授权检查与随后本地业务提交之间无法裁决撤回竞争；不引入第二套HTTP授权服务或HMAC身份。跨服务业务仍只经MCP/受控读取接口，不联合修改platform和travel业务表。

| 私有记录 | 最小字段与约束 |
| --- | --- |
| session_guards | origin_session_id PK、subject_id、expires_at、revoked_at、version；是执行授权的撤回围栏，不存Cookie/hash/CSRF。平台创建/撤销Session与本行在同一本地事务提交，运行账号不能绕过撤销入口单写Session |
| run_guards | source_run_id PK、subject_id、conversation_id、origin_session_id、固定deadline_at、active、version、固定budget_limits；接纳/终态与平台Run同事务。waiting不关闭Run授权，不延长deadline |
| context_guards | (subject_id,conversation_id) PK、context_epoch、可用状态；清除/删除与平台对应状态同事务，来源排除仍按原授权读取规则过滤 |
| execution_scopes | scope_id PK、issuer_service、owner_service、subject/run/conversation、delegation_id可空、能力/目标/来源范围、context_epoch、expires_at、revoked_at、scope_hash；issuer从实际发放者身份固化，MCP认证caller须匹配；后台memory scope独立service授权，不借已结束Run |
| resource_limits | resource_key PK、max_concurrency、version、Redis重建generation；多个资源统一按key顺序认领 |
| outbound_calls | attempt_id PK、scope_id、logical_call_key、attempt_no、owner_service、owner_job/operation、lease_epoch、resource_keys、reservation、usage、status、hold_until、response_ref；UNIQUE(scope_id,logical_call_key,attempt_no)，attempt_no为1..4，结果不可重复结算 |

各表FK仅在control内部；platform/Travel保存scope_id/attempt_id为受控外部关联，不跨业务schema FK。control不复制正式计划、用户消息、查询正文或凭据；Run/Session guard仅作同步授权围栏，不由巡检异步复制后再自称即时撤销。平台业务更新与guard变更必须同事务且影响行数正确，否则整体回滚。数据库运行身份绑定固定service_id，不能由模型或参数自报；函数内部基于实际连接登录角色（非SECURITY DEFINER后的current_user）核验调用者。

白名单接口约定：`issue_scope(scope_input)->scope_id/hash`仅平台和获准Maintenance角色可调用；`revoke_session_guard(session_id)`、`close_run_guard(run_id)`仅平台可调用；`assert_scope(scope_id,scope_hash,capability,target,purpose)->authorized_scope`向所属执行服务开放；`reserve_attempt(scope_id,logical_key,attempt_no,owner_epoch,reservation,resource_keys)->attempt_id`及`settle_attempt(attempt_id,usage,status,response_ref)`只操作本服务已授权范围；`read_related_memory(scope_id,domain)`只返回允许投影，不开放平台记忆表DML。完整SQL及Go消费接口在详细计划展开，不能自行扩大函数权限。

业务提交事务先通过`assert_scope(...,purpose='commit')`对相关guard/scope持SHARE锁至本事务结束，再锁本地Task/Plan等对象；撤回/终态/清除以相同固定顺序获取对应guard排他锁。若提交先取得合法guard锁，则其业务先提交、随后撤回生效；若撤回先提交，后续业务拒绝。不得只读一次guard、不持至提交便声称关闭竞争。新外部调用另在短准入事务核验授权、当前本地执行权及剩余期限；事务外发送的物理计算不能保证被随后撤回立即停止。

统一顺序：session guard→run guard→context guard→scope→排序后的resource_limits→本服务用户/会话/Run或Task/Thread/Plan→调用/操作行。后台memory不持前台Session/Run guard，改核对其独立service scope及来源/epoch。每条事务遵守适用子序列，不在已锁Plan后倒查授权锁；撤回不能先锁platform Session/Run再反向获取guard。运行账号仅获得所需EXECUTE；函数owner为无登录专用角色，固定search_path和限定表名、撤销PUBLIC EXECUTE，禁止调用方创建同名对象或提供任意SQL/字段路径。

预算准入从事务开始就取得Run预算guard（后台为operation scope）的UPDATE锁，再核对账内累计预占；不先SHARE后升级，避免两个准入事务互相锁升级。普通业务提交仅需相应授权SHARE锁；结算对原attempt及所属预算范围幂等核对，可在授权已关闭后记录真实usage，但不得发新调用或授予有效业务提交资格。

预算采用control.outbound_calls唯一调用账：Run和后台operation的模型尝试数、工具尝试数及token/费用上界由部署配置显式提供，缺少所启用能力的有限上界时拒绝启动新执行，不暗设无限预算或未经确认的付费额度。子Task使用原Run scope总账，不复制一份可独立花满的额度；参数在Run接纳时固定，后续配置改变不重置。每次真实尝试先预占安全上界，已知usage按原attempt幂等结算，缺失/未知继续保留原预占，不凭超时退款。scope/guard失效仍允许有证据的原尝试结算与历史审计，不允许新业务推进；结算不把迟到结果变成当前业务成功。

任务状态轮询/按key找回属于control_rpc，不冒充新模型或业务工具尝试，也不清零其计数；它们有独立有界轮询频率/并发、每次传输重试与原绝对期限。scope_hash仅覆盖不可变授权定义；撤回标记/当前guard版本独立核验，不通过改hash或换scope为原业务重发配额。

资源账只保护有效调用准入，不承诺外部供应方断连后停止物理计算。未知调用按固定请求硬截止及保守清理边界保持占用；清理规则和数值须随真实供应方时限在实施配置锁定，不能因Redis键消失、Job换epoch或进程重启立即释放。Redis预准入/重建仍按1.3，不另建正式余额。必须验证提交/撤回两种锁顺序、非法service/其他scope、Redis丢许可、重复结算、未知usage与多资源锁序。

## 3. SSE 与页面重连

保留 GET /runs/:id/events、Last-Event-ID 和 after 兼容入口；首次授权 Run 归属后先订阅本地/跨 API 通知，再补读 PG，期间缓冲提示。每次循环只按 seq 连续读取持久事件，不因收到某个较大序号通知直接推进游标。正 seq 由 PG 在 Run 锁下分配，与对应状态/结果共同提交；回放按序分页（沿用最多 1000）。

事件envelope扩展schema_version、run_id、seq、type、job_id可选、stream_id/output_version可选、payload、created_at。对外事件名适配期映射，不声称前端已支持新字段。持久状态/工具进度/输出快照/最终回答/错误进run_events，最终结果仍以messages/Run为权威。SSE单帧候选上限256KiB；过大输出快照使用已授权Run/消息查询的output_ref及版本，不截断正文或塞入完整工具证据。内部Planner提案/原始日志不直接发用户。

临时文本delta无SSE id、不推进持久游标，带stream_id、lease_epoch、delta_seq。单stream聚合推送候选100ms；PG普通部分快照最多每1秒一次，4KiB作为内存聚合阈值、仍受1秒写频限制，无变化不写，最终/错误立即提交。快照带已覆盖delta_seq，客户端替换覆盖内容而不重复追加；发现缺口等待快照或补读，重连以持久快照重建，不承诺逐Token重放。接管用新stream_id，旧代次delta丢弃；最终快照覆盖草稿，不拼接两次模型输出。

Redis notify 只含已提交序号提示；临时 delta 可经独立 Pub/Sub 通道传输但不承担可靠恢复。API 保留现有每 2 秒周期鉴权与补读并合并同 Run 多订阅查询；无新事件不写 PG。Session 失效关闭流，前端重新验证登录；网络断流按有上限退避重连并补读，断线本身不改变 Run。

慢客户端沿用 5 秒写 deadline、本地队列候选 64，队列满关闭此连接，不能反向阻塞 Worker。现有每 API 总 128/每 Run 8 订阅限制保留为本地保护，不冒充集群总限额。连接候选最长 15 分钟/Session 剩余期限，正常重连不启动执行；客户端收到终态停止重连。游标大于当前持久最大值返回 cursor_ahead，客户端重查权威快照；日后事件保留策略产生缺口时必须显式 reset，不静默跳过。

Nginx SSE location 关闭 proxy_buffering/cache/gzip，保留 text/event-stream、Cache-Control:no-cache,no-transform、X-Accel-Buffering:no；候选注释心跳 15 秒、proxy_read_timeout 60 秒。平台整体 HTTP WriteTimeout 不得无意切断流，使用连接写期限保护。依据 [WHATWG SSE](https://html.spec.whatwg.org/multipage/server-sent-events.html)、[Nginx buffering](https://nginx.org/en/docs/http/ngx_http_proxy_module.html#proxy_buffering)。

