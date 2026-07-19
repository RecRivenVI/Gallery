# Gallery

Gallery（画廊，代码代号 `gallery`）是面向个人及可信局域网的本地优先、只读媒体目录产品。它是独立净室项目，不兼容或迁移任何旧 Gallery 实现。

> 当前状态：**阶段 4「查询和媒体」已完成代码与合成 Correctness 实现**（阶段 0、Walking Skeleton、Architecture Proof 正确性切片、阶段 1、阶段 2 与阶段 3 同样已完成相应 Correctness 实现）。阶段 3 已接入持久 Job/attempt、六类独立资源池、可取消完整 SHA-256 Hash Job、Watcher 周期收敛、staging/publication、GC/VACUUM/空间预检、DerivedAsset 与外部工具执行边界，以及 REST/OpenAPI/生成客户端和迁移测试；真实 SSD（约 36.6 万文件）与 HDD（约 63.2 万文件）验收发现默认全量完整哈希扫描在真实规模下不适合作为日常路径，扫描改为 `index`（快速发布未确认媒体）/`incremental`（默认，仅对新增或变化媒体建立 Hash Job）/`verify`（显式强制完整性校验）三种档案，详见 [验证记录 EV-25](Documents/证据/验证记录.md#ev-25ssdhdd-真实大数据集验收与轻量扫描档案重构e3--e1)（全量扫描未完成，正式全量性能 Gate 仍未通过）。阶段 4 新增服务端权威结构化查询字段注册表（AND/OR/NOT）、Creator 合并查询等价组解析、版本化 Ranking/高亮/Total 协议、签名 keyset 游标 rank 扩展、Overlay 查询依赖服务端权威分类表、媒体 If-Range、`located_unverified` 媒体按需内容确认闭环与 DerivedAsset 受限 JPEG 缩略图端到端公共契约，详见 [验证记录 EV-30](Documents/证据/验证记录.md)。当前仍没有可供普通用户安装的产品版本；真实全量规模 HDD 性能特征、SMB/NAS、网络挂载、真实平台文件身份、正式 Reference/Degradation Performance Gate 和 ranking/total/cursor 租约等 PRE_FREEZE 数值冻结留待下一轮实测，FileLocation 最终唯一约束、多平台和发行签名门禁仍未完成。SourceWork 决策撤回仅限尚未被扫描消费的 pre-seed Binding，消费后返回 `CONFLICT`，不等于已生效结构变化的完整反向操作。

## 当前可运行能力

- `galleryd` 在 loopback 启动，创建彼此独立的 WAL `control.db` / `catalog.db`，并在启动时执行迁移和跨库 reconciliation；
- Personal 模式提供短时单次配对、服务端 HttpOnly Session、available/effective capability、Session 列表和吊销；
- 可通过正式 API 创建 Library、严格只读 Source、规范化/编译 RuleVersion 和 SourceRuleBinding；
- 持久 Scan Job 通过冻结 Rule IR 识别合成作品/媒体，完成全文件 SHA-256、Canonical Binding、Catalog staging 和短事务 query publication；
- REST 可查询 Job、当前 publication、Work 和 Media；媒体内容支持稳定 Media ID、HEAD、完整 GET、单区间 Range、显式 ETag 和条件请求；
- `/ws/v1` 推送 Job、query publication 和 Session 吊销事件；断线后的事实恢复仍使用 REST snapshot；
- 在任何写入和数据库初始化前检查 AppDirs 与启动参数 Source；运行中登记 Source 时再次检查真实路径、AppDirs 和已登记 Source 的双向重叠；
- 打开或迁移数据库前取得 AppDirs 进程独占锁（Windows/Unix 各自平台 adapter）；第二个实例在此以 `INSTANCE_ALREADY_RUNNING` 失败，不打开数据库、不迁移、不监听、不改写活动 descriptor，锁在进程退出或强杀后由操作系统释放；
- 原子发布带 nonce/ownership 的运行 descriptor，并在正常停止时按 ownership 清理；
- 扫描与 Overlay 投影 Job 经中央有界调度器领取：按资源类别独立并发上限、context 取消传播、重复领取防护和 graceful shutdown，未完成 Job 由启动 reconciliation 重新入队；
- 阶段 3 的 Scan、Hash、Overlay、Derived、External Tool、Maintenance 使用独立有界资源池；Job 持久化 attempt、租约/心跳、单调字节与实体进度、取消、重试和幂等键，启动时收敛强杀与未完成任务；
- Watcher 只写 dirty/overflow 提示，周期性 Source 收敛才创建扫描 Job；Source 离线、权限、文件身份变化和内容消失均保留旧 publication，不把暂时不可达误判成删除；
- Catalog 维护 Job 支持 staging candidate 活跃保护、GC dry-run、WAL checkpoint、VACUUM 和 AppDirs 临时文件清理；哈希结果只有完整 SHA-256 且前后身份复核成功后才能进入 publication；
- `galleryctl health` 只使用公开的 `pkg/galleryapi` 生成客户端，不访问数据库、应用服务或后端 `internal` 包；
- OpenAPI、错误信封、WebSocket 信封、签名游标、规则包 JSON Schema 与 CEL Profile v1 均可执行校验。
- 规则 API 提供 Schema 感知规范化、默认值物化、三类 hash、validate/compile/Dry Run/Trace/Impact、参数校验和编译缓存；扫描器只执行版本化 Rule IR，不含平台特例；
- 规则闭环 API 提供 RulePackage/Draft/Version 生命周期、revision 并发保存、JSON/YAML/TOML 导入、参数集 revision/hash、Explain/Trace/diff/Impact、回滚/弃用/审计、持久编译缓存、三类内置示例列表和受限示例测试；每个扫描 Job 记录规则语义、参数和 Rule IR 快照；
- Work 查询使用同一 publication 内的 FTS5、CJK bigram/拉丁与文件名 trigram、原文复核、自然排序 v1、稳定 tie-break、签名 keyset cursor 和持久租约；
- 标题 Override、ManualTag、HiddenState、CustomCover 写入 control 后由持久 Overlay Job 发布新 projection；Favorite/Progress 作为实时状态不改变分页成员或顺序；
- Catalog 删除重建会通过稳定来源引用恢复 Canonical Work/Creator/Media、Binding、Overlay、Favorite、Progress 和媒体 URL；冲突与手动解绑不会被静默覆盖；
- 用户可经 `/creators` 查看 CanonicalCreator 及来源 Binding 证据，用 `/creators/merges` 合并疑似同一创作者、用 `DELETE /creators/merges/{id}` 撤销；合并以 `merged_into` 记录于 control，不改写 Binding，复用 Overlay 投影 Job 更新查询与搜索，撤销、重扫和服务重启后结果一致；
- 扫描无法唯一确定 Canonical Binding 时持久化富化 Binding issue（实体类型、来源稳定键、候选证据、状态与乐观版本），按候选指纹去重、忽略与 stale 收敛；用户经 `/binding-issues` 查看，用 `/binding-issues/{id}/resolve|dismiss|reopen` 与 `/binding-actions/unbind-work|unbind-media|undo-unbind` 修复，下一次扫描据此重建投影；Source 在线时未发现的 Binding 转 inactive，连续多次成功扫描仍缺失则按保留窗口升级为 orphan candidate；用户经 `/orphan-candidates` 与 `/orphan-candidates/{bindingId}/decide` 选择保留、延长、确认孤立或解绑，四种决策都不删除 Canonical 与用户事实，重现后复用原 Canonical 实体；
- ContentBlob、FileLocation 和逻辑 Media 分离；DerivedAsset 使用完整 key、受校验 manifest、singleflight、原子发布、读取 lease 和 GC，旧 publication 同样受游标与 Blob 读取 lease 保护；
- control.db 可经 `admin.backup` 生成产品级备份：SQLite 一致性副本 + 自描述 manifest（role、schema 版本、checksum、安全范围），接入 `maintenance` 有界调度类别，写临时位置校验后原子发布到 AppDirs 受控目录，不纳入 catalog、媒体或缓存，Source 零写入；用户经 `POST/GET /admin/control-backups` 创建与列出；
- control.db 恢复经 `admin.restore`：`POST /admin/control-restores/verify` 做 Dry Run 验证（checksum、版本兼容、隔离迁移与完整性/外键检查），`POST /admin/control-restores` 登记待应用请求，下次启动在打开数据库前于单写者锁下隔离迁移并原子替换当前库、轮换旧库，坏备份或迁移失败保留当前库，恢复后作废 Session/pairing 与非终态 Job；备份 control.db、删除 catalog.db、恢复 control.db 再全量重扫可端到端恢复人工决策；
- 八个独立子进程强杀点覆盖扫描、publication、Overlay、DerivedAsset 和完整哈希，重启 reconciliation 保持旧快照可读并且不写 Source。

- SourceWork 拆分/合并按 ContentBlob digest 证据检测，复用 Binding issue（`SOURCE_WORK_SPLIT/MERGE_REVIEW_REQUIRED`）阻塞 publication；人工决策经 `POST /binding-issues/{id}/resolve-structure`（继承/保持同一/新建、绑定现有/新建）以 pre-seed WorkBinding 表达，`GET /source-structure-decisions` 与 `.../undo` 查询/撤回；撤回仅清除尚未被扫描消费的 pre-seed Binding，已消费时返回 `CONFLICT` 且不做部分修改，已生效结构变化需新的人工决策或未来补偿流程，决策经 Catalog 全量重建恢复；

上述能力代表合成 Source 上的正确性切片，不代表百万/千万正式性能、真实媒体规模、多平台支持、Web/PWA 或发行就绪。当前冻结结论和剩余门禁见 [v1 实施计划](Documents/指南/01-v1实施计划.md) 与 [验证记录](Documents/证据/验证记录.md)。

## 当前 API 流程

从空 AppDirs 启动后，正式客户端按以下顺序使用 `/api/v1`；绝对 Source 路径只出现在创建请求和 control 私有事实中，不进入资源响应：

1. `GET /bootstrap`，取得匿名 CSRF 和 available/effective capability；
2. `POST /personal/pairing-attempts`，再 `POST /personal/pair` 建立 HttpOnly Session；
3. `POST /libraries`、`POST /sources`；
4. `POST /rule-versions`、`POST /source-rule-bindings`；
5. 可先调用 `/rules/validate`、`/rules/compile`、`/rules/dry-run` 和 `/rules/impact`，再连接 `/ws/v1` 并 `POST /sources/{sourceId}/scan-jobs`；
6. 通过 `GET /jobs/{jobId}`、`GET /jobs/{jobId}/attempts` 和 `GET /sources/{sourceId}/scan-status` 取得事实状态，通过 `POST /jobs/{jobId}/cancel|retry` 控制可重试任务，并通过 WS 接收提示事件；
7. 管理员可通过 `POST /admin/maintenance/gc|checkpoint|vacuum` 创建持久维护 Job，再用同一 Job API 观察进度、取消和失败原因；
8. `GET /query-publications/current`，通过 `GET /works` 的服务端过滤、搜索、排序和 cursor 分页读取固定快照，再读取 `/works/{workId}/media`；
9. 对 `/media/{mediaId}/content` 执行 HEAD、GET 或单区间 Range GET；
10. 通过 `GET/PUT /works/{workId}/overlay` 写入并观察 pending/published/failed；
11. 通过 `GET /creators` 与 `/creators/{creatorId}` 核对创作者证据，用 `POST /creators/merges` 合并、`DELETE /creators/merges/{mergeId}` 撤销，并按返回的 `projectionJobId` 观察查询投影更新；
12. 通过 `GET /binding-issues` 与 `/binding-issues/{issueId}` 查看绑定歧义与候选证据，用 `/binding-issues/{issueId}/resolve|dismiss|reopen` 或 `/binding-actions/unbind-work|unbind-media|undo-unbind` 修复后重扫；通过 `GET /orphan-candidates` 查看到达保留窗口的孤立候选，用 `/orphan-candidates/{bindingId}/decide` 决定保留、延长、确认孤立或解绑；服务重启后复用未吊销 Session，并重新读取 Job、publication 和媒体 snapshot。

阶段 3 的任务执行已完成 Correctness 修正：retry 在同一 Job ID 下增加 Attempt，队列满保持持久 `queued` 并由周期恢复重提；Watcher 默认采用五分钟低频 polling fallback 并动态管理 Source。Catalog 候选（Candidate）归逻辑 Job 所有而非归 Attempt 所有，`BeginCandidate` 是幂等恢复入口，同一 Job 被强杀后的新 Attempt 会安全重置未发布候选并从头 Stage，不会因历史候选残留而永久失败（见 [验证记录 EV-28](Documents/证据/验证记录.md)）。真实 SSD/HDD 大数据集结构抽样、只读清单和有界样本验收（全量扫描未完成，正式全量性能 Gate 仍未通过；见 [验证记录 EV-25](Documents/证据/验证记录.md)）后，扫描新增 `scanProfile`：默认 `incremental` 按 Source/相对路径/大小/mtime 组合证据与既往已发布 `content_verified` 观察比较，跨扫描复用未变化媒体的已确认摘要，仅对新增或变化媒体建立 Hash Job；`index` 只发现定位、不建立 Hash Job，媒体以 `located_unverified` 发布；`verify` 忽略既往观察强制重新完整哈希。维护空间预算由服务端给出，外部工具或 DerivedAsset resolver 未配置时会在创建 Job 前返回稳定不可用错误，因此当前不宣称已接入正式 ffmpeg 或完整变换集合。

仓库内的完整生成客户端验收见 `internal/bootstrap` 与 `internal/transport/httpapi` 测试；合成输入位于 `tests/fixtures/walking-skeleton`。

## 开发环境

正式工具链使用 Go 1.26.5。若 `go` 不在 PATH，可设置 `GALLERY_GO` 为 `go.exe` 的完整路径。

```powershell
$env:GALLERY_GO = "C:\path\to\go.exe"
& $env:GALLERY_GO run ./cmd/galleryd --listen 127.0.0.1:18080 --app-root "$env:TEMP\gallery-dev"
```

另开终端验证生成客户端：

```powershell
& $env:GALLERY_GO run ./cmd/galleryctl --base-url http://127.0.0.1:18080 health
```

完整本地门禁：

```powershell
./Check.ps1
./Check.ps1 -Race  # 需要当前平台支持 Go race detector
```

也可直接运行：

```powershell
go generate ./...
go test ./...
go vet ./...
go build ./cmd/...
go run golang.org/x/vuln/cmd/govulncheck@v1.6.0 ./...
```

## 当前工程选型

| 主题 | 当前选择 | 理由 |
| --- | --- | --- |
| SQLite | `modernc.org/sqlite` v1.53.0 | 纯 Go、基础发行不依赖 cgo；延续 Cleanroom 的可运行证据并升级到当前版本 |
| migration | 内嵌 forward-only runner | 仅服务两个 SQLite 文件；逐迁移事务、SHA-256 防历史改写，不引入多数据库 CLI/驱动依赖树 |
| OpenAPI | `oapi-codegen` v2.7.2 | 支持 OpenAPI 3.0 和生成 Go client/model；以固定版本的 `go generate` 开发工具运行，不进入生产依赖 |
| WebSocket | `github.com/coder/websocket` v1.8.15 | API 小、支持 `context.Context`、零传递依赖，适合标准 `net/http` |
| 结构化日志 | 标准库 `log/slog` JSON handler | 无额外框架，字段化输出并保持日志与领域解耦 |
| JSON Schema | `jsonschema/v6` v6.0.2 | 支持 Draft 2020-12，用同一校验器执行规则和协议 Schema |
| 测试 | 标准库 `testing` + `httptest` | 当前断言无需额外测试 DSL；依赖小且可覆盖真实 HTTP/WS/SQLite 边界 |

这些是当前正式实现选择，可通过兼容升级调整；不得借库替换改变两库所有权、只读 Source、publication、API 或 capability 语义。

## 仓库导航

- [Agent 与工程规则](AGENTS.md)
- [工程文档唯一入口](Documents/README.md)
- [v1 实施计划](Documents/指南/01-v1实施计划.md)
- [测试与发布门禁](Documents/指南/02-测试与发布门禁.md)
- [正式测试约定](tests/README.md)
- `Test-Bench/cleanroom-lab*`：历史净室原型和证据材料，不是正式代码依赖
