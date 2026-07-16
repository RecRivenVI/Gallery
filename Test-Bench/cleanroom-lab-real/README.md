# gallery feedback cleanroom lab

本目录用于 Gallery（画廊）的第二轮净室考察；仍有效的聚合结果和局限以 [验证记录](../../Documents/证据/验证记录.md) 为准。

边界：

- 真实媒体根只读；工具只输出脱敏数量、类型、耗时和结构结论；
- 所有输出、数据库、缓存和复制夹具必须位于本目录；
- 不保存 metadata 原文、媒体内容、绝对媒体路径或完整 URL；
- 正式代码、包和接口示例统一使用代号 `gallery`。

主要命令：

```powershell
go run ./cmd/realprobe --root "library-a=..." --root "library-b=..." --out results/real-pre.json
go run ./cmd/ruleprobe --root "library-a=..." --root "library-b=..." --out results/rules-real.json
go run ./cmd/identityprobe --dir results/identity-fixtures --out results/identity.json
go run ./cmd/commitbench --n 1000000 --ratios 0.01,0.10,0.50 --dir results/commit-million --out results/commit-million.json
go run ./cmd/searchbench --n 1000000 --bleve=true --dir results/search-million --out results/search-million.json
go run ./cmd/searchlatency --dir results/search-million --out results/search-million-latency.json
go run ./cmd/sortprobe --out results/sort-v1.json
go run ./cmd/celprofile --out results/cel-profile.json
go run ./cmd/contractprobe --dir results/contracts --out results/contracts.json
go run ./cmd/platformprobe --dir results/platform-fixtures --out results/platform.json
go run ./cmd/shellprobe --dir results/shell-runtime --out results/shell.json
```

`contractprobe` 同时覆盖跨库 Saga、快照游标和 Personal 配对。Linux 运行使用交叉编译二进制在 WSL 中执行；macOS 本轮只交叉编译。

执行结果与未完成门禁以当前 [验证记录](../../Documents/证据/验证记录.md) 和 [测试与发布门禁](../../Documents/指南/02-测试与发布门禁.md) 为准。真实媒体测试必须先后各运行一次 `realprobe`，并比较每个 alias 的 `guard_sha256`；任何不一致都使本轮结果无效。
