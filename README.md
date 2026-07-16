# Gallery

Gallery（画廊，代码代号 `gallery`）是面向个人及可信局域网的本地优先、只读媒体目录产品。它是独立净室项目，不兼容或迁移任何旧 Gallery 实现。

> 当前状态：Architecture Proof Slice 已完成正确性验收，正式实现已证明规则、双 revision 查询、Overlay 重投影、Catalog 重建、媒体缓存生命周期和强杀恢复路径。当前仍没有可供普通用户安装的产品版本；物理 Schema 和完整 API 因百万级参考性能、完整排序/过滤语义及平台门禁尚未冻结。

## 当前可运行能力

- `galleryd` 在 loopback 启动，创建彼此独立的 WAL `control.db` / `catalog.db`，并在启动时执行迁移和跨库 reconciliation；
- Personal 模式提供短时单次配对、服务端 HttpOnly Session、available/effective capability、Session 列表和吊销；
- 可通过正式 API 创建 Library、严格只读 Source、规范化/编译 RuleVersion 和 SourceRuleBinding；
- 持久 Scan Job 通过冻结 Rule IR 识别合成作品/媒体，完成全文件 SHA-256、Canonical Binding、Catalog staging 和短事务 query publication；
- REST 可查询 Job、当前 publication、Work 和 Media；媒体内容支持稳定 Media ID、HEAD、完整 GET、单区间 Range、显式 ETag 和条件请求；
- `/ws/v1` 推送 Job、query publication 和 Session 吊销事件；断线后的事实恢复仍使用 REST snapshot；
- 在任何写入和数据库初始化前检查 AppDirs 与启动参数 Source；运行中登记 Source 时再次检查真实路径、AppDirs 和已登记 Source 的双向重叠；
- 原子发布带 nonce/ownership 的运行 descriptor，并在正常停止时按 ownership 清理；
- `galleryctl health` 只使用公开的 `pkg/galleryapi` 生成客户端，不访问数据库、应用服务或后端 `internal` 包；
- OpenAPI、错误信封、WebSocket 信封、签名游标、规则包 JSON Schema 与 CEL Profile v1 均可执行校验。
- 规则 API 提供 Schema 感知规范化、默认值物化、三类 hash、validate/compile/Dry Run/Trace/Impact、参数校验和编译缓存；扫描器只执行版本化 Rule IR，不含平台特例；
- Work 查询使用同一 publication 内的 FTS5、CJK bigram/拉丁与文件名 trigram、原文复核、自然排序 v1、稳定 tie-break、签名 keyset cursor 和持久租约；
- 标题 Override、ManualTag、HiddenState、CustomCover 写入 control 后由持久 Overlay Job 发布新 projection；Favorite/Progress 作为实时状态不改变分页成员或顺序；
- Catalog 删除重建会通过稳定来源引用恢复 Canonical Work/Creator/Media、Binding、Overlay、Favorite、Progress 和媒体 URL；冲突与手动解绑不会被静默覆盖；
- ContentBlob、FileLocation 和逻辑 Media 分离；DerivedAsset 使用完整 key、受校验 manifest、singleflight、原子发布、读取 lease 和 GC，旧 publication 同样受游标与 Blob 读取 lease 保护；
- 八个独立子进程强杀点覆盖扫描、publication、Overlay、DerivedAsset 和完整哈希，重启 reconciliation 保持旧快照可读并且不写 Source。

上述能力代表合成 Source 上的 Architecture Proof 正确性切片，不代表百万/千万正式性能、真实媒体规模、多平台支持、Web/PWA 或发行就绪。当前冻结结论和剩余门禁见 [v1 实施计划](Documents/指南/01-v1实施计划.md) 与 [验证记录](Documents/证据/验证记录.md)。

## 当前 API 流程

从空 AppDirs 启动后，正式客户端按以下顺序使用 `/api/v1`；绝对 Source 路径只出现在创建请求和 control 私有事实中，不进入资源响应：

1. `GET /bootstrap`，取得匿名 CSRF 和 available/effective capability；
2. `POST /personal/pairing-attempts`，再 `POST /personal/pair` 建立 HttpOnly Session；
3. `POST /libraries`、`POST /sources`；
4. `POST /rule-versions`、`POST /source-rule-bindings`；
5. 可先调用 `/rules/validate`、`/rules/compile`、`/rules/dry-run` 和 `/rules/impact`，再连接 `/ws/v1` 并 `POST /sources/{sourceId}/scan-jobs`；
6. 通过 `GET /jobs/{jobId}` 取得事实状态，通过 WS 接收提示事件；
7. `GET /query-publications/current`，通过 `GET /works` 的服务端过滤、搜索、排序和 cursor 分页读取固定快照，再读取 `/works/{workId}/media`；
8. 对 `/media/{mediaId}/content` 执行 HEAD、GET 或单区间 Range GET；
9. 通过 `GET/PUT /works/{workId}/overlay` 写入并观察 pending/published/failed；服务重启后复用未吊销 Session，并重新读取 Job、publication 和媒体 snapshot。

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
