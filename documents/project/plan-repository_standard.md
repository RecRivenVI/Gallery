# 仓库规范落实计划

## 目标

基于现有工作树落实仓库规范 1.0.0；Gallery 暂缓功能开发，等待 Gallery-Legacy 完工后再决定迁入代码。完成一次 `Update` 检查点并正常推送 main，不创建 tag，不改历史。

## 范围

当前基线 `9d4c784`，受跟踪文件无修改；两个未跟踪长路径夹具随 experiments 整体外移，不能丢弃。仅结构、事实来源、文档、工具入口与生成承载调整；既有业务/UI 问题登记 debt。

最可能的三处风险：

1. 契约、版本包、工具和文档移动导致 import、embed、生成器或相对引用失效。先记录相同构建/测试命令，再对比。
2. 把小型合成测试输入误当实验结果移出。保留生产测试所需 testdata；实验外移前核实只有独立 module，并保留文件清单。
3. 新规则替换丢掉产品不变量。旧规则逐节映射到 X- 补充、产品范围、架构、运维或工作流，原文保存仓库外。

下列目录规则覆盖全部文件；具体改名如下表，其余产品源码、资源、相邻单元测试、go.mod/go.sum、LICENSE、编辑器配置保留。生成物只由原生成器重建。

| 类别 | 当前路径                                                                    | 去向                                                                                                                 |
| ---- | --------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------- |
| 替换 | AGENTS.md                                                                   | 原样复制规范 assembled/AGENTS-go_web.md；原规则外存 OriginalRules                                                    |
| 新增 | documents/AGENTS.md                                                         | 项目规范及旧规则中仍有效的 X- 补充                                                                                   |
| 重写 | README.md                                                                   | 简介、平台、快速开始、入口、许可                                                                                     |
| 保留 | cmd/、internal/（以下例外除外）、api/、web/（以下例外除外）、tools/testlab/ | 生产和复用测试能力保留；修引用，不改行为                                                                             |
| 移动 | version/*                                                                   | internal/version/*；更新 imports，产品版本由 Git 注入                                                                |
| 移动 | internal/contract/api/*                                                     | api/*；Go 契约包装与生成器同目录                                                                                     |
| 移动 | tests/fixtures/*                                                            | internal/scanner/testdata/*；bootstrap 和 testlab 复用明确路径                                                       |
| 改名 | web/src/shared/*                                                            | web/src/runtime/*；更新全部导入与注释引用                                                                            |
| 移动 | scripts/（除 Check.ps1）                                                    | tools/windows-portable/*；按新深度修路径                                                                             |
| 移动 | packaging/*                                                                 | tools/windows-portable/templates/*                                                                                   |
| 移动 | .github/windows-historical-baselines.json                                   | tools/windows-portable/historical-baselines.json                                                                     |
| 替换 | Check.ps1、scripts/Check.ps1                                                | tools/build/Build.ps1 纯构建入口；原检查器移至仓库外 RemovedTools，不保留门禁                                        |
| 移出 | .github/ 其余全部                                                           | Artifacts/Gallery/RepositoryStandard-20261021/RemovedTools/github/；删除现行 CI/模板/审计策略                        |
| 移出 | web/scripts/check-audit.mjs、check-audit.test.mjs                           | 同上 RemovedTools/web/；删除 npm 审计门禁入口                                                                        |
| 移出 | experiments/（全部含未跟踪文件）                                            | Artifacts/Gallery/Experiments/；不执行实验                                                                           |
| 移出 | internal/webapp/dist/、web/public/icons/、规则预编译校验器                  | 本轮旧产物外存 GeneratedBefore；输出目录忽略，构建按 Web → Go                                                        |
| 移出 | docs/validation/*、无引用的目录索引、tests/README.md、第三方目录内说明      | 本轮外存 OriginalDocuments；当前结论提炼，历史不入库                                                                 |
| 转写 | CONTRIBUTING.md、SECURITY.md、THIRD_PARTY_NOTICES.md、CLAUDE.md             | documents/development/workflow-contribution.md、design/security-deployment.md、NOTICE、documents/AGENTS.md；原文外存 |

### 文档改名

| 当前 docs/ 内路径                                         | documents/ 新路径                                                              |
| --------------------------------------------------------- | ------------------------------------------------------------------------------ |
| reference/product-definition.md                           | project/overview-gallery.md                                                    |
| reference/glossary.md                                     | reference/glossary-domain.md                                                   |
| reference/configuration.md                                | reference/configuration-service.md                                             |
| architecture/system-overview.md                           | design/architecture-system.md                                                  |
| architecture/domain-model-and-data-ownership.md           | design/concept-data_ownership.md                                               |
| architecture/api-realtime-and-security.md                 | design/security-api.md                                                         |
| architecture/filesystem-and-media.md                      | design/mechanism-media.md                                                      |
| architecture/platform-and-clients.md                      | design/architecture-clients.md                                                 |
| architecture/query-search-and-sorting.md                  | design/mechanism-query.md                                                      |
| architecture/rules-engine.md                              | design/mechanism-rules.md                                                      |
| architecture/scanning-catalog-and-jobs.md                 | design/flow-scanning.md                                                        |
| design/web/README.md                                      | design/overview-web.md                                                         |
| design/web/administration-experience.md                   | design/flow-administration.md                                                  |
| design/web/design-system.md                               | design/principle-web.md                                                        |
| design/web/gallery-experience.md                          | design/flow-browsing.md                                                        |
| design/web/motion-system.md                               | design/mechanism-motion.md                                                     |
| development/repository-layout.md                          | development/workflow-repository.md                                             |
| development/generated-files.md                            | development/build-generated.md                                                 |
| development/documentation-guide.md                        | development/workflow-documents.md                                              |
| development/local-environment-template.md                 | development/setup-local.md                                                     |
| development/roadmap.md                                    | project/roadmap-restart.md                                                     |
| development/testing-and-release-gates.md                  | development/testing-gallery.md；去除自建门禁/CI，场景提炼至 validations        |
| operations/windows-x64-runtime.md                         | usage/guide-start.md（用户写法）与 development/workflow-runtime.md（开发命令） |
| operations/backup-and-recovery.md                         | usage/guide-backup.md                                                          |
| decisions/0001～0011-*.md                                 | design/adr-1～11-*.md；topic 用下划线，保留决策正文并补固定小节                |
| decisions/README.md                                       | design/overview-decisions.md                                                   |
| 其余 README.md、images/postmortems 索引与 validation 记录 | OriginalDocuments 外存；无内容空壳不留仓                                       |

代码旁的 legacy coverage、testlab/夹具/安全说明、Web design、minimatch 适配说明转入 reference 或 development 的类型文档，源码仅保必要注释/链接。可复用实际场景写 validations/<type>-<topic>/validation.md；数据与日志不入库。新增 status、backlog、debt、依赖来源文档。

### 旧规则去向

| 旧规则                                                               | 当前责任                                               |
| -------------------------------------------------------------------- | ------------------------------------------------------ |
| 语言、最小范围、Git、LF、成熟度、平台隔离                            | 新共用规范；特有状态/Windows 范围进项目规范和 overview |
| Source 零写入/互斥、两库、身份、完整 publication、后端权威、受限规则 | documents/AGENTS.md X- 与相关 design 文档              |
| 历史 migration 不改、依赖/第三方材料                                 | 新规范及 NOTICE/依赖参考                               |
| 私密数据、Personal/LAN、真实 Source 授权                             | X- 与 security-deployment/场景定义                     |
| 生成入口、Check/CI/门禁、旧文档布局、提交格式                        | 按新规范替换；原文外存，不延续检查器或旧提交标题       |

## 步骤

1. 完成：盘点、原规则备存、创建并读回 Goal。
2. 完成：取得固定工具链，记录整理前 Web/Go 构建与现有测试。
3. 完成：根规则逐字同步，完成项目规范、路径迁移、纯构建工具和生成关系。
4. 完成：文档七类重组，提炼历史结论，登记逐节技术债和 validations。
5. 完成：同命令构建/测试对比与格式化、自查；最终检查点按已授权的一次 Update 正常推送 main。

## 结果

目录和文档迁移已完成，新增独立 tools/api-generation module 锁定原生成器，避免升级产品依赖；Go/TS 接口源仍保留，构建 bundle 与预编译输出忽略。本轮未改产品业务、Schema 或 UI 交互。构建对比一致，Web 业务用例一致，Go 既有时序失败有差异且重跑后通过，均未修业务。检查点以 Git 当前分支记录为准；原始输出在仓库外 RepositoryStandard-20261021。
