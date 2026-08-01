# 贡献指南

Gallery 处于 pre-alpha，当前只接受与 Windows x64 RC 收口、缺陷修复、文档和既有工程边界直接相关的变更。自动化 Agent 除本指南外还必须遵守 [AGENTS.md](AGENTS.md)。

## 开始之前

1. 阅读 [README.md](README.md) 和[产品定义](docs/reference/product-definition.md)。
2. 从[工程文档总览](docs/README.md)找到对应主题的权威文档。
3. 检查当前分支与未提交改动，避免覆盖其他工作。
4. 对较大的架构或契约变化先提交 Issue 或 ADR 讨论。

## 开发环境

- Windows x64；Windows x64 RC 前不主动推进其它系统或架构。
- Go 1.26.5；仓库检查脚本会拒绝其它 Go 版本。
- PowerShell 7，用于仓库自带的 Windows 检查与发行脚本。
- 修改 `web/` 时需要 Node.js 22.22.0 或更高版本及 npm 10.9.8；CI 当前使用 Node.js 22.23.1。
- Git 检出必须遵守 `.gitattributes`，所有文本使用 UTF-8 与 LF。

## 代码边界

- `Source` 永久只读，AppDirs 与 Source 必须互不重叠。
- 不修改历史 migration；新增 Schema 通过新的连续 migration 表达。
- 来源差异进入规则，不在通用代码中按 Provider 名称硬编码。
- 平台行为进入 `internal/platform/*`，业务核心保持平台中立。
- HTTP、WebSocket 和公开 DTO 先修改 OpenAPI 或对应契约源，再更新生成物。
- 不提交真实媒体、私密路径、凭据、运行数据库、日志或大型测试结果。

## 生成与检查

常用入口如下：

```powershell
./Check.ps1 -BackendOnly
./Check.ps1
./Check.ps1 -Race
```

完整检查会执行依赖安装和测试，运行前先阅读[测试与发布门禁](docs/development/testing-and-release-gates.md)。仅修改 OpenAPI 时，生成入口是：

```powershell
go generate ./...
cd web
npm run generate:api
```

Web 的脚本与产物关系见[生成文件说明](docs/development/generated-files.md)。

## Issue 与安全问题

- Bug 报告应包含最小复现、实际行为、预期行为、环境和相关模块。
- 功能建议说明通用场景、范围和非目标，不以单一 Provider 的特例替代规则设计。
- 安全问题不要公开提交，按 [SECURITY.md](SECURITY.md) 的私密渠道报告。

## Pull Request

- 一个 PR 只完成一个可独立审查和回退的逻辑结果。
- 描述契约、migration、平台、生成物、文档和依赖影响。
- 只列实际执行过的验证；未运行项明确写出，不引用旧结果替代。
- 使用仓库 PR 模板，并确保没有 secret、真实路径或媒体内容。

直接包含第三方源码或资产时保留原许可证，并同步 [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)。
