# 术语表

| 术语 | 含义 |
| --- | --- |
| Gallery / 画廊 | 产品公开名称；代码与模块代号使用 `gallery` |
| `galleryd` | Go 后端服务进程 |
| `galleryctl` | 只消费公开 API 的命令行客户端 |
| AppDirs | Gallery 可写的 config、data、state、cache、logs、tmp 与 run 目录集合 |
| Library | 对若干 Source 的用户组织边界 |
| Source | 用户显式登记的只读媒体根及其规则上下文 |
| File root | 只读目录浏览入口；不产生 Catalog 事实，也不等同于 Source |
| Source-derived fact | 可由 Source 与规则重新计算的事实 |
| Canonical fact | 跨扫描保留的稳定身份、Binding、用户决策或安全事实 |
| Binding | Source 实体与 Canonical 实体之间的可审查映射 |
| Overlay | 用户对作品的标题、收藏、进度、封面等覆盖事实 |
| Catalog | 可从 Source、规则和 control 事实重建的查询投影库 |
| Catalog revision | 一次完整 Catalog 候选或已发布快照 |
| Overlay projection revision | Overlay 写入投影到查询库后的版本 |
| query publication | 把一个 Catalog revision 与一个 Overlay projection revision 绑定成可查询快照 |
| CanonicalWork | 稳定的作品身份 |
| CanonicalCreator | 稳定的创作者身份，可显式合并或撤销合并 |
| CanonicalMedia | 稳定的媒体 occurrence 身份，不等同于路径或内容摘要 |
| ContentBlob | 以内容摘要标识的媒体字节身份 |
| FileLocation | Source 内相对位置与文件身份的组合引用 |
| DerivedAsset | 从已确认 ContentBlob 生成的缓存型派生资源 |
| RulePackage | 可编辑、校验和规范化的规则文档 |
| RuleVersion | 由语义摘要标识的不可变已编译规则版本 |
| SourceRuleBinding | Source 到 RuleVersion 与参数快照的绑定 |
| Job / Attempt | 持久逻辑任务及其一次执行尝试 |
| capability | 服务端授权词表中的原子能力；角色只是预设集合 |
| Personal | loopback 单机模式 |
| LAN | 受信私网本地账户模式 |

文档中保留 `Source`、`Catalog`、`Binding`、`Overlay`、`Job`、`Attempt` 等英文领域名，不用“源”“目录库”“绑定任务”等临时同义词替换。操作原媒体时避免使用“同步”“导入媒体”或“上传”，这些词会暗示不存在的写入行为。
