# 全新数据库初始化与版本迁移

更新：2026-11-07。用户明确旧数据库数据均可舍弃。本文件展开[正式规格](../specs/agent-delegation/spec.md)的“全新数据库部署与新请求切换”，替代先前旧数据保留、backfill和legacy读取方案。

## 1. 部署边界

- 新部署使用Docker隔离PG；platform、travel、control各自schema和owner。
- 新账号、会话、消息、计划、证据、记忆、Task及checkpoint均由新系统生成，不导入旧库或映射旧身份。
- 新部署不执行原001～005迁移。原文件仅作尚未移除旧代码的回归材料。
- 启用新API/Worker/Travel前停止旧执行者；旧任务、旧Outbox和旧checkpoint不进入新链路。
- 取消旧历史读取账号、导入清单、偏好转换、暂停任务兼容收尾和历史只读回退。

## 2. 账号准备

管理员显式执行Provision，创建带部署前缀的角色和schema。三个owner均NOLOGIN；各组件迁移登录只可切换至对应owner。平台、Travel、Maintenance运行登录不继承owner，不具有CREATEDB、CREATEROLE、BYPASSRLS或业务schema CREATE权限。

角色标记绑定当前数据库OID，重放拒绝接管不属于该部署的角色或schema。口令通过私有文件或环境传递，不写入版本记录、输出或Git。

## 3. 初始化和后续版本

1. 使用专用control迁移登录执行control迁移。
2. 使用专用platform迁移登录执行platform迁移。
3. 使用专用travel迁移登录执行travel迁移。
4. 各组件独立schema_migrations记录版本、SQL摘要及时间；同库DDL用事务级advisory锁串行提交。
5. 重放校验摘要，不重复建表；拒绝篡改已执行SQL或跳过前序组件。
6. API和运行进程只使用运行账号，不在启动时执行DDL。

迁移登录只读前序组件版本元数据，不读取对方业务数据。FK只在所属schema内建立；服务间通过原ID、版本、hash和受控接口引用。

## 4. 新版本保护

用户舍弃的是旧数据库数据。新版本开始产生的消息、证据、记忆撤回、回执和审计继续遵循正式规格，不通过丢弃新事实恢复服务。未来版本使用受控迁移；故障恢复继续校验租约、epoch、授权、期限及幂等身份。

## 5. 验证证据

- 空库结构、账号、唯一键、复合归属和扫描索引可创建。
- 重复初始化不改变新结构或放宽运行权限。
- 真实session_user证明平台/Travel隔离及control私有表保护。
- 运行账号不能迁移，管理员不能冒充组件迁移登录。
- 新库不导入旧用户、任务或断点，不依赖public旧业务表。
- 测试资源清理核对xdb_v1_前缀及记录OID，不清理其他资源。
