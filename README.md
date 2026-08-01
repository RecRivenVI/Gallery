# Gallery

Gallery（画廊）是一个以 Go 后端为基座的本地优先个人媒体目录系统。`galleryd` 从用户显式登记的只读 `Source` 建立可重建 `Catalog`，把账户、授权、Binding 和 Overlay 等不可重建事实保存在独立控制库中，并通过 HTTP API、WebSocket、`galleryctl` 和可选 Web/PWA 提供访问入口。

当前仓库包含：

- Go 服务 `galleryd` 和最小 CLI `galleryctl`；
- Library、只读 Source、规则、扫描、任务、Catalog publication、查询和媒体读取；
- Personal/LAN 认证、Session、Token、Grant、Share、审计、备份与恢复；
- 用户端 `/` 与管理端 `/manage/` 两个 Web 入口；
- Windows x64 检查、测试工具和便携制品脚本。

项目处于 **pre-alpha**。Windows x64 是 RC 前唯一主动开发、测试和发行目标；当前没有正式 RC、稳定 v1 API、已签名发行版、安装器或自动更新器。源码中存在实现或测试入口不等于相应发布门禁已经通过。

## 构建

### 后端

后端要求 Go 1.26.5。在仓库根目录编译正式命令：

```powershell
go build -o galleryd.exe ./cmd/galleryd
go build -o galleryctl.exe ./cmd/galleryctl
```

该方式使用仓库中现有的内嵌 Web 资产。只需要确认全部正式命令可编译时，可以执行：

```powershell
go build ./cmd/...
```

### 后端与 Web

修改 Web 后，先使用 Node.js 22.22.0 或更高版本及 npm 10.9.8 生成生产资产：

```powershell
cd web
npm ci
npm run build
```

构建结果写入 `internal/webapp/dist`。返回仓库根目录后重新构建 `galleryd`，新的 Web 资产才会进入可执行文件。

### Windows x64 便携包

便携构建脚本要求完整 SemVer、干净工作树，以及可用的 Git、Node.js、npm 和 `cyclonedx-gomod`；它会生成 ZIP、manifest、校验和与 SBOM：

```powershell
./scripts/Build-WindowsPortable.ps1 -Version '0.2.0-pre-alpha'
```

默认输出目录为 `dist/release/`。正式制品还必须满足签名、升级、恢复和候选门禁；脚本成功不等于 RC 完成。

## 开发流程

1. 从 [工程文档总览](docs/README.md)定位当前主题的事实源，并检查工作树中的既有改动。
2. 只在当前任务范围内修改实现；Windows x64 RC 前不并行推进其它系统或架构。
3. 契约和生成物从其源文件修改：OpenAPI 以 `internal/contract/api/openapi.yaml` 为准，生成关系见[生成文件说明](docs/development/generated-files.md)。
4. 按变更范围执行检查：后端使用 `./Check.ps1 -BackendOnly`，包含 Web 时使用 `./Check.ps1`；高成本浏览器、制品、性能和真实 Source 门禁按任务单独执行。
5. 同步更新承担唯一事实源职责的文档；技术方向变化使用 ADR，未来工作进入路线图，验证结果进入验证记录。
6. 提交前检查差异、生成物、文档链接、UTF-8/LF 和实际验证结果。完整规则见 [AGENTS.md](AGENTS.md) 与[贡献指南](CONTRIBUTING.md)。

## 核心功能状态

复选框表示当前开发状态：已勾选表示源码中已有对应完整功能入口，未勾选表示仍待开发或尚未达到候选完成条件；它不代表本轮已运行测试，也不等同于 RC 门禁通过。

- [x] Library、只读 Source 登记及 AppDirs/Source 重叠保护
- [x] RulePackage、参数、版本生命周期和 SourceRuleBinding
- [x] 扫描、内容确认、持久 Job/Attempt 和 Catalog publication
- [x] 作品、创作者与媒体的查询、搜索、排序、Total 和 keyset cursor
- [x] 已确认媒体读取、单区间 Range、ETag 和 JPEG 缩略图派生
- [x] Personal/LAN 认证、Session、Token、Grant、Share 和审计
- [x] Overlay、Binding issue、结构决定、孤儿治理和人工解绑
- [x] `control.db` 备份、恢复验证、启动期恢复和 Catalog 维护
- [x] 用户端 Web/PWA 与管理端 Web 双入口
- [x] Windows x64 便携包、manifest、校验和与 SBOM 构建入口
- [ ] 冻结 Windows x64 RC 所需的 OpenAPI、实时协议、Schema 和 `PRE_FREEZE` 预算
- [ ] 对同一候选完成 Correctness、Security、Performance、恢复和 degradation 门禁
- [ ] 完成真实浏览器、可访问性、升级、正式签名和 Windows x64 制品验收
- [ ] 完成 Pixiv 全量只读扫描、哈希、publication、查询和全树零写入最终验收

详细阶段、非目标和完成条件见[开发路线](docs/development/roadmap.md)。

## 文档导航

- [工程文档总览](docs/README.md)
- [产品定义与不变量](docs/reference/product-definition.md)
- [系统架构](docs/architecture/system-overview.md)
- [配置参考](docs/reference/configuration.md)
- [架构决策记录](docs/decisions/README.md)
- [测试与发布门禁](docs/development/testing-and-release-gates.md)
- [Windows x64 运维](docs/operations/README.md)
- [安全政策](SECURITY.md)

HTTP API 的完整资源面以 [`internal/contract/api/openapi.yaml`](internal/contract/api/openapi.yaml) 为准，普通 Markdown 不维护重复的路由和 DTO 清单。

## 许可

Gallery 依据 [GNU AGPL v3](LICENSE) 发行。仓库直接包含的第三方材料见 [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)。
