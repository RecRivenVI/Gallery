# 扫描、Catalog 与任务

## 扫描入口

扫描是持久 Job，不是同步 HTTP 请求。创建 Job 时冻结 Source、有效规则 Binding、参数、编译器/CEL/primitive 版本和扫描档案；执行时不改用后来发布的规则。

当前扫描档案：

| 档案 | 内容确认行为 |
| --- | --- |
| `index` | 不计算完整摘要，以 `located_unverified` 发布；新 Source 的默认档案 |
| `incremental` | 对未变化且身份、大小、mtime 一致的媒体复用既有摘要，否则哈希；已有 publication 的默认档案 |
| `verify` | 忽略既有观察，对发现媒体重新完整哈希 |

媒体确认 API 还可以把同一 Source 的一个或多个目标媒体合并为目标化 incremental Scan Job。它只强制列出的目标，不能让同目录其它媒体被顺带哈希。

## 执行流

1. 解析 Source 与有效 Rule Binding；
2. 只读遍历目录，拒绝链接或 reparse point 逃逸；
3. 读取有界 metadata，并对规则包执行作品、创作者、媒体、封面、日期和呈现求值；
4. 按扫描档案复用或计算内容确认，必要时建立 Hash 子任务；
5. 在 `catalog.db` 建立 staging revision，写入作品、关系、媒体、FTS 与聚合投影；
6. 校验候选计数、关系、Source 成员和 seal；
7. 用短事务发布 Catalog revision；
8. 把 Job 与 publication 结果回写 `control.db` 并广播变化。

discovery 回调检查 `context`，取消后不应继续无界枚举。任何 Source 读取失败都通过结构化错误和 Job 终态表达，不在 Source 写入标记。

## Publication

Catalog revision 有 staging、published 或 aborted 等状态。查询不直接读“当前表”，而是读取 query publication 绑定的：

- `catalogRevision`；
- `overlayProjectionRevision`；
- Source 成员集合和依赖字段投影。

发布前的候选不可见。Overlay 写入不修改旧 Catalog revision，而是建立新的 Overlay projection 并发布新的组合快照。旧 cursor 继续绑定旧 publication，直到 lease 到期或被明确判定过期。

## Job 与 Attempt

Job 保存类型、来源、创建者、状态、阶段、进度、失败码、publication 和取消意图。Attempt 保存一次执行的领取、心跳、终态与恢复信息。列表使用新到旧 keyset 分页，所有候选在响应前重新授权。

取消是持久意图：调度器取消活动 context，父 Scan 可级联到 Hash；已越过不可逆发布点的任务不能伪装为“从未发布”。重试在同一逻辑 Job 下创建新 Attempt，并遵守重试上限和 next-attempt 时间。

## 启动恢复

新进程取得 AppDirs 锁后、Scheduler 接收新工作前执行恢复：

- 先以 Catalog publication 事实对账，避免已经发布的候选被重复构建；
- 将上一进程遗留的 Attempt 标记为 recovered/中断，并按 Job 策略继续或失败；
- 收敛遗留 staging candidate；
- 恢复 Watcher 和 Source dirty 状态。

跨库一致性依靠这套 publication-first 对账，不依赖不存在的跨 SQLite 原子事务。

## Watcher 与收敛

Watcher 只提供 dirty hint，周期性 reconcile 才是收敛事实源。当前生产适配器是 polling watcher，间隔和批量上限属于未冻结运行策略。Overflow、身份变化、权限拒绝和 Source 不可用有独立错误码。

## 维护与 GC

Catalog GC、checkpoint、VACUUM、备份和派生资源清理由独立维护 Job 执行，并与扫描/publication 协调。publication lease 保护仍在读取的 revision；维护不能为了释放空间删除活动快照或正在运行 Job 的候选。

## 主要实现位置

- `internal/scanner/service.go`
- `internal/jobs/`
- `internal/catalog/store.go`
- `internal/catalog/publication_lease.go`
- `internal/maintenance/`
- `internal/watcher/`
