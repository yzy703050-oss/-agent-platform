# Docker 本地隔离环境

本项目按用户选择使用 Docker Compose 部署。当前文件提供 PostgreSQL、Redis、Kafka、Milvus、etcd、MinIO 六个基础服务；API、Worker、Travel、Maintenance 和前端镜像将在实施任务 A13/A14 接入。基础容器健康不代表应用、数据库迁移或专业任务链路验收完成。

## 准备与启动

在本工作区根目录执行：

```powershell
pwsh -NoProfile -File scripts/runtime-docker.ps1 -Action Prepare
pwsh -NoProfile -File scripts/runtime-docker.ps1 -Action Up
pwsh -NoProfile -File scripts/runtime-docker.ps1 -Action Status
```

Prepare 不启动服务；它创建隔离配置、随机凭据与当前进程使用的 Docker CLI 配置，并在 CLI 可用时实际运行 `compose config --quiet`。再次执行复用原凭据，不更改用户的 `.env` 或全局 Docker CLI 配置。Up 在 Windows 必要时启动已有 Docker Desktop，使用 Linux 引擎，拉取锁定镜像并等待全部六个健康检查。失败返回非零，日志保存在本地状态目录。

本机于 2026-11-06 安装了 WSL 3.0.3，并于 2026-11-07 补全 Docker Desktop 4.91.0 安装；Linux 引擎和六个依赖容器已通过健康检查，基础协议探针结果见[本机报告](../../docs/reports/runtime-v1/docker-setup.md)。若其他机器启用 Windows 组件后要求重启，Up 会在同次 Windows 启动内明确返回 `windows_reboot_required_for_wsl`；脚本不执行重启。重启后仍使用同一条 Up 命令继续，不重复生成密码。

默认状态目录为当前工作区的 `.cache/runtime-v1`，可用 `-StateDirectory <绝对目录>` 显式指定另一个私有目录。它包含：

- `docker-secrets.json`：本环境的 Compose project 与 PG、Redis、MinIO 随机密码。Windows 仅当前用户和 SYSTEM 有权限，Linux 为 0600；不提交 Git，不打印密码。
- `profile.json`：隔离端点和配置引用。它不是产品已部署证明。
- `docker-cli/`：本次命令使用的插件查找配置，不修改用户全局 CLI 设置。
- `compose-*.log` / `docker-engine.log`：配置、启动与状态日志。
- `wsl-bootstrap-result.json`：本机管理员安装结果，仅本次机器生成，不复制到其他机器冒充状态。

保留本地凭据与 project 名，后续才能访问原来的 Docker 数据卷。脚本没有删除容器、卷或业务数据的命令。归档工作区前须先保留所需的忽略文件。

## 端点及范围

| 服务 | 主机端点 | 说明 |
| --- | --- | --- |
| PostgreSQL | 127.0.0.1:15432 | 新库 `runtime_fixture`，管理员 `runtime_fixture_admin`；A02 再建立隔离测试数据库与真实服务角色 |
| Redis | 127.0.0.1:16379 | requirepass；单实例功能环境，不冒充 Sentinel 故障验收 |
| Kafka | 127.0.0.1:19092 | 单节点 KRaft 隔离夹具；容器内使用 kafka:29092 |
| Milvus | 127.0.0.1:19530 | 独立存储；健康端口 127.0.0.1:19091 |
| etcd / MinIO | 仅 Compose 内部网络 | Milvus 官方组合依赖；不公开管理端口 |

所有公开端口绑定回环地址。镜像固定 linux/amd64 manifest，见 `compose.runtime-v1.yaml`。此文件用于开发与真实集成测试，不提供多主机容灾。正式授权、预算围栏、Redis Sentinel 和多实例应用故障矩阵仍按 OpenSpec 执行。

Kafka 数据卷挂载到官方镜像已授权给 `appuser` 的 `/var/lib/kafka/data`，`KAFKA_LOG_DIRS` 使用相同路径。若已使用旧版配置创建过 root 所有的空目录，仅改挂载目标不会改变旧卷的权限；应先核对卷的 Compose project/volume 标签，再修正该目录的归属，保留数据卷。

Up/Status 只有在每个服务都为 running/healthy 时返回 `ok=true`；返回范围为 `docker_dependency_health_only`。之后仍需验证 PG 认证/权限/迁移、Redis 实际命令、Kafka 消息收发以及 Milvus 中文索引。Embedding/Reranker 权重和 RAG 模型加载另由 R00/R06 核验。

安装依据：[Docker Windows 安装说明](https://docs.docker.com/desktop/setup/install/windows-install/)、[Microsoft WSL 安装说明](https://learn.microsoft.com/en-us/windows/wsl/install)。
