# 当前技术复核

日期：2026-11-06。范围：原Agent/RAG两change的proposal、全部specs、design、tasks、协议/领域契约/数据库附录，对照小豆包、`G:/简历项目/MODULAR-RAG-MCP-SERVER`实际代码、固定SDK源码及相关测试。本文件只维护当前结论，不创建讨论快照。正式行为仍以specs为准。

## 1. 结论与进入条件

架构可以继续推进，无需重选双入口、Kafka/MCP、同步RAG或300秒期限。Eino嵌套中断/定向恢复、Go/Python HTTP MCP互通已有隔离实验证据；旧代码不能直接当成目标实现复用。

**本轮源码与设计复核完成，关键接法已写回原设计，可提交设计审阅并展开详细实施计划；产品编码仍需计划审阅、执行方式选择及相应环境准备。** 公共授权/预算、运行Schema和数据归属见第5节；环境未就绪影响真实集成，不能用模拟器或validate勾成已通过。未进入apply、产品verify或archive。

本次用户补充确认并已同步specs：Travel最多8个“执行＋重新规划”轮次，连续2轮无实质进展结束并返回已有结果和未完成原因；恢复不清零，仍受累计预算及全局deadline约束。计数口径见design 4。

## 2. 关键发现与处置

| 编号/级别 | 实际依据与问题 | 处置和必须补足的验证 |
| --- | --- | --- |
| A1 / 阻断 | einoruntime/runner.go未请求暂停时使用WithSkipCheckpoint；RepositoryStore以run_id寻址，SaveCheckpoint单独提交，不能原样承接异步委派 | design 4.1锁定轻量桥Agent主动中断、捕获最外层完整bytes，再联合提交waiting/委派/Outbox；需PG提交前后崩溃和失败不发布测试 |
| A2 / 阻断 | AgentTool共享父SessionValues；嵌套现场不等于独立TravelThread，默认输出取最后消息文本 | 状态按job/call/plan命名；独立Travel自有Runner/线程；定向恢复原target并确定性序列化真实结果，不把accepted当结果 |
| A3 / 已修正文档 | 数据库附录残留Delegation跨服务FK、Travel消息统一取平台conversation序号、直接扩展旧业务表为新Travel表的描述 | database字典/迁移已改为各自schema与本地FK、远端引用、Travel本地序号；补充持久Dispatch操作、索引/锁序。未生成或执行DDL |
| A4 / 设计已收敛 | 共PG隔离已定，原设计却缺公共授权撤回及预算接口；Travel不能直接读Session/Run | 公共协议2.1选定control私有guard/scope/调用账＋白名单函数，定义撤回/提交锁序及预算准入/结算；API Key不替代用户授权。实际SQL/权限竞争待实施 |
| A5 / 契约已补 | 原RuntimeScope不覆盖异步任务envelope/dispatch_seq | 原Schema新增7个运行定义、样例37～43和10反例；协议4.1定义四工具、可信_meta、JCS输入hash、结果/幂等保留和过期拒绝。结构校验不是运行验收 |
| A6 / 高 | config默认Run120秒、Travel600秒；Manager从开始执行WithTimeout；providerhttp总尝试3次；bootstrap独占锁＋自动迁移仍在 | 接纳时保存固定300秒deadline；子期限取min且为父表达留余量；唯一重试层4次总尝试。先事务/围栏，再独立迁移/拆锁 |
| A7 / 高 | 前端subscribe不带重连游标，保留暂停/继续/结束和旧run_conflict文案；通知在进程内 | 增加持久游标/快照及stream/epoch过滤，保留2秒鉴权/PG补读；移除旧控制，跨API/丢通知/慢客户端测试 |
| R1 / 已复现 | RAG _perform_search捕获异常返回[]，PolicyEvidenceTool继而返回no_knowledge/isError=false | 双路异常注入已复现。两路成功空才是no_knowledge；一路失败另一路也空应明确错误，不能冒充正常无命中 |
| R2 / 高 | _current_collection＋RLock串行全查询；重排提前按final_k截断；CrossEncoder.timeout只保存字段，predict无实际期限控制 | 固定每请求build/profile/visibility；最多40候选保留到时间融合后取Top-K；原生worker实际结束才释放资源槽 |
| R3 / 高 | Dashboard忽略PipelineResult.success仍报成功；格式选项有md/docx；临时路径与文件hash未形成稳定处理身份 | 持久归档原文，分开document/version/chunk_set/build；修正CLI/管理/列表/摘要/删除等全部读取者，不仅增加Milvus配置 |
| R4 / 高 | Loader/分块/评测chunk_id不等于canonical原文范围；1000字符不能保证BGE512 token不截断 | 固定规范化原文及位置，检索增强文字不改证据content；按真实tokenizer切分；gold以文档版本＋原文范围标注 |
| R5 / 接法已验证 | Python SDK支持现有low-level Server的stateless JSON包装，但json_response=True不会自动GET405；Go SDK有默认独立SSE/重连 | lifespan管理唯一SessionManager，路由显式拒绝GET；Go DisableStandaloneSSE=true、MaxRetries=-1、HTTP池复用；不升级SDK2或混用协议 |

“阻断/高”指目标设计或实现必须处理，不表示本轮已修改这些产品代码。

## 3. 搜索变更与复用边界

活跃add-general-web-search的4.2真实公网/模型验收及4.3规格核对/归档仍未完成，本change不替它勾选。其原“不增加差旅工具/委派”是当次范围，新Agent specs已明确两入口共享查询，不能解释为永久禁令。

保留已发布query/time_range、输入/结果限额、完整JSON、安全URL、真实来源及指纹；新入口/Executor通过同一客户端适配。搜索继续保持原能力的零自动供应方重试；“最多三次重试”是上限，不强制每个能力都重试三次。旧取消信号测试保留系统停止/失权传播，不据此保留用户取消按钮；旧来源排除仍按legacy规则处理。最终归档需核对两个活跃change的主规格替代关系，不能覆盖搜索未完成事项。

可复用身份、领域校验、候选/证据、回执、客户端及提示分层；需替换Main/Verifier/总结阶段、固定默认行程、hard/soft筛选和页面暂停/手动继续。旧测试须区分保留的保护和已被替代的行为，不能原样全绿就称新控制流完成。

## 4. 依赖选择与本机环境

以下为核验官方发布/tag/源码后的实施选型，**未安装、未改go.mod/pyproject，新增依赖未完成项目编译/服务验收**。实施计划使用精确版本，首次获取后记录镜像digest与完整依赖锁，不用latest。

| 依赖 | 本轮选择/已知事实 |
| --- | --- |
| Eino / Go MCP | 保持v0.9.0-beta.1 / v1.8.0，本地源码与隔离探针通过 |
| Python / MCP / ASGI | 当前venv为Python3.12.14、mcp1.30.0、uvicorn0.54.0、starlette1.7.0；协议基线2025-11-25；HTTP探针使用这些版本 |
| Kafka / Go client | 候选apache/kafka:4.4.0、franz-go v1.22.2（tag 7b6cac30a57a462216fd27e2408eadb7af80a224）；客户端最低Go1.26，与项目要求一致；未下载编译 |
| Redis / Go client | 候选redis:8.10.3、go-redis/v9 v9.23.0；MaxRetries=0实际用默认重试，需-1禁用隐藏重试，未知许可结果按同ID核对 |
| Milvus / PyMilvus | 维持2.6线，候选Milvus2.6.26 / PyMilvus2.6.17；该版官方Compose依赖etcd3.5.25、MinIO RELEASE.2024-12-18T13-15-44Z；需真实中文BM25/CRUD/发布验证，未安装 |
| Embedding | 目标bge-small-zh-v1.5 revision 7999e1d3359715c523056ef9478215996d62a620，HF缓存有该snapshot，max_seq_length512；当前配置指向的另一本地目录仍需核对清单/校验和 |
| Reranker | 目标bge-reranker-v2-m3 revision 953dc6f6f85a1b2dbfca4c34a2796e7dde08d41e；仅核验远端元数据，未下载/加载/中文验收 |

Docker Hub只读元数据已解析以下linux/amd64 manifest digest（没有拉镜像/运行）：Kafka4.4.0为`sha256:76af3d0116abbb33b4cd964af410d4921c33d1f663e7a95d5453a9c35a84869b`；Redis8.10.3为`sha256:e2728231060693472a8efb488dd139a8142a01bd0612ae70429bf5b5be924be1`；Milvus2.6.26为`sha256:e6066d0afc4eeab69b7c9269e49be2b9ae54c740a21c05f230c4e5743c646ee3`。这是选定架构的manifest，不冒充其他CPU架构或整套Compose依赖已安装。

Windows11、15.86GiB内存，本次约4.02GiB空闲；Go1.27.2、Node/npm可用，PG17服务运行。docker/redis-server/java命令未在PATH发现；WSL发行版/引擎未验收，pymilvus和reranker未就绪。PG服务运行不等于隔离库/角色/迁移可用。本轮未停止其他程序、安装服务/下载模型或读取/打印.env。

Go1.27.2触发sonic对1.26以上版本的fallback警告，实验回退encoding/json；正式支持范围仍按CI Go1.26.x验证。两仓库原有未提交修改保留，外部RAG未新增OpenSpec结构。

Kafka核对：关闭自动offset提交，以有界PollRecords完成PG持久接手和连续安全offset提交，及时AllowRebalance；MCP在独立Dispatch池执行。producer保留幂等和AllISRAcks；RecordRetries/DeliveryTimeout默认不能保证终止未知在途幂等发送，需核对AllowIdempotentProduceCancellation及回调围栏。Outbox租约过期不能释放仍在途发送的本地容量；未知发布沿原message_id重发。发送重投不消耗Travel工具四次业务尝试，但受发布窗口、Run资格及有界内存约束。

来源：[franz固定源码](https://github.com/twmb/franz-go/blob/v1.22.2/pkg/kgo/config.go)、[Redis客户端](https://github.com/redis/go-redis/blob/v9.23.0/options.go)、[Kafka tag](https://github.com/apache/kafka/tree/4.4.0)、[Redis发行版](https://github.com/redis/redis/releases/tag/8.10.3)、[Milvus Compose](https://github.com/milvus-io/milvus/releases/download/v2.6.26/milvus-standalone-docker-compose.yml)、[PyMilvus元数据](https://pypi.org/pypi/pymilvus/2.6.17/json)、[Python SDK1.30.0](https://github.com/modelcontextprotocol/python-sdk/blob/v1.30.0/src/mcp/server/streamable_http_manager.py)。模型revision来自Hugging Face官方模型API，元数据不代表模型可用性。

## 5. 技术收敛结果与实施边界

1. **公共授权/预算（A4）**：采用control协调schema，具体guard/scope/调用账、白名单接口、服务角色、锁序和撤回竞争已在engineering-protocol 2.1定义。Run/后台有限预算上界需显式配置并在接纳时固定，未知usage不退款；不在本轮替用户猜付费额度。实际函数/SQL、非法权限及竞争测试进入详细计划，禁止把其未实现写为通过。
2. **运行协议（A5）**：mq-mcp-dispatch-protocol 4.1及原Schema已定义submit/find/status/result、可信_meta、JCS hash、真实失败/部分结果、结果大小和原身份对应；首版不自动清理映射/结果，过期scope不能新建Task。未来GC须先有tombstone和过期拒绝协议。跨服务证据读取需scope校验、原版本与内容hash；接口具体文件/函数、通用JCS及跨进程负例在实施计划展开。
3. **物理数据库/迁移（A3/A6）**：字典第0节及迁移已修正schema、本地FK、专业Task/Dispatch、消息序号、关键唯一键、扫描索引、锁序与角色边界；control表归公共协调，不复制另一份正式调用账。实施计划须展开精确CHECK/grant/增量DDL及隔离库升级/回退。既有public表首版留作legacy读取，新platform/travel结构独立串行迁移，不在本变更默认SET SCHEMA搬旧表；旧计划只读，不把旧action/receipt当新Travel执行。
4. **依赖/环境**：第4节记录精确候选和已核验API；尚需新增依赖解析/项目编译、镜像digest/完整依赖锁、隔离PG和Linux容器或远端测试环境、模型本地目录。本机尚不能宣称可启动完整多实例拓扑，此项保持未完成，不影响如实展开环境准备和实施计划。

上述技术决定依据同PG/权限隔离、即时后续授权核验、唯一预算账和原运行规格，不改已确认产品行为。只有新发现影响产品行为、兼容范围或验收标准才需用户确认。可分别编写相互引用的Agent/RAG实施计划，需求仍只在原两change；不默认派子代理。

## 6. 本次实际验证

使用superpowers:verification-before-completion按真实输出记录。没有连接业务数据库、调用真实模型/供应方或启动旧产品服务。

| 检查 | 实际结果与限制 |
| --- | --- |
| Go针对性基线 | `go test -mod=readonly ./internal/agent/einoruntime ./internal/client/providerhttp ./internal/client/mcp ./internal/agent/memory ./internal/agent/agents/travel -count=1 -timeout 90s`，5包通过；GOPROXY=off。不是全量测试/构建/PG集成 |
| Eino隔离实验 | TestNestedCheckpointFreshRunnersAndRepeatedCalls、TestCheckpointSetFailureSurfacesError通过；最终完整嵌套payload约48,523字节，错误target保持等待，两次原调用配对、命名空间键跨恢复保留，Set拒绝可观测。仅新对象＋文件bytes，不是跨进程PG联合事务验收 |
| HTTP MCP隔离实验 | TestGoPythonStatelessJSONInterop通过：独立Python进程、回环HTTP、官方Go SDK初始化、四工具发现、可信_meta到请求上下文、两次完整证据JSON调用；检索器为fixture，401由临时认证包装、405由显式路由产生，不证明产品授权或真实检索已实现 |
| HTTP首次差异 | 探针最初假设SDK自动GET405，实际无Accept GET返回406；查固定源码定位后，显式路由GET405重跑通过；未改产品代码或SDK |
| RAG现有单元基线 | `.venv/Scripts/python.exe -m pytest tests/unit/test_policy_evidence.py tests/unit/test_reranker_fallback.py tests/unit/test_splitter_factory.py tests/unit/test_text_ingestion.py -q -p no:cacheprovider`，66 passed，HF/Transformers离线；不含Milvus/真实模型/消融 |
| RAG异常复现 | 注入HybridSearch.search抛ConnectionError，经真实_perform_search及PolicyEvidenceTool得到no_knowledge/isError=false。复现命令成功只表示问题被观察到，未修复 |

隔离探针在`%TEMP%/xiaodoubao-review-20261106`，不作为产品实现或新历史文档。该目录`go test -mod=readonly -v -count=1 -timeout 30s .`复现三项探针；HTTP项需先用RAG venv启动同目录http_probe.py。实施时转为有版本控制、含真实PG事务/围栏的回归测试，不能把临时文件作为最终交付验收。临时HTTP进程核验后关闭。

本轮材料检查：Agent/RAG两项`openspec validate <change> --strict --no-interactive`均valid、退出0；契约Schema元检查、43正例、47反例及跨样例身份/hash引用检查通过。35份当前Markdown的86个本地文件链接、109项Requirement和204个Scenario计数核对，文件链接/UTF-8/尾空白/围栏检查通过；锚点和Mermaid渲染未作自动证明。`git diff --check`退出0，因本批材料尚未跟踪，另对实际文件做检查，不把Git空输出当内容证据。小豆包backend/frontend没有diff；外部RAG原有修改列表保持，无产品源码编辑。

旧11月3日Schema/数据库检查只证明其原范围。当前CLI没有独立verify命令；status的isPlanningComplete/isComplete只表示材料存在，不覆盖tasks和实际测试。未运行新链路、真实PG/Kafka/Redis/Milvus或真实模型验收。
