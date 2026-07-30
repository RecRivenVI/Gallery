# Gallery {{VERSION}} Windows x64 便携包

> 制品状态：{{RELEASE_STATUS}}
>
> 源码提交：`{{COMMIT}}`

本包是无桌面壳发行形态：`galleryd.exe` 已内嵌完整用户前端与管理前端，浏览器是业务界面；
`galleryctl.exe` 提供最小诊断入口。Node.js、npm 和 Go 不进入运行时。

## 首次启动

1. 在 PowerShell 中进入本目录。
2. 运行 `./galleryd.exe -mode personal -listen 127.0.0.1:8080`。
3. 打开 `http://127.0.0.1:8080/`，按页面提示完成 Personal 配对。
4. 结束服务时在运行窗口按 `Ctrl+C`，等待进程优雅退出。

如果 8080 已占用，可改为其它 loopback 端口。不要把 Personal 模式绑定到局域网或公网地址；服务端也会拒绝该配置。

## 数据与只读 Source

- 程序目录只保存可替换的发行文件；数据库、配置、日志、缓存和临时文件位于当前 Windows 用户的 Gallery AppDirs。
- 媒体 Source 永久只读。不要把 AppDirs 放进 Source，也不要把 Source 放进 AppDirs。
- `galleryd.exe -h` 可查看启动参数；`galleryd.exe version` 与 `galleryctl.exe version` 可核对制品版本。
- `release-manifest.json` 记录目标平台、提交、Web/契约版本、签名状态和文件摘要；包内 `SHA256SUMS` 与 ZIP 旁的 `.sha256` 可用于离线完整性核对。
- 本制品的 control schema 为 {{CURRENT_CONTROL_SCHEMA}}；真实历史二进制门禁连续覆盖 schema {{MINIMUM_CONTROL_SCHEMA}} 到当前版本的前向迁移。更早的开发快照不在本制品已验证的升级范围内。

## 覆盖升级与回退

1. 在管理端创建并确认最新 `control.db` 备份。
2. 用 `Ctrl+C` 停止旧进程，确认 `galleryd.exe` 已退出。
3. 解压新包到新的程序目录，保留旧程序目录作为短期回退副本；不要覆盖或删除 AppDirs。
4. 启动新版本。服务会在打开数据库前应用兼容迁移；Catalog 不兼容时允许重建，但用户事实必须从 control 备份恢复。
5. 不要用旧版本直接打开已被新版本迁移的数据。需要回退时，先使用相应版本的 control 备份恢复流程。

当前没有自动更新器；升级必须显式完成上述备份和停止步骤。

## 安全与支持边界

- Authenticode 状态：{{AUTHENTICODE_STATUS}}。未签名包只能用于本地开发测试，不能冒充正式 RC。
- 当前正式门禁状态以仓库 `PROJECT_STATUS.md` 和 `Documents/证据/验证记录.md` 为准；便携包构建成功不等于
  Reference Performance、Security、Web、Windows 发行候选或 v1 门禁通过。
- Windows 11 x64 / NTFS 是 v1 正式目标；其它系统、SMB/NAS/UNC、真实移动设备和桌面壳不得由本包外推为已支持。

Gallery 依据 GNU AGPL v3 发行。完整许可见 `LICENSE`，第三方说明见 `THIRD_PARTY_NOTICES.md`，对应源码位于
`https://github.com/RecRivenVI/Gallery`。三个标准 CycloneDX 文件位于 `sbom/`，具体规范版本登记在发行清单中。
