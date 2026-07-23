# 安全政策

## 当前支持状态

| 版本 | 安全修复 |
| --- | --- |
| `main` / pre-alpha | 尽力处理 |
| 已发布版本 | 当前没有正式发行版本 |

Gallery 目前处于 pre-alpha 阶段，已有同源内嵌 Web/PWA 代码基线，但尚无安装包或正式发行版本，也没有面向普通用户的公开部署。安全政策的主要目的是为早期代码审阅者和贡献者提供一条私密报告渠道，而不是承诺已经具备成熟的漏洞响应 SLA。

当前正式后端已实现 Personal 配对、LAN 本地账户、Argon2id、服务端 Session、API Token、资源 Grant 与即时吊销，以及带 scope/过期/吊销/固定 Blob 语义的匿名 Work/Media/媒体正文分享；Web/PWA 不把 Session、CSRF、secret、publication、cursor 或业务事实写入持久浏览器存储，Service Worker 也不缓存 API、WebSocket 或媒体响应。Chrome/Edge 已通过同机 Personal/LAN 主路径与 Session 吊销验证，但阶段 5 Security Gate 尚未通过。Personal 只用于 loopback；LAN 只适用于受信私网并要求先在 loopback 初始化 Owner；Remote/OIDC 与公网反向代理部署不受支持。当前剩余安全门禁主要是真实 LAN 多设备、目标低端设备 Argon2id 延迟/并发、真实恶意容器和外部工具资源上限验证。

## 报告安全问题

请**不要**通过公开 Issue、Discussion 或 Pull Request 报告可能导致以下后果的问题：

- 未授权文件读取或写入
- Source 只读边界被突破
- 路径穿越或符号链接逃逸
- Session、配对（Pairing）或 capability 授权绕过
- API Token、资源 Grant、分享 credential 或 WebSocket 吊销绕过
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
