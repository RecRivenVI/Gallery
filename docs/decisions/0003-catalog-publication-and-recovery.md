# ADR-0003：Catalog publication 与恢复

- 状态：Accepted
- 日期：2026-07-16

## 上下文

扫描、搜索投影和 Overlay 投影跨越大量行与两个数据库。读者不能观察半成品，进程也可能在 Catalog 已发布但 Job 尚未完成回写时中断。

## 决策

- 扫描先建立不可见 staging revision，完整写入并校验 seal 后再以短事务发布。
- 查询读取 query publication，它绑定一个 Catalog revision 与一个 Overlay projection revision。
- 旧 publication 由 lease 保护，直到 cursor 和读者不再需要。
- 跨库完成顺序以 Catalog publication 为权威，启动恢复先对账 publication，再收敛 control Job/Attempt。
- 未发布候选可以中止或重建；已发布候选不得重复发布同一逻辑结果。
- GC、VACUUM 和 checkpoint 通过维护协调器与活动 publication/Job 协调。

## 理由

不可变快照让搜索、作品和媒体在同一代次内一致；publication-first 对账把无法使用跨库事务的事实转化为可恢复协议；lease 让 keyset 分页可以继续读取旧快照。

## 影响

- Catalog 写路径必须是 staging → validate → publish。
- Job 完成状态不能反过来证明 publication 存在，恢复必须查 Catalog。
- API 和 cursor 都显式携带 publication 身份。
- 维护操作不能删除活动 revision。

## 重新审议

只有出现可证明的更简单协议，同时保持崩溃恢复、旧快照和跨库一致性时，才替换 publication 模型。
