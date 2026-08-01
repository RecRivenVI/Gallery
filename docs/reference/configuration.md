# 配置参考

## 配置入口

当前 `galleryd` 只从命令行参数构建启动配置；源码中没有加载 TOML、YAML 或 JSON 运行配置文件的入口。`Config` AppDir 仍作为产品目录保留，但不能据此假设已有文件格式。

```powershell
galleryd.exe -mode personal -listen 127.0.0.1:8080
```

`galleryd version` 输出服务名与制品版本，`galleryd -h` 输出参数帮助。

## 参数

| 参数 | 默认值 | 说明 |
| --- | --- | --- |
| `-mode` | `personal` | `personal` 或 `lan` |
| `-listen` | `127.0.0.1:0` | HTTP 监听地址；端口 `0` 由系统分配 |
| `-app-root` | 空 | 开发/测试用统一 AppDirs 父目录 |
| `-source-root` | 可重复、默认空 | 启动时参与 AppDirs/Source 重叠守卫；不自动登记 Source |
| `-file-root` | 可重复、默认空 | `id=absolute-path`，建立只读文件浏览根 |
| `-external-tool-path` | 可重复、默认空 | `ffprobe=absolute-path` 或 `ffmpeg=absolute-path` |
| `-external-tool-version` | 可重复、默认空 | 与工具 ID 对应的精确 version token |
| `-external-tool-sha256` | 可重复、默认空 | 与工具 ID 对应的 64 位 SHA-256 |

外部工具的 path、version 和 SHA-256 必须成组声明；当前只接受 `ffprobe` 与 `ffmpeg`。服务不会从 `PATH` 静默发现工具。

## 监听限制

- Personal 只接受 loopback 主机名或地址。
- LAN 接受 loopback 或明确私有地址，不接受 unspecified 或公网地址。
- LAN 首次初始化前，非 loopback 监听会失败；先以 LAN 模式监听 loopback 并创建 Owner。

## Windows AppDirs

默认目录由 `APPDATA` 与 `LOCALAPPDATA` 计算：

| 角色 | 默认位置 |
| --- | --- |
| Config | `%APPDATA%\Gallery` |
| Data、State、Cache、Logs、Temp、Runtime | `%LOCALAPPDATA%\Gallery` 下的对应子目录 |

使用 `-app-root D:\Gallery-Dev` 时会改为 `config/`、`data/`、`state/`、`cache/`、`logs/`、`tmp/` 和 `run/` 七个子目录。`data/` 保存 `control.db` 与 `catalog.db`，`run/galleryd.json` 是运行时 descriptor。

## 安全约束

AppDirs 的任一写入目录不能与任一 `-source-root` 重叠，多个 Source root 也不能互相包含。目录规范化会解析已存在的链接；配置失败发生在数据库创建之前。

不要把真实凭据、个人绝对路径或媒体目录写入仓库文档或示例。需要本机记录时复制[本机环境模板](../development/local-environment-template.md)到被 `.gitignore` 排除的本地文件。
