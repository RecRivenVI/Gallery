# cleanroom-lab — 净室重建技术验证

本验证台的仍有效结论已收敛至 [验证记录](../../Documents/证据/验证记录.md)。**与任何旧 Gallery 无数据、接口或代码兼容关系**：
本目录不读取任何旧数据库、旧配置或旧 catalog,全部原型使用 `internal/synth` 自生成的合成素材。
本轮的问题是:如果今天从零设计一个规则驱动、本地优先、自托管、多前端的个人媒体目录平台,
哪些技术选择才是真正合理的——而不是"如何迁移旧系统"。

执行日期 2026-07-16。工具链:便携版 Go 1.26.5、.NET SDK 10.0.301、Node 22、ffmpeg 2024-11；核心 Go 后端原型零 cgo，Wails 桌面壳使用系统 WebView2。

## 原型与结果

| 原型 | 决策问题 | 关键结果 |
| --- | --- | --- |
| `cmd/commitmodels` (P12) | generation / staging / 原地增量 三种提交模型 | full: staging 668ms < inplace 951ms < generation 2.0s;磁盘:staging/inplace 6.5MiB < generation 12.7MiB;读一致性 generation 最强 |
| `cmd/crashsim` (P12) | 崩溃恢复实证(子进程写一半强杀) | generation:崩溃后 reader 见 0 半代次行(干净);inplace:崩溃后 15000 行已带新 scan_id(新旧混合,依赖清理) |
| `cmd/searchbench` (P13) | SQLite FTS5 vs Bleve(CJK) | FTS5 trigram 对 2 字 CJK("机械"/"星空")返回 0;Bleve cjk(bigram)命中 2083/4167,亚毫秒;代价 Bleve 独立索引目录、更大(20.6 vs 11.2MiB) |
| `cmd/rulestech` (P14) | 自建原语 / CEL / Starlark | 三者均可表达同一规则;自建原语可静态分析+生成 UI+最安全;CEL 受限可分析;Starlark 图灵完备但难分析、不宜放进配置 |
| `cmd/fsidentity` (P15) | 媒体身份:路径 / FileID / 内容签名 | 无单一方案稳定;推荐组合:内容签名(去重/移动跟踪)+ NTFS FileID(同卷移动关联)+ 路径(仅当前位置) |
| `cmd/multilib` (P16) | 每库一 db vs 统一库+library_id | 统一库全局 keyset 3 页 4.3ms 走单索引;ATTACH 跨库全局排序是全量归并;默认统一库,拆库仅为强隔离 |
| `cmd/fssec` (P17) | 路径解析安全 | 14/14 攻击面(../、编码、盘符、UNC、保留名、尾点、NUL、symlink 逃逸)全部拒绝 |
| `cmd/account` (P18) | 账户+多客户端闭环 | 匿名 401→owner 登录+CSRF→viewer 越权 403→CLI Bearer token→分享链接；Session 吊销后真实 `/ws/v1` 以 `4001/session_revoked` 主动关闭 |
| `deploy/` (P19) | Go/ASP.NET/Wails/Docker/PWA | Go 6.0MiB(+Bleve 16.6)；ASP.NET 不裁剪 98.8MiB，裁剪版 17MiB 但 Kestrel 运行时崩；Wails 壳 11MiB/烟雾工作集 27.4MiB；Dockerfile 与 PWA 壳已生成 |
| `internal/synth` (P11) | 从产品概念造最小领域数据 | Work/Media/Creator 及内容签名派生身份,供上述原型共用 |

## 复现

```powershell
$env:GOROOT="<解压>\go"; $env:GOPATH="<可写>\gopath"; $env:PATH="$env:GOROOT\bin;$env:PATH"
cd Test-Bench\cleanroom-lab
go mod tidy; go build ./...
go run ./cmd/commitmodels -n 50000 -dir $env:TEMP
go run ./cmd/crashsim -dir $env:TEMP
go run ./cmd/searchbench -n 50000 -dir $env:TEMP
go run ./cmd/rulestech
go run ./cmd/fsidentity
go run ./cmd/multilib -n 20000 -dir $env:TEMP
go run ./cmd/fssec
go run ./cmd/account -mode lan   # 另开终端 curl,GET /quit 退出
# P18 WS:以 Session Cookie 连接 ws://127.0.0.1:18096/ws/v1,吊销 sid 后应收到 4001
# P19: go build -ldflags="-s -w" ./cmd/account; cd deploy/aspnet; dotnet publish -c Release
# P19 Wails:cd deploy/wails-shell; wails build -clean -o GalleryShellProbe.exe
```

P19 产物：`deploy/Dockerfile`、`deploy/pwa/` 和 `deploy/wails-shell/`。本轮 Docker daemon 未运行，所以没有把 Dockerfile 构建成实际镜像；PWA 也尚未做浏览器安装/离线自动化。

不做的事:不读旧库、不导旧配置、不对拍旧行为、不追求与旧结果一致。
