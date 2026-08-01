# AGENTS.md

## 工作语言与起点

- 对话、计划、报告和提交信息使用中文；代码标识、协议字段和必要术语保留英文。
- 每次开始工作前检查当前目录、Git 状态、实际文件和适用的 `AGENTS.md`，不得从旧对话、旧报告或文件名推断现状。
- 只做用户请求范围内的最小必要修改。调查不自动授权实现，文档任务不顺手改变产品行为。
- 当前仓库可能包含未提交改动；先识别并保留用户变更，不得用破坏性 Git 命令清理工作树。

## 项目身份与成熟度

- 英文公开名为 **Gallery**，中文公开名为**画廊**，代码与服务代号为 `gallery`。
- 后端命令为 `galleryd`，CLI 为 `galleryctl`。
- Gallery 是独立净室产品，不以其它产品的数据库、配置、API、目录或行为作为兼容目标。
- 项目处于 pre-alpha。源码中存在某项实现、测试或跨平台文件，不等于该能力已通过门禁、已稳定或已发行。
- 描述状态时区分：源码已实现、存在自动化覆盖、本轮已执行验证、发布门禁已通过。没有当轮证据时不得提升结论。

## 不可违反的产品边界

1. `Source` 永久只读。产品代码不得在 Source 内创建、修改、重命名、移动或删除任何内容。
2. AppDirs、数据库、日志、缓存、临时文件和派生资源不得与 Source 重叠。
3. 用户事实存入 `control.db`；可重建投影存入 `catalog.db`。重扫不得覆盖账户、授权、Binding 或 Overlay。
4. 路径不是永久业务身份。`CanonicalMedia`、`ContentBlob` 与 `FileLocation` 必须保持不同语义。
5. Catalog 只能发布完整 revision；查询不得观察半次扫描。
6. 来源差异通过规则表达，不在通用业务代码或前端按 Provider 名称分支。
7. 排序、过滤、分页、授权和错误语义由后端契约决定；客户端不得建立第二套规则。
8. 规则不得执行任意代码、读取未显式提供的文件或访问网络；外部 JSON Schema 引用必须拒绝。

## 源码与依赖边界

- `cmd/` 保持薄入口；业务实现位于 `internal/`。
- `api/` 是从 OpenAPI 生成的公开 Go DTO 与客户端，`version/` 提供共享版本标识；不要新增无明确兼容责任的公开 package。
- 平台中立核心不得直接依赖 Win32。文件身份、路径比较、锁、进程、磁盘和 AppDirs 差异经 `internal/platform/*` 与 `internal/ports` 隔离。
- 不修改已经生效的 migration；Schema 变化只能新增连续 migration，并同步升级与降级拒绝验证。
- 新依赖必须有明确用途、固定版本和可接受许可证；直接复制的第三方材料同步维护 `THIRD_PARTY_NOTICES.md`。

## 当前平台范围

- Windows x64（`windows/amd64`）是 RC 前唯一主动开发、运行验证和发行目标。
- Windows x64 RC 前，不新增、同步开发或运行 Linux、macOS、其它 `GOOS` 或其它 `GOARCH` 的实现与测试。
- 既有非 Windows 文件保留为未验证代码。机械性签名或目录调整不得被描述为平台能力推进。
- 文件名与 build constraint 必须一致：使用 `*_windows.go`、`*_darwin.go`、`*_unix.go`、`*_other.go` 等明确后缀，不在通用文件里用 `runtime.GOOS` 选择行为。

## 配置、安全与隐私

- 不读取、提交或输出 secret、Token、Cookie、私密 metadata、真实媒体内容或完整个人媒体路径。
- 示例使用合成数据、占位路径和临时 AppDirs；真实 Source 验证必须由用户明确授权并带零写入 guard。
- Personal 模式只能监听 loopback；LAN 模式只面向显式私网地址，并要求先在 loopback 初始化 Owner。
- 日志、错误和文档不得泄露 Source 绝对路径或一次性凭据。

## 生成物与唯一事实源

- OpenAPI 唯一源是 `internal/contract/api/openapi.yaml`；`api/openapi.gen.go` 由 `go generate ./...` 生成。
- Web API 类型由 `web` 中的 `npm run generate:api` 生成；完整 Web 生成入口为 `npm run generate`。
- `internal/webapp/dist` 是 `npm run build` 的生产产物并嵌入 `galleryd`。不要手工编辑生成结果。
- 变更生成源后必须按现有脚本更新生成物，并核对没有意外漂移。

## 验证要求

- 先选择与变更范围相符的最小检查，再决定是否运行完整门禁；不得用历史结果冒充当轮验证。
- Windows x64 后端入口为 `./Check.ps1 -BackendOnly`，完整入口为 `./Check.ps1`，race 入口为 `./Check.ps1 -Race`。
- `Check.ps1` 固定 Go 1.26.5，并检查 LF、生成一致性、格式、vet、Go 测试与构建；默认模式还检查 Web 依赖、类型、lint、格式、单元测试和生产构建。
- 浏览器、便携包、历史升级和真实 Source 属于独立高成本门禁；没有明确任务范围时不自动执行。
- 测试只使用合成或复制到临时目录的输入，不连接现有生产实例，不改变真实 Source。
- 报告必须列明实际执行的命令、结果、跳过项和适用边界。

## 文本与文档

- 仓库文本使用 UTF-8、LF 和末尾换行；不得引入 CRLF 或孤立 CR。`.gitattributes` 是 Git 换行事实源。
- 文档写当前事实时必须由源码、配置、契约或当轮验证支持；未来工作进入路线图，历史过程进入验证或归档，不混入当前说明。
- 根 `README.md` 只承担项目介绍、能力边界、使用入口和导航；本文件只维护长期 Agent 规则，不记录进度或任务日志。
- 同一事实只保留一个权威文档，其它位置写摘要并链接。API 细节留在 OpenAPI 与 Go package 文档中。
- 文档结构、文件命名和写作约定见 `docs/development/documentation-guide.md`。

## Git 与交付

- 未经用户明确要求，不提交、不推送、不创建 PR、不改分支历史。
- 不使用 `git reset --hard`、`git checkout --` 或等价破坏性操作处理不属于当前任务的改动。
- 提交前检查 diff、生成物、文档链接、LF 和实际验证结果；提交必须是单一可解释的逻辑结果。
- 默认提交标题采用 `type(scope): 中文摘要`，正文解释原因、边界和验证；不要把临时工作记录堆入提交信息。

## 入口

- 项目介绍：`README.md`
- 工程文档：`docs/README.md`
- 产品边界：`docs/reference/product-definition.md`
- 仓库布局：`docs/development/repository-layout.md`
- 开发路线：`docs/development/roadmap.md`
- 测试门禁：`docs/development/testing-and-release-gates.md`
