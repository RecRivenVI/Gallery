# Gallery Windows x64 测试快照

本包用于 Windows x64 本机隔离试用，不是 RC、安装器或自动更新包。`galleryd.exe` 已内嵌用户端和管理端，运行时不需要 Go、Node.js 或 npm。

## 启动

1. 完整解压到新目录。
2. 在该目录打开 PowerShell。
3. 运行：

   ```powershell
   powershell -ExecutionPolicy Bypass -File .\Start-Gallery-Test.ps1
   ```

4. 打开脚本显示的地址，完成 Personal 配对。用户端位于 `/`，管理端位于 `/manage/`。
5. 结束时按 `Ctrl+C` 并等待服务退出。

默认 AppRoot 为 `%LOCALAPPDATA%\Gallery-Test\<解压目录名>`，与正式实例隔离。脚本设置 `GOMAXPROCS=2` 和 `GOMEMLIMIT=1536MiB`。

可用参数：

```powershell
powershell -ExecutionPolicy Bypass -File .\Start-Gallery-Test.ps1 -Port 18081 -AppRoot '<测试目录>'
powershell -ExecutionPolicy Bypass -File .\Start-Gallery-Test.ps1 -PixivRoot '<只读 Pixiv 根>'
powershell -ExecutionPolicy Bypass -File .\Start-Gallery-Test.ps1 -SkipPixivPreset
powershell -ExecutionPolicy Bypass -File .\Start-Gallery-Test.ps1 -NoBrowser
```

## Pixiv 预置

未指定 `-SkipPixivPreset` 时，脚本从 `presets/pixiv-root.local.txt` 或 `-PixivRoot` 取得路径，随后建立 Pixiv Library、只读 Source、规则包和 `paused` Binding。预置不会启动扫描，也不会复制媒体。已有冲突资源时初始化器拒绝猜测或覆盖。

`pixiv-preset-status.json` 记录初始化状态，`pixiv-preset-v1.json` 只记录内部资源 ID、规则 hash、Binding 状态和 `scanStarted=false`。这些文件位于测试 AppRoot。

## 边界

- Source 与测试 AppRoot 必须互不重叠；Source 始终只读。
- 包内不应包含媒体、私密 metadata、Cookie、Token 或现成数据库。
- 测试快照不包含安装器、后台服务注册、自动更新器或桌面壳。
- 浏览器试用和构建成功不能替代正式门禁，也不能外推到物理 LAN、其它设备或其它平台。

`TEST-SNAPSHOT.json`、`SHA256SUMS` 和 ZIP 旁的 `.sha256` 用于核对快照来源与完整性。许可见 `LICENSE`，第三方说明见 `THIRD_PARTY_NOTICES.md`。
