# 测试与发布门禁

## 原则

测试入口存在不等于测试已通过，历史结果也不自动适用于当前工作树。任何验证记录必须包含 commit 或工作树状态、工具版本、目标平台、输入边界、实际命令、退出码和跳过项。

本文件定义验证体系；有日期的具体审查结果进入 [`docs/validation/`](../validation/README.md)。

## 当前自动化入口

### Go 后端

```powershell
./Check.ps1 -BackendOnly
```

脚本固定 Go 1.26.5，正式 package 集合为 `./cmd/...`、`./internal/...`、`./api/...`、`./version/...` 和 `./tools/...`。它检查：

- tracked 文件 LF；
- Windows 发行脚本语法；
- `go mod tidy -diff`；
- `go generate ./...` 后生成物无漂移；
- `gofmt`、`go vet`；
- `CGO_ENABLED=0 go test` 与 `go build ./cmd/...`。

`experiments/testbench` 的独立 module 和 `web/node_modules` 不进入该 package 集合。

### 完整仓库检查

```powershell
./Check.ps1
```

在后端检查之前或期间还会在 `web/` 执行 `npm ci`、依赖审计、TypeScript、ESLint、Prettier、Vitest 和生产构建，并核对 Web API 类型与嵌入资产没有漂移。

### Race

```powershell
./Check.ps1 -Race
```

该入口增加 `go test -race`。是否可在当前 Windows 工具链执行应以当轮实际结果为准，不能引用旧的 Linux/WSL 结论替代。

### Web 浏览器

`web/package.json` 定义以下层次：

- `npm test`：Vitest 组件和逻辑测试；
- `npm run test:smoke`：Chromium 与 Firefox mock smoke；
- `npm run test:real:bootstrap`：隔离 `galleryd` 的真实后端 Playwright 链；
- `npm run test:e2e`：按 Playwright 配置运行完整浏览器集合。

真实后端 E2E 必须使用临时 AppDirs、合成 Source 和隔离端口，不得连接现有实例。trace 在真实模式下关闭，避免记录 Cookie。

### Windows 制品

- `scripts/Build-WindowsPortable.ps1`：构建 `windows/amd64` 便携 ZIP、manifest、checksum 与 SBOM，可选 Authenticode；
- `scripts/Test-WindowsPortable.ps1`：检查制品结构与启动 smoke；
- `scripts/Test-WindowsPortableUpgrade.ps1`：同源便携升级、恢复和失败窗口；
- `scripts/Test-WindowsHistoricalUpgrade.ps1`：从 manifest 指定的历史 control schema 前向迁移并验证降级拒绝。

这些脚本会创建和清理隔离临时目录，正式运行前必须检查参数与目标路径。

## CI

仓库当前只有 Windows 工作流：

| 工作流 | 触发 | 主要范围 |
| --- | --- | --- |
| `windows-ci.yml` | push、PR | 后端检查与 `govulncheck` |
| `windows-web.yml` | Web/后端相关变更、手动 | Web 检查、双浏览器 smoke、真实后端 E2E |
| `windows-portable.yml` | 手动 | 完整门禁、历史升级、未签名便携制品与来源证明 |

没有 Linux、macOS 或其它架构的当前 CI 门禁。

## 证据等级

| 等级 | 含义 | 可支持的结论 |
| --- | --- | --- |
| S0 静态审查 | 阅读源码、配置、契约与生成关系 | 实现和入口存在，不能证明运行正确 |
| S1 单元/契约 | package 或组件自动化 | 局部语义受保护 |
| S2 集成 | 两库、HTTP、进程或浏览器的隔离组合 | 指定组合在合成环境成立 |
| S3 规模/退化 | 固定语料、资源预算和环境的重复测量 | 仅对记录环境与分布成立 |
| S4 真实 Source/设备 | 显式授权的真实只读根、硬件或网络 | 只对记录范围成立 |
| S5 制品 | 精确候选 ZIP、签名、升级与恢复 | 对该候选制品成立 |

高等级证据不能省略低等级正确性，局部真实设备结果也不能外推到未测平台。

## RC 门禁

### Correctness

- Source 与所有 AppDirs 零重叠，真实或复制输入有前后 guard；
- migration 只前进且历史 checksum 不变；
- Job/Attempt、取消、重试、进程中断和 publication 对账终态可解释；
- Catalog staging、发布、旧快照租约、Overlay 投影与 GC 不交叉 revision；
- OpenAPI、路由、生成客户端、错误词表与 capability 一致；
- 媒体确认、Range、ETag、内容变化和 DerivedAsset 输入保持同一 publication 语义。

### Security

- Personal/LAN 监听、Owner 初始化、CSRF、Origin/Host 和凭据生命周期有效；
- Session、Token、Grant、Share、HTTP 与 WebSocket 吊销一致；
- 授权按资源 scope 裁剪，不通过 404/空结果泄露身份；
- 规则、JSON Schema、媒体和外部工具有输入、时间、内存、输出与进程树边界；
- 日志、错误、制品和浏览器诊断不包含 secret 或绝对媒体路径。

### Performance 与 degradation

- Reference 语料的形状、来源数、关系数、publication 与查询 revision 被报告封印；
- warm、cold-process、并发、维护和失败恢复分开报告；
- P95/P99、吞吐、内存、数据库与 WAL 增长有候选预算；
- 超预算时返回有界 `lower_bound`、分页或结构化失败，不无界阻塞；
- HDD、低磁盘、文件锁、权限和取消等退化场景有明确结果。

### Web 与可访问性

- 用户端和管理端全部当前路由在真实 `galleryd` 上可达；
- 网络断开、服务重启、sequence gap、过期 cursor 和旧请求迟到能恢复；
- 键盘、焦点、减少动效、文本间距、强制颜色、400% 等效重排和关键交互状态通过；
- 真实设备与人工辅助技术缺口不得由桌面 viewport 模拟冒充关闭；
- Service Worker 不缓存 API、WebSocket、媒体或授权响应。

### Windows 发行

- 精确候选从干净提交构建，版本、VCS、manifest、checksum、SBOM 和来源证明一致；
- Authenticode 满足正式发行要求；未签名包只能标为测试制品；
- control schema 支持范围有真实历史二进制升级与降级拒绝；
- 备份、恢复、文件占用、强杀窗口和失败回滚对同一候选通过；
- 安装、升级、卸载或便携使用说明与实际制品一致。

### 最终真实 Source

只有上述候选完成后，才执行 Pixiv 全量只读扫描、必要哈希、publication、查询、媒体与前后全树 guard。真实 Source 不作为开发中的常规可变夹具。

## 本轮状态

2026-08-01 的文档重写只执行静态调查和文档完整性审查，不运行上述产品测试、构建或发行门禁。当前门禁结果必须在后续候选验证中重新建立。
