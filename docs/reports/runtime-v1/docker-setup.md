# Docker本机准备状态

2026-11-06，用户要求代理准备数据库/Redis等，并明确Docker部署。

- 已复用本机DockerDesktop4.91.0、DockerCLI29.8.0及自带Compose插件，没有重新安装Docker。
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
