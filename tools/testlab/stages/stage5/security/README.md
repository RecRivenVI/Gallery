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

## 恶意媒体真实工具门禁

`TestMaliciousMediaCorpusWithPinnedTools` 默认关闭，只在 Windows 上接受显式绝对路径、精确
version token 与 SHA-256 同时匹配的 `ffprobe`/`ffmpeg`。它生成 13 个不含真实媒体的合成
样本，覆盖尺寸/解压炸弹、截断长度、深层容器、异常 lacing/metadata、压缩附件和外部引用；
所有工具调用均经过生产 ToolDiscovery、持久 Job 预算和 Windows Job Object，且协议/格式白名单
禁止 HLS 外部引用建立网络连接。

启用前设置以下变量；报告路径必须位于授权的测试结果目录：

```powershell
$env:GALLERY_TEST_MALICIOUS_MEDIA = '1'
$env:GALLERY_TEST_FFPROBE_PATH = '<absolute ffprobe path>'
$env:GALLERY_TEST_FFPROBE_VERSION = '<exact version token>'
$env:GALLERY_TEST_FFPROBE_SHA256 = '<sha256>'
$env:GALLERY_TEST_FFMPEG_PATH = '<absolute ffmpeg path>'
$env:GALLERY_TEST_FFMPEG_VERSION = '<exact version token>'
$env:GALLERY_TEST_FFMPEG_SHA256 = '<sha256>'
$env:GALLERY_TEST_MEDIA_CORPUS_REPORT = '<absolute report path>'
& $env:GALLERY_GO test -count=1 ./tools/testlab/stages/stage5/security -run TestMaliciousMediaCorpusWithPinnedTools -v
```

该门禁只证明当前被 pin 的 Windows 工具制品能在所列小型结构攻击样本上有界收敛；它不是
coverage-guided fuzz、第三方 CVE 语料全集，也不替代非 Windows 等价资源门禁。
