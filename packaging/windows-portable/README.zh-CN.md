# Gallery {{VERSION}} Windows x64 便携包

> 制品状态：{{RELEASE_STATUS}}
>
+> 源码提交：`{{COMMIT}}`
+>
+> Authenticode：{{AUTHENTICODE_STATUS}}

本包包含 `galleryd.exe`、`galleryctl.exe` 和内嵌 Web 客户端。运行时不需要 Go、Node.js 或 npm；浏览器是用户端和管理端界面。

## 启动

1. 完整解压 ZIP，在解压目录打开 PowerShell。
2. 运行 `./galleryd.exe -mode personal -listen 127.0.0.1:8080`。
3. 打开 `http://127.0.0.1:8080/`，按页面提示完成 Personal 配对。
4. 结束时按 `Ctrl+C`，等待进程优雅退出。

端口占用时可选择其它 loopback 端口。Personal 模式不得绑定 LAN 或公网地址。`galleryd.exe -h` 显示当前参数；`galleryd.exe version` 和 `galleryctl.exe version` 显示制品版本。

## 数据目录和 Source

- 默认配置目录为 `%APPDATA%\Gallery`；数据、状态、缓存、日志、临时文件和运行描述符位于 `%LOCALAPPDATA%\Gallery` 的对应子目录。
- `-app-root <path>` 把上述目录统一放到指定根下，适合隔离实例。
- Source 永久只读。AppDirs 与 Source 必须互不重叠，不同 Source 根也必须互不重叠。
- 外部 `ffprobe`/`ffmpeg` 不从 `PATH` 自动发现；使用时必须同时配置路径、版本和 SHA-256。

## 完整性

`release-manifest.json` 记录目标、版本、提交、Web/契约版本、签名状态和文件摘要。`SHA256SUMS` 与 ZIP 旁的 `.sha256` 用于离线核对；`sbom/` 保存 CycloneDX SBOM。

本包的 control schema 为 {{CURRENT_CONTROL_SCHEMA}}，manifest 声明的最早兼容基线为 {{MINIMUM_CONTROL_SCHEMA}}。兼容声明只适用于该制品实际通过的历史升级矩阵，不应外推到更早快照。

## 升级与回退

1. 在管理端创建并确认最新 control 备份。
2. 用 `Ctrl+C` 停止旧进程并确认退出。
3. 把新包解压到新的程序目录，不覆盖 AppDirs；暂时保留旧程序目录。
4. 启动新版本并检查迁移与服务状态。

不要用旧版本直接打开已被新版本迁移的数据。回退需要匹配版本和 schema 的已验证备份恢复流程。当前没有自动更新器。

## 支持边界

该制品只面向 Windows x64。未签名或标为测试状态的包不是正式 RC。构建成功、manifest 存在或基础启动成功均不等于 Correctness、Security、Performance、Web、升级和真实 Source 门禁全部通过。

许可见 `LICENSE`，第三方说明见 `THIRD_PARTY_NOTICES.md`，对应源码仓库为 `https://github.com/RecRivenVI/Gallery`。
