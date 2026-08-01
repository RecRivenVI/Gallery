# 领域模型与数据所有权

## 两类事实

Gallery 将数据分为两个所有权域：

| 类型 | 示例 | 存储 | 重建语义 |
| --- | --- | --- | --- |
| Canonical / user-owned | 账户、Grant、Library、Source 登记、规则版本、Binding、Overlay、人工治理决策 | `control.db` | 不得由重扫覆盖 |
| Source-derived | 作品投影、媒体位置、内容摘要、搜索字段、聚合封面 | `catalog.db` | 可从 Source、规则和 control 事实重建 |

“可重建”不表示可以随意删除正在使用的 Catalog；重建仍需通过维护、扫描和 publication 流程。

## 核心实体

- `Library`：用户组织与授权边界，可包含多个 Source。
- `Source`：只读媒体根及其扫描上下文；根路径不通过普通列表 API 暴露。
- `RulePackage`：可编辑规则容器；发布后产生不可变 `RuleVersion`。
- `SourceRuleBinding`：冻结 Source、RuleVersion、参数或参数集、优先级和编译 IR 身份。
- `CanonicalWork`：跨扫描稳定的作品身份。
- `CanonicalCreator`：跨扫描稳定的创作者身份，支持显式合并与撤销。
- `CanonicalMedia`：一个稳定媒体 occurrence 的身份。
- `ContentBlob`：由 `sha256-v1` 摘要标识的内容字节身份。
- `FileLocation`：Source、文件身份版本和 location key 的位置引用。
- `DerivedAsset`：以 Blob 与 transform 身份寻址的可重建缓存资源。
- `Job` 与 `Attempt`：逻辑任务和一次持久执行尝试。

公开 ID 是带类型的 opaque 标识。内部 row ID、绝对路径或排序键不作为外部身份。

## Binding

扫描先得到 SourceWork、SourceCreator 和 SourceMedia，再由 Binding 映射到 Canonical 实体。Binding 记录来源证据与生命周期，不把路径直接升级为 Canonical ID。

当证据不足或冲突时，系统产生 Binding issue。人工可以绑定既有实体、保持分离、建立新实体，或对 SourceWork 结构执行拆分/合并决策。已被扫描消费的结构决策不能通过删除一条记录自动反向；需要新的补偿性决策。

人工解绑不删除 Canonical 或用户事实。孤立 Binding 先经过保留窗口，再进入 orphan candidate 审查；决定可以保留、延长、确认孤立或解绑。

## Creator 与作品关系

作品可有多个 Creator 关系，关系包含 role 与 ordinal。Creator merge 把 absorbed 身份映射到有效 target，同时保留合并记录以支持撤销和重新投影。查询和聚合封面使用 publication 中已物化的有效关系，不在客户端临时合并。

## Overlay

Overlay 保存用户对 Work 的可变事实，例如标题覆盖、收藏、进度和自定义封面。写入先提交 `control.db`，影响查询的字段随后异步投影到新的 Overlay projection revision，并与当前 Catalog revision 发布成新的 query publication。

读取端必须区分快照值与可读取的 live user state。当前 `favorite` 与 `progress` 可以通过 Overlay 详情读取 live 值；列表排序仍以其绑定 publication 为准。

## 媒体身份

路径变化不必改变 CanonicalMedia，内容相同也不意味着两个 occurrence 自动成为同一 CanonicalMedia。媒体读取先从 publication 找到 CanonicalMedia 与 ContentBlob，再解析当前 FileLocation，并在打开后复核文件身份、大小和时间证据。完整读取还会复算摘要；Range 读取不能为此强制读取全文件。

## 删除与恢复

- Source 消失或离线不会自动删除 Canonical 和用户事实。
- Catalog GC 只回收不再被 publication lease 引用的可重建数据。
- control 备份是产品级恢复对象；Catalog 由扫描和投影重建。
- 恢复 control 会作废 Session、Token、配对和分享凭据摘要对应的现有访问。

## 主要实现位置

- `internal/application/resources.go`
- `internal/application/bindings.go`
- `internal/application/structure.go`
- `internal/application/orphans.go`
- `internal/domain/`
- `internal/storage/migrations/control/`
- `internal/storage/migrations/catalog/`
