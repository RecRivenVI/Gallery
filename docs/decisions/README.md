# 架构决策记录

ADR 只记录已经作出的长期技术选择，不承担实现进度、测试日志或完整规范。当前行为由架构文档与源码说明，未来工作由路线图说明。

## 状态

- **Proposed**：已形成候选，尚未接受。
- **Accepted**：当前实现与后续变更必须遵守。
- **Superseded**：被后续 ADR 取代，保留历史上下文。
- **Rejected**：比较后不采用。

## 索引

| ADR | 状态 | 主题 |
| --- | --- | --- |
| [0001](0001-product-and-technology-foundation.md) | Accepted | 产品与技术基础 |
| [0002](0002-domain-and-data-ownership.md) | Accepted | 领域与数据所有权 |
| [0003](0003-catalog-publication-and-recovery.md) | Accepted | Catalog publication 与恢复 |
| [0004](0004-rules-engine.md) | Accepted | 规则系统 |
| [0005](0005-query-protocol.md) | Accepted | 查询协议 |
| [0006](0006-api-and-security.md) | Accepted | API 与安全 |
| [0007](0007-platform-adapters.md) | Accepted | 平台适配层 |
| [0008](0008-replaceable-desktop-shell.md) | Accepted | 可替换桌面壳 |
| [0009](0009-web-and-pwa-delivery.md) | Accepted | Web 与 PWA 交付 |
| [0010](0010-confirmed-media-content-reads.md) | Accepted | 已确认媒体正文读取 |
| [0011](0011-go-repository-and-windows-x64-rc.md) | Accepted | Go 仓库结构与 Windows x64 RC |

## 维护规则

新 ADR 使用四位连续编号和小写 kebab-case 文件名。修改 Accepted ADR 的文字只能澄清原决策；改变结论时新增 ADR 并把旧记录标为 Superseded。实施结果、测量数字和临时例外不回填到 ADR。
