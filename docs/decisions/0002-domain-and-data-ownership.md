# ADR-0002：领域与数据所有权

- 状态：Accepted
- 日期：2026-07-16

## 上下文

Source 可以离线、改名或被重扫，Catalog 也需要可删除重建；账户、授权、用户覆盖和人工 Binding 决策不能因此丢失。路径和内容摘要也不能同时承担位置、occurrence 与内容三种身份。

## 决策

- `control.db` 保存 Canonical、用户、安全、规则生命周期、Binding、Job 与治理事实。
- `catalog.db` 保存 Source-derived、搜索、媒体定位、派生资源和 publication 投影。
- Source 实体先映射到 CanonicalWork、CanonicalCreator、CanonicalMedia，再通过 publication 查询。
- CanonicalMedia、ContentBlob 和 FileLocation 是三个独立身份层。
- Overlay 独立保存用户事实并通过 projection revision 进入查询快照。
- 冲突、拆分、合并、解绑与 orphan 生命周期通过持久 Binding 证据和人工决策表达。

## 理由

明确所有权使 Catalog 可以重建，同时保留用户长期投入；分离媒体身份避免路径变化或内容重复造成错误合并；持久治理记录让自动匹配失败可被解释和修复。

## 影响

- 重扫不得直接覆盖 control 事实。
- 删除 Source 或 Catalog 不等于删除 Canonical 实体。
- 备份优先保护 `control.db`；Catalog 通过产品流程重建。
- 客户端只能使用公开稳定 ID，不使用数据库 row ID 或绝对路径。

## 后续约束

新增领域事实必须先决定所有者和重建方式。无法明确归属的字段不能随意加入 Catalog 表或 API。

## 重新审议

若某类当前标为可重建的事实在真实恢复中无法确定性重建，应新增决策调整所有权，而不是在扫描器中静默保留旧值。
