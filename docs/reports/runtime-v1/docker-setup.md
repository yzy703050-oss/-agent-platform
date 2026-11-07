# Docker本机准备状态

2026-11-06，用户要求代理准备数据库/Redis等，并明确Docker部署。

- 重启前已发现本机DockerDesktop4.91.0程序文件、DockerCLI29.8.0及自带Compose插件；重启后进一步确认这只是旧安装失败留下的文件，不能认定Docker已成功安装，详见下文。
- 经Windows管理员授权启用VirtualMachinePlatform与WSL组件，两者退出3010；WSL安装退出0，实际wsl --version为3.0.3.0。状态为reboot_required，WSL --status仍报告虚拟化后端不能启动。硬件此前核验支持并已启用虚拟化。
- 首次创建用户.wslconfig，内存4GB、CPU上限4、swap4GB和gradual内存回收；既有配置若存在则保留。本机本轮约2.4GB可用内存，容器实际容量仍待运行验证。
- 固定六依赖Compose、私有随机PG/Redis/MinIO凭据、Prepare/Up/Status和运行说明已准备。Prepare真实compose config --quiet退出0；重复准备保留原密码，Windows DACL限定当前用户/SYSTEM，Linux0600。密码没有进入提交或报告。
- 脚本6项测试全部通过。Docker Up实际退出1，reason=windows_reboot_required_for_wsl；没有把配置通过当成容器就绪。
- 当前容器未启动，数据卷未创建，PG认证/角色/迁移、Redis命令、Kafka消息收发、Milvus索引仍未验收；原OpenSpec2.6保持未完成。需要用户重启Windows后继续Up及真实检查，代理未重启机器。
- A00/A01代码完成记录见对应报告；RAG R00仅完成配置代码检查点，完整依赖锁及模型验收未完成。

重启后执行：

```powershell
pwsh -NoProfile -File scripts/runtime-docker.ps1 -Action Up
```

脚本不删除容器/卷/业务数据；原业务PG服务与原仓库.env保持不变。

## 重启后复查（2026-11-06）

- Windows LastBootUpTime为19:44:23，晚于WSL组件安装记录；同次启动的重启门槛已解除。
- `runtime-docker.ps1 -Action Up`实际退出1，`reason=docker_engine_not_ready`。Docker Desktop界面明确报告缺少`SOFTWARE\Docker Inc.\Docker Desktop`注册项，尚未启动后端。
- 只读核对HKLM/HKCU安装注册项均不存在；`C:/ProgramData/DockerDesktop/install-log-admin.txt`记录9月23日安装因Windows组件启用失败退出1。此前“Docker已安装”的判断已按此证据更正。
- 从[Docker官方4.91.0发布记录](https://docs.docker.com/desktop/release-notes/#4910)下载同版本安装包，628014512字节；SHA-256与[官方校验值](https://desktop.docker.com/win/main/amd64/239619/checksums.txt)一致（`ac405b09942701770d581b173747fc1024cf0e6047cbe60f13d1df85437311ac`），Authenticode状态Valid、签发主体Docker Inc。按[官方Windows安装方式](https://docs.docker.com/desktop/setup/install/windows-install/)补全同版本WSL2安装；实际安装结果待核验，未手工伪造注册项。
- 容器和协议检查仍未通过，不将安装包校验或安装命令发起当成依赖就绪。

## 补全安装（2026-11-07）

- 用户反馈未看到前次UAC弹窗；复查确认旧启动器及授权进程均已退出、安装尚未开始，没有重复运行中的安装器。
- 重新校验同一安装包后，以可见安装程序运行。管理员安装日志于13:12:49记录`Installation succeeded`，已完成产品注册、服务和启动项部署。
- Docker Linux引擎实际`docker info`返回`29.8.0 linux`，重启与安装阻塞已解除。Compose正在拉取固定镜像；本轮使用实时日志保存启动输出，未更换镜像来源或修改用户全局代理。
- 真实协议探针已在忽略目录编译，准备分别检查PG认证及临时事务、Redis带认证读写、Kafka独立测试Topic消息往返、Milvus健康端点。该探针不代表A02权限迁移、RAG中文索引或真实模型验收；结果待容器启动后记录。

## 启动与基础协议结果（2026-11-07）

- Docker Hub最后一个Milvus层下载缓慢；从[Google公共Docker Hub缓存](https://docs.cloud.google.com/artifact-registry/docs/pull-cached-dockerhub-images)拉取同一固定digest，未修改全局代理或镜像锁。缓存manifest的SHA-256和实际pull输出均为`e6066d0afc4eeab69b7c9269e49be2b9ae54c740a21c05f230c4e5743c646ee3`，随后原Compose引用成功复用内容。
- 首次启动暴露Kafka挂载目录权限错误。真实日志为`AccessDeniedException`；同镜像`appuser`连接原卷执行`test -w`退出1，目录为root:root/0755。按[Apache镜像Dockerfile](https://github.com/apache/kafka/blob/4.4.0/docker/jvm/Dockerfile)改用`/var/lib/kafka/data`；仅在核对专用卷标签后，将既有卷顶层目录归属改为1000:0/0775，不递归删除或改写数据。相同可写性检查退出0，Kafka正常启动。
- `runtime-docker.ps1 -Action Up`实际退出0，scope=`docker_dependency_health_only`；postgres/redis/kafka/etcd/minio/milvus全部running/healthy。
- 主机侧Go探针实际退出0：PG经15432认证、临时表事务写入/读取并回滚；Redis经16379认证SET/GETDEL；Kafka经19092建独立测试Topic并生产/消费同一条消息；Milvus 19091健康端点返回200。结果保存在`.cache/runtime-v1/dependency-smoke.json`。测试Topic为`xdb_dependency_probe_d423ccf6-6364-4c72-995a-602e0e6fe4bd`，未删除其他Topic。
- 脚本6项测试复测通过。首次测试运行中，A01完整Go检查超过测试包装器30秒期限；单独完整验证实际退出0、63测试通过。将该编译场景的包装期限设为180秒，其他快速脚本检查仍为30秒，随后6项全部通过。
- 以上仅完成基础环境。平台/Travel/control角色与迁移、共享预算/Redis Sentinel、应用镜像、RAG中文索引与真实模型仍未完成，OpenSpec 2.6保持未勾选。
