# 安全政策

## 当前支持状态

| 版本 | 安全修复 |
| --- | --- |
| `main` / pre-alpha | 尽力处理 |
| 已发布版本 | 当前没有正式发行版本 |

Gallery 目前处于 pre-alpha 阶段，尚无图形界面、安装包或正式发行版本，也没有面向普通用户的公开部署。安全政策的主要目的是为早期代码审阅者和贡献者提供一条私密报告渠道，而不是承诺已经具备成熟的漏洞响应 SLA。

## 报告安全问题

请**不要**通过公开 Issue、Discussion 或 Pull Request 报告可能导致以下后果的问题：

- 未授权文件读取或写入
- Source 只读边界被突破
- 路径穿越或符号链接逃逸
- Session、配对（Pairing）或 capability 授权绕过
- 敏感信息、完整媒体路径或 token 泄露
- 恶意媒体文件或 metadata 导致的资源耗尽（DoS）
- 数据库损坏或用户事实（Canonical/Overlay/Binding 等）丢失

请改为使用 GitHub 的 [Private vulnerability reporting](https://github.com/RecRivenVI/Gallery/security/advisories/new) 提交报告。仓库已启用该功能，报告只对维护者可见。

提交报告时请尽量包含：

- 影响的模块或 API 路径
- 复现步骤或触发条件
- 预期行为与实际行为的差异
- 相关 commit SHA 或分支

请不要在报告中附带真实媒体内容、完整个人路径或其他他人隐私信息；如需说明结构，使用脱敏或合成示例即可。

## 响应流程

作为 pre-alpha 阶段的单人维护项目，暂不承诺固定的响应时限。收到报告后会尽快确认，评估影响范围，并在修复就绪后协调披露时间；报告者会被告知修复进度。

## 已启用的仓库安全能力

- Private vulnerability reporting
- Dependabot alerts
- Secret scanning 与 push protection
