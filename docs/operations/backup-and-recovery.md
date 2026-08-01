# 备份与恢复

## 备份对象

产品备份只包含 `control.db` 及其 manifest，不包含 `catalog.db`、Source 媒体、缓存或日志。实现使用 SQLite 一致性副本、完整性检查、SHA-256 和原子发布，备份目录位于 State AppDir 的 `backups/`。

备份包含账户、角色、Grant、审计和用户事实。Session、配对、API Token 与分享仅以验证摘要存在，并在恢复后失效；操作者应预期所有客户端重新认证。

## 操作流程

1. 在管理端“诊断”页面创建 control 备份任务。
2. 等待 Job 达到 `completed`，再从备份清单确认 backup ID、schema、大小与 checksum。
3. 在恢复前执行“验证恢复”。该步骤复制备份到隔离临时目录，前向迁移副本并检查完整性，不修改当前库。
4. 只有验证报告兼容时，登记恢复请求。
5. 停止并重新启动 `galleryd`。恢复只在启动期、取得 AppDirs 单写者锁且数据库尚未打开时应用。
6. 启动后重新登录，并确认恢复记录、关键用户事实和新的健康状态。

## 影响与失败语义

- 恢复会丢弃备份创建之后的 control 变更；
- 来自更高 schema 的备份被拒绝；较低兼容 schema 在隔离副本中验证前向迁移；
- 损坏、checksum 不符或无法迁移的备份不会替换当前库；
- 若替换越过连续性边界而无法确认当前库存在，服务应 fail-closed，不创建空库伪装成功；
- Catalog 不在备份中，必要时由 Source、规则和 control 事实重新建立。

不要手工复制正在运行的 `control.db`，也不要删除 `restore-pending.json`、轮换库或备份 manifest 来绕过失败状态。
