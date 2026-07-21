# 贡献指南

感谢你对 Gallery（画廊）感兴趣。本指南面向人类贡献者；自动化 Agent 除本指南外还必须完整遵守 [AGENTS.md](AGENTS.md)。

## 项目当前状态

- Gallery 仍处于 **pre-alpha**：后端阶段 0～4 主线已完成代码与合成正确性（Correctness）验证，但尚无图形界面、安装包或正式发行版本。
- 当前主要开发目标是阶段 4 的正式压力测试与 API 接口冻结，随后进入阶段 5（账户、安全与多客户端）。
- 尚无稳定的数据库表结构或 API 兼容性承诺；接口和 Schema 仍可能在冻结前调整。

详见 [README.md](README.md) 和 [PROJECT_STATUS.md](PROJECT_STATUS.md) 的总体进度表。

## 开始之前

- 阅读 [README.md](README.md) 了解项目定位。
- 阅读 [PROJECT_STATUS.md](PROJECT_STATUS.md) 了解当前阶段与已知缺口。
- 从 [Documents/README.md](Documents/README.md) 进入权威工程文档（规范、ADR、实施计划、验证记录）。
- 自动化 Agent 必须额外遵守 [AGENTS.md](AGENTS.md)。

## 提交 Issue

- Bug 报告必须提供可复现步骤、涉及模块和实际/预期行为差异。
- 功能建议应说明通用使用场景，而不是针对单一用户的定制需求。
- 不接受针对单一 Provider 或平台的核心业务硬编码；差异应通过规则系统表达。
- Gallery 是净室实现，不以任何旧 Gallery 的数据库、配置、API 或目录结构为兼容或迁移目标。
- 安全问题请勿通过公开 Issue 报告，见 [SECURITY.md](SECURITY.md)。

## 开发环境

- Go 1.26 系列（与 [go.mod](go.mod) 一致）；本地按你的操作系统正常安装 Go 工具链即可，不需要复用仓库内部 Agent 使用的固定路径配置。
- PowerShell（跨平台 `pwsh`）用于运行仓库检查脚本。
- Git，并了解本仓库的提交信息规范（见下文）。
- 修改 OpenAPI 定义后必须运行 `go generate ./...` 同步生成的客户端代码。

## 代码与架构要求

以下边界摘自 [AGENTS.md](AGENTS.md) 的产品不变量，PR 审查会据此把关：

- Source（媒体来源）永久只读，不改名、不移动、不删除、不写回原始媒体或 metadata。
- 不修改已生效的历史 migration；新变更通过新增 migration 表达。
- 平台相关能力（文件身份、Watcher、路径、进程、凭据等）必须经 `internal/platform/*` 与 `internal/ports` 适配层，不得直接写入领域或应用层。
- 规则系统不得执行任意代码；CEL 表达式仅限受限布尔条件、集合谓词和简单值选择。
- API 拥有协议语义：排序、过滤、分页、授权由后端决定，客户端不得重排服务端列表或直连数据库。

## 依赖与第三方材料

- 新增依赖前检查其许可证与 AGPL-3.0-only 的兼容性；避免引入未知许可证、来源不明或强 copyleft 冲突的依赖。
- 直接复制或改编第三方源码、字体、图片等资产时，必须保留原始版权与许可证声明，并在 [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md) 登记来源；不得将第三方文件重新声明为项目原创的 AGPL 材料。
- `go.mod`/`package.json` 中声明的依赖关系本身不等于仓库复制了其源码，两者在合规处理上不同，不要混为一谈。
- 安全相关的依赖升级必须同步对应的 manifest 与 lockfile，并执行该模块已有的构建与测试门禁。

## 测试

- 常规检查使用仓库根目录的 `Check.ps1`（Windows/跨平台 `pwsh`），会执行 `go mod tidy -diff`、OpenAPI 生成一致性、`gofmt`、`go vet`、测试与构建。
- Race 检测（`-race`）需要在 Linux 环境（原生 Linux 或 WSL2）执行；Windows 原生 Go race runtime 在本项目环境下有已知限制。
- 新增功能或修复 Bug 必须包含直接测试；migration、OpenAPI 变更需要对应的契约/集成测试覆盖。

## Pull Request

- 一个 PR 对应一个可独立解释、审查和撤销的逻辑结果；不相关的重构、格式化或多个独立能力请拆分为不同 PR。
- 请在描述中说明契约、migration、测试和文档影响，参考 [PR 模板](.github/PULL_REQUEST_TEMPLATE.md)。
- 涉及较大设计变更（新增子系统、改变已接受的技术方向）建议先开 Issue 讨论，再提交实现。
- 提交信息请遵循 Conventional Commit 风格的 `type(scope): 中文标题` 结构；如需了解本仓库对提交粒度和格式的完整强制规范，参见 [AGENTS.md](AGENTS.md) 的“Git Commit Message 规范”一节。
