# Gallery 工程文档

`docs/` 保存长期维护的产品、架构、设计、开发、运维、决策与验证文档。当前事实必须由源码、配置、契约或可复核的当轮证据支持；计划和历史记录不得混入当前说明。

## 阅读路径

- 初次了解项目：从[产品定义](reference/product-definition.md)和[系统架构](architecture/system-overview.md)开始。
- 开发或审查代码：阅读[仓库布局](development/repository-layout.md)、[开发路线](development/roadmap.md)和[测试门禁](development/testing-and-release-gates.md)。
- 修改技术方向：先查[架构决策记录](decisions/README.md)。
- 处理 Web：阅读[Web 设计索引](design/web/README.md)及代码内的设计系统说明。
- 启动、备份或恢复：进入[运维文档](operations/README.md)。
- 解释验证结论：先读[验证说明](validation/README.md)，确认日期、输入和适用边界。

## 目录职责

| 目录 | 职责 |
| --- | --- |
| [`reference/`](reference/README.md) | 稳定术语、产品定义和配置参考 |
| [`architecture/`](architecture/README.md) | 当前实现的组件边界、数据流与关键机制 |
| [`decisions/`](decisions/README.md) | 已作出的架构决策、理由、影响与重新审议条件 |
| [`design/`](design/README.md) | 当前 Web 信息架构、视觉、交互和动效约束 |
| [`development/`](development/README.md) | 仓库结构、开发计划、生成物和验证方式 |
| [`operations/`](operations/README.md) | Windows x64 运行、升级、备份和恢复 |
| [`validation/`](validation/README.md) | 有日期和边界的审查、测试与历史证据 |
| [`postmortems/`](postmortems/README.md) | 真实事故复盘；不存放普通缺陷记录 |
| [`images/`](images/README.md) | 被文档引用的共享图片与图表资源 |

## 权威来源

| 主题 | 主要来源 |
| --- | --- |
| 产品范围与不变量 | [产品定义](reference/product-definition.md) |
| 术语 | [术语表](reference/glossary.md) |
| 启动参数与 AppDirs | [配置参考](reference/configuration.md)及 `internal/config` |
| 系统边界与数据流 | [架构文档](architecture/README.md) |
| HTTP API | `internal/contract/api/openapi.yaml` |
| Go 客户端 API | `api` package 的生成代码与 package 文档 |
| 当前计划与完成条件 | [开发路线](development/roadmap.md)和[测试门禁](development/testing-and-release-gates.md) |
| 技术选择 | [ADR 索引](decisions/README.md) |
| 历史验证 | [验证目录](validation/README.md) |

## 状态表达

- **当前事实**：可从当前工作树的实现、配置或生成源直接核对。
- **既定决策**：ADR 中已接受的约束；不等同于全部实现或门禁完成。
- **计划**：路线图中的未来工作，不能写成现有能力。
- **历史记录**：带日期的旧快照，只在明确说明仍有效时影响当前判断。
- **待确认**：当前材料不足；不得用推测补齐。

文档的文件名、结构、链接和写作规则见[文档维护指南](development/documentation-guide.md)。
