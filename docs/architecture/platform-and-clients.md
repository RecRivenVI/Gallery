# 平台与客户端

## 平台分层

平台差异分成三层：

1. 领域、规则、查询和应用层只依赖平台中立类型；
2. `internal/ports` 定义最小文件系统、Watcher、磁盘和进程接口；
3. `internal/platform/*` 用 build constraint 提供具体 OS 实现。

当前适配面包括 AppDirs、路径比较、文件身份、独占锁、磁盘空间、进程组和工具进程。平台文件后缀与范围见[仓库结构](../development/repository-layout.md)。

## 支持状态

| 平台 | 当前状态 |
| --- | --- |
| Windows x64 | RC 前唯一主动开发、CI、浏览器和制品目标 |
| Linux、macOS、其它 Unix | 仓库保留部分实现或测试工具，不在当前验证矩阵 |
| 其它架构 | 未列为当前目标 |

跨平台可编译或存在 `*_other.go` 不能被描述为受支持。Windows x64 RC 完成后需要重新评估平台、存储和维护成本。

## `galleryd`

`galleryd` 是产品能力宿主。它不依赖桌面壳，运行时不需要 Node.js。启动参数、AppDirs、descriptor 和模式限制见[配置参考](../reference/configuration.md)。

## `galleryctl`

`galleryctl` 只导入公开 `api` 和 `version` package。当前命令为：

- `version`：输出 CLI 版本；
- `health`：调用 `GET /api/v1/health`。

它不是管理 API 的完整命令行覆盖，也不直连 SQLite。

## Web/PWA

`web/` 使用 React、TypeScript、React Router、TanStack Query、React Aria Components、Vite 与 Playwright。构建产生两个同源入口：

- `/`：面向浏览的 Gallery，可注册 Service Worker 并安装为 PWA；
- `/manage`：面向管理与诊断，使用独立 `manage.html`，不进入 PWA manifest。

两端共享 Session、错误、实时、主题和设计 primitive，但路由、密度和信息架构不同。服务端按 `/manage` 前缀选择外壳，深链刷新不会落到用户端。

PWA 只缓存版本化静态资产。API、WebSocket、媒体、授权和 publication 响应不进入运行时缓存；管理端导航也不由用户端 fallback 接管。

## 桌面壳

正式产品树没有桌面壳实现。`experiments/testbench/cleanroom-lab/deploy/wails-shell` 是历史技术实验，不参与根 module、CI 或发行。未来壳只能发现/启动 `galleryd`、承载 WebView 和处理 OS 集成，不得拥有数据库、规则、授权或查询语义。

## Windows 制品

`scripts/Build-WindowsPortable.ps1` 构建 `galleryd.exe`、`galleryctl.exe`、内嵌 Web、manifest、checksum 和 CycloneDX SBOM，可选 Authenticode。当前 GitHub 工作流明确产出未签名预发行制品；正式签名、安装和更新仍是未来门禁。

## 主要实现位置

- `internal/ports/`
- `internal/platform/`
- `cmd/galleryd/`
- `cmd/galleryctl/`
- `web/`
- `internal/webapp/`
- `scripts/Build-WindowsPortable.ps1`
