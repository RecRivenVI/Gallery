# 阶段 5 Security testlab

本模块复用 testlab 的报告模型，在临时 AppDirs 与合成身份上验证 LAN Owner、Session、
资源 Grant、API Token，并把路径逃逸、恶意 metadata、非可信媒体正文与启动期恢复标记纳入
Correctness/Security 闭环。它不读取本地
`testlab.local.json`，不访问真实 Source，也不输出 secret 或绝对路径。

正式执行入口：

```powershell
& $env:GALLERY_GO test ./tools/testlab/stages/stage5/security -v
```

高风险并发和重复门禁仍由生产包测试承担：

```powershell
& $env:GALLERY_GO test -count=20 ./internal/auth ./internal/transport/httpapi ./internal/contract/realtime ./internal/backup
& $env:GALLERY_GO test -count=100 ./internal/auth -run 'TestLANOwnerInitializationIsAtomicUnderConcurrency|TestLANOwnerUserGrantTokenAndRevocationLifecycle'
```
