# Gallery Windows x64 测试快照

本包用于提前确认当前 Gallery 的完成度，不是正式 RC，也不要求满足安装、签名、升级矩阵、真实设备或完整性能门禁。
`galleryd.exe` 已内嵌当前用户前端与管理前端；运行时不需要安装 Go、Node.js 或 npm。

## 快速启动

1. 将 ZIP 完整解压到一个新目录，不要直接在压缩包内运行。
2. 在该目录打开 PowerShell。
3. 执行：

   ```powershell
   powershell -ExecutionPolicy Bypass -File .\Start-Gallery-Test.ps1
   ```

4. 首次启动会预置 `Pixiv` Library、当前本机的只读 pixiv Source、已发布规则以及一个
   `paused` Binding；不会自动启动扫描。完成后脚本会打开 `http://127.0.0.1:18080/`。
5. 按页面提示完成 Personal 配对；需要扫描时，请先在管理端确认目录和规则，再手动恢复 Binding 并发起任务。
6. 用户前端位于 `/`，管理前端位于 `/manage`。结束测试时回到 PowerShell 窗口按 `Ctrl+C`，等待服务退出。

默认测试数据位于 `%LOCALAPPDATA%\Gallery-Test\<解压目录名>`，不会使用 Gallery 正式实例的默认 AppDirs。
启动脚本固定 `GOMAXPROCS=2` 与 `GOMEMLIMIT=1536MiB`，避免测试实例无边界占用处理器和内存。
若端口被占用，可使用 `-Port`；若要指定测试数据目录，可使用 `-AppRoot`：

```powershell
powershell -ExecutionPolicy Bypass -File .\Start-Gallery-Test.ps1 -Port 18081 -AppRoot 'D:\Gallery-Test-Data'
```

本包从 `presets/pixiv-root.local.txt` 读取当前测试机上的 Pixiv 根目录。若目录迁移，可显式指定；
若只想启动空白实例，可跳过预置：

```powershell
powershell -ExecutionPolicy Bypass -File .\Start-Gallery-Test.ps1 -PixivRoot '<新的 Pixiv 根目录>'
powershell -ExecutionPolicy Bypass -File .\Start-Gallery-Test.ps1 -SkipPixivPreset
powershell -ExecutionPolicy Bypass -File .\Start-Gallery-Test.ps1 -NoBrowser
```

预置状态位于 AppRoot 下的 `pixiv-preset-status.json`，成功标记为 `ready`。首次成功后的
`pixiv-preset-v1.json` 只记录 Gallery 内部资源 ID、规则 hash 与 `scanStarted=false`，不保存媒体内容。

## 建议检查范围

- Personal 配对、用户前端与管理前端导航；
- Library 创建、服务端目录选择、只读 Source 登记与规则绑定；
- 扫描任务、任务历史、重试/取消和实时状态；
- 作品浏览、搜索/过滤/排序、详情、Viewer 与 Overlay；
- 规则草稿、Schema 表单、Dry Run/Explain/Trace、发布/回滚；
- Session、API Token、Share、用户/授权以及备份、恢复和 Catalog 维护页面。

Source 永久只读。不要把测试 AppDirs 放进媒体 Source，也不要把 Source 放进测试 AppDirs。

## 已知边界

- 本包未签名；Windows 可能显示未知发布者提示，只应用于本机测试。
- 本包不含安装器、自动更新器、桌面壳或后台服务注册。
- 本包携带此前真实有界验证消费的 Pixiv 规则配置，但不携带 Pixiv 媒体、私密 metadata、Cookie、Token
  或现成数据库；Source 只登记本机目录，Binding 默认暂停。Pixiv 全量只读验收仍是后续正式 RC 的最后一道门槛。
- 构建成功不表示 Reference Performance、Degradation、Security、Web、真实存储、平台发行或正式 RC 门禁已经通过。

`TEST-SNAPSHOT.json` 记录源码提交、工作树状态、版本和内嵌 Web 契约；`SHA256SUMS` 与 ZIP 旁的 `.sha256`
用于核对完整性。许可见 `LICENSE`，第三方说明见 `THIRD_PARTY_NOTICES.md`。
