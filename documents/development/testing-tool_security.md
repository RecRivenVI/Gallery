# Security testlab

本 package 在临时 AppDirs 和合成身份上组合验证 LAN Owner、Session、Grant、API Token、路径边界、恶意 metadata、媒体工具预算和恢复标记。它不读取本机 testlab 路径配置，不访问真实 Source，也不输出 secret 或绝对路径。

普通入口：

```powershell
go test ./tools/testlab/stages/stage5/security
```

## 固定外部工具场景

`TestMaliciousMediaCorpusWithPinnedTools` 默认关闭，只在 Windows 上接受同时给出绝对路径、精确版本和 SHA-256 的 `ffprobe` 与 `ffmpeg`。输入是仓库生成的结构攻击样本，不是真实媒体。工具调用仍经过生产 ToolDiscovery、Job 预算和 Windows Job Object。

所需环境变量和报告路径以测试源码为准；启用前必须检查变量不会泄露本机路径到日志或仓库。报告写入授权测试根，测试产物不得提交。

该场景只验证指定工具制品在当前小型语料上的有界行为，不替代 coverage-guided fuzz、第三方漏洞语料、真实设备或非 Windows 平台验证。
