# Gallery

Gallery（画廊，代码代号 `gallery`）是面向个人及可信局域网的本地优先、只读媒体目录产品。它是独立净室项目，不兼容或迁移任何旧 Gallery 实现。

> 当前状态：阶段 0 契约骨架已进入正式实现和自动化验证；尚未完成 Walking Skeleton，也没有可供普通用户安装的产品版本。

## 当前可运行能力

- `galleryd` 在 loopback 启动，创建彼此独立的 `control.db` / `catalog.db`，运行 WAL 与内嵌 migration；
- 在任何写入和数据库初始化前检查 AppDirs、Source 真实路径与双向重叠；
- 提供 `/api/v1/health`、匿名非管理员的 `/api/v1/bootstrap`，以及拒绝匿名升级的 `/ws/v1`；
- 原子发布带 nonce/ownership 的运行 descriptor，并在正常停止时按 ownership 清理；
- `galleryctl health` 只使用生成的 OpenAPI 客户端，不访问数据库或内部服务；
- OpenAPI、错误信封、WebSocket 信封、签名游标、规则包 JSON Schema 与 CEL Profile v1 均可执行校验。

上述能力只代表阶段 0 工程基础，不代表 Library、扫描、Catalog publication、配对、媒体读取或 Web UI 已交付。

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
./scripts/Check.ps1
./scripts/Check.ps1 -Race  # 需要当前平台支持 Go race detector
```

也可直接运行：

```powershell
go generate ./...
go test ./...
go vet ./...
go build ./cmd/...
```

## 阶段 0 工程选型

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
