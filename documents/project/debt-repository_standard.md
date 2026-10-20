# 存量技术债

按用户可见影响排序。Gallery 暂缓开发；本轮不修业务、UI 或扩展测试矩阵。以下是现有实现违反项和证据边界，不把推测当缺陷。

| 影响                     | 规则                      | 位置                                                                                                                                                                          | 事实及建议                                                                                                                                  |
| ------------------------ | ------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------- |
| 高：进程资源限制证据不稳 | T-02/T-04、C-09           | internal/toolrunner/integration_test.go，TestIntegrationOutputOverflowKillsRealProcessTree                                                                                    | 整理前 Execute 溢出收敛耗时 31.6 秒，未满足测试预期。不能据此宣称进程树总能及时强杀；恢复开发时在隔离 Windows 环境定位，保留原实现          |
| 高：备份时序不稳         | T-04、R-03                | internal/transport/httpapi/server_test.go，TestControlBackupEndpoints                                                                                                         | 整理前备份 Job 在 12 秒测试时限仍 running；不能据少量断言说恢复链已交付。先区分负载、调度和逻辑，再决定修复                                 |
| 高：核心日常路径不完整   | R-01/R-03/R-04、U-02/U-06 | web/src/manage/pages/scans.tsx、rules.tsx、internal/scanner/service.go                                                                                                        | Library/Source 无改名/停用/删除；规则发布、绑定、索引、内容确认分散，首次 index 后原媒体不可读。定义用户验收后再改产品契约                  |
| 高：内部概念直接展示     | U-03、WEB-07              | web/src/manage/layout.tsx、pages/governance.tsx、contract.ts，gallery/components/browser.tsx                                                                                  | ID、sourceKey、Attempt、协议/快照和长篇限制解释进入界面；改为任务语言及折叠诊断，当前只登记                                                 |
| 中：规则编辑测试偶发超时 | T-02/T-04、C-07           | web/src/manage/manage.test.tsx，分类扩展和任意 JSON payload 用例                                                                                                              | 摸底轮在默认 5 秒下两次超时；本轮整理前后均未复现。用例内单次等待达 10 秒而整体只有 5 秒，仍保留开放，不增加超时掩盖                        |
| 中：测试和 DOM 警告      | U-04/U-07、WEB-05、T-02   | web/tests/setup.ts、src/runtime/session.test.tsx、gallery/components/browser.tsx                                                                                              | act 环境警告持续；摘要 p 内 Spinner 间接生成 div，React 报 HTML 嵌套问题。组件测试绿不代表 DOM/可访问性验收                                 |
| 中：巨型职责文件         | C-07                      | internal/transport/httpapi/server.go、catalog/store.go、application/rules_lifecycle.go、jobs/store.go；web/src/manage/api.ts、rules/RuleStructuredFields.tsx、manage.test.tsx | 超过约 600 行的手写文件集中路由/SQL/状态机/表单职责；按事务和功能拆分，不能仅机械切段。生成的 Go/TS 不纳入此债                              |
| 中：调试接缝在生产类型   | C-04/C-06                 | internal/scanner/service.go 的 preReuseHook/SetPreReuseHook，catalog/backup 测试接缝                                                                                          | 某些接缝仅为确定性测试，正式装配不设值；需要逐项判断测试必要性，不能无依据删除。优先沿用既有 testhook build tag 模式                        |
| 中：重复工具适配         | C-08、GO-10               | tools/windows-portable 多个脚本的 Resolve-Executable/Invoke-Checked/清理函数；web/src/manage/api.ts                                                                           | 多处相似封装，但边界不完全相同；恢复维护后以现有行为对照合并。当前未冒险抽取                                                                |
| 中：手写协议映射         | G-03/F-01、GO-06、WEB-04  | internal/transport/httpapi；web/src/manage/api.ts、gallery/contracts.ts、auth/capabilities.ts                                                                                 | 服务端 handler 和前端局部类型/权限词表仍有手写副本；已生成公开类型，但未由契约生成完整服务端骨架。保留业务行为，后续逐项消除漂移            |
| 中：完整遍历成本         | C-07、GO-12               | internal/scanner/service.go discover/discoverResult                                                                                                                           | incremental 复用摘要仍遍历全根并积累 Works；不是已证明差分扫描。大树内存/I/O 要用固定输入测量，当前不重写                                   |
| 中：规则转换边界         | R-04、C-09                | internal/rules/legacy、reference/format-legacy_rules.md                                                                                                                       | 递归媒体、兄弟条件、部分稳定键/fallback 无法忠实转换；必须审阅 Unconverted，不把成功返回当兼容完成                                          |
| 中：外围范围待重审       | R-01/R-05                 | auth、backup、Share/审计/治理/PWA                                                                                                                                             | 核心用户路径未验收前已铺外围；暂停期间保留现有数据/授权行为，范围由用户决定，不擅自删除产品能力                                             |
| 低：存量自建设计层       | U-01、WEB-06              | web/src/design、manage/ui.tsx、gallery/app.css                                                                                                                                | 使用 React Aria，但仍有自建组件/布局与硬编码尺寸；本轮不迁设计体系，待与 Legacy 迁入方案一起定                                              |
| 低：声明多于发送         | G-02、C-05                | internal/contract/realtime/envelope.go、web/src/manage/contract.ts                                                                                                            | 部分事件只有词表无发送者；不能直接删除公开协议项，应在恢复开发/冻结时逐项核实                                                               |
| 低：旧构建/诊断流程      | G-05、T-05、W-05          | tools/testlab/cmd/web-e2e/main.go；tools/windows-portable/Test-WindowsHistoricalUpgrade.ps1                                                                                   | 个别旧工具仍把诊断临时输出放忽略目录、使用旧环境参数/历史构建流程。定义已外置，实际高成本工具本轮未运行；迁移脚本需独立验收                 |
| 低：小型二进制夹具       | G-06、GO-03               | tools/testlab/fixtures/synthetic 下合成 PNG                                                                                                                                   | 保留测试所需小型合成输入，未混进实验或真实数据。G-06 未明确列测试二进制例外，待用户决定是否改为运行时从文本生成；不为过目录检查删除有效输入 |
| 低：原有注释/说明漂移    | C-10、G-02                | Web contract.ts 的“配置文件”说明、若干阶段号/过程注释                                                                                                                         | 配置实际上只有 CLI，长篇事实副本可能漂移；暂停期间不动 UI，必要历史约束保留，其它随相关功能处理                                             |

## 逐节核查

| 规范节   | 当前结论                                                                                                    |
| -------- | ----------------------------------------------------------------------------------------------------------- |
| 2 总则   | 根规范同步；实验/产物移出；上述 PNG 例外和协议声明列债。没有把保留数据当缓存清理                            |
| 3 范围   | 暂停功能开发，核心路径和外围问题列债；未新做产品功能                                                        |
| 4 目录   | 根按 Go/Web 白名单，文档七类；shared 改 runtime；测试夹具进 testdata；独立生成工具 module 位于 tools        |
| 5 事实   | 产品版本取 Git，协议版本读 OpenAPI，SDK/依赖取各 module/package 声明。本机路径不入库；手写类型/词表副本列债 |
| 6 代码   | 未新加无证据防御或业务抽象；大文件、接缝、重复工具列债。Source/权限检查都有实际失败边界，不一概视为防御冗余 |
| 7 界面   | 只改 imports，没有改交互/视觉，未声称 U-08 验收；存量 U-01/U-02/U-03/WEB-07 问题列债                        |
| 8 测试   | 现有原生测试对比，删自建审计器相关测试；不以数量证明正确；真实场景未执行                                    |
| 9 文档   | 用途与命名重组；ADR 保留决策并补固定标题；日志原文外存，有效问题提炼；状态页约 10 KB 内                     |
| 10 协作  | 计划先行、禁止子代理、范围内更新；一次 Update/正常推送已获本轮授权                                          |
| Go 附则  | 契约/生成器/版本归位；产品模块依赖不升级；场景和工具仍有列明存量债                                          |
| Web 附则 | 双入口保留、原生构建/lint/测试；产物忽略；旧 UI 与本机驱动限制列债                                          |

## 本轮删除与保留

移出现行树的是根/脚本 Check、自建 Web 依赖审计器及其 12 个断言、全部 CI/模板策略和无有效职责的索引空壳；原文件在仓库外保留。未发现可在不改变协议或运行行为前提下安全删除的产品分支，未做业务死代码清扫或大规模去重。小型夹具与全部既有用户未提交输入均保留。
