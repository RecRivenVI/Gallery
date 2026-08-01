# 安全政策

## 支持范围

Gallery 尚无正式发行版本。`main` 是 pre-alpha 开发线，安全修复按可用维护能力尽力处理，不承诺稳定版本支持周期或固定响应 SLA。

当前部署模型只包括：

- Personal：仅允许 loopback，通过一次性配对建立 Session；
- LAN：仅允许 loopback 或私有地址，必须先在本机初始化 Owner；
- Remote/OIDC、公网反向代理和多租户：不受支持。

## 私密报告

请使用 GitHub 的 [Private vulnerability reporting](https://github.com/RecRivenVI/Gallery/security/advisories/new)，不要通过公开 Issue、Discussion 或 Pull Request 披露可能造成以下影响的问题：

- 未授权读取、Source 写入、路径穿越或链接逃逸；
- 配对、Session、CSRF、账户、Token、Grant、分享或 WebSocket 授权绕过；
- secret、绝对私人路径、媒体内容或敏感 metadata 泄露；
- 恶意媒体、规则、Schema 或外部工具导致的资源耗尽；
- `control.db` 用户事实丢失、备份恢复破坏或 Catalog 交叉 revision 泄漏。

报告请包含受影响版本或 commit、最小复现、影响范围和建议缓解措施。使用合成数据并删除凭据、Cookie、个人路径和真实媒体。

## 处理原则

维护者会先确认报告可复现性和影响边界，再协调修复与披露。修复尚未发布前，请避免公开利用细节。安全状态以当前代码与当轮验证为准；文档中的设计目标或历史记录不构成安全证明。
