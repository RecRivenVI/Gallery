# Cleanroom Lab

类型：历史实验模块  
状态：不属于正式构建、测试或运行时

本独立 Go module 保存早期净室技术探针，用合成数据比较 Catalog 提交模型、搜索引擎、规则技术、文件身份、多库布局、路径安全、账户流程和交付外壳。它不读取旧数据库、旧配置或真实媒体，也不定义当前 Gallery 架构。

## 内容

- `cmd/commitmodels`、`cmd/crashsim`：提交与崩溃恢复原型；
- `cmd/searchbench`：SQLite FTS 与 Bleve 探针；
- `cmd/rulestech`：自建 primitive、CEL 与 Starlark 比较；
- `cmd/fsidentity`、`cmd/multilib`、`cmd/fssec`：身份、存储和路径边界；
- `cmd/account`：合成账户和实时连接原型；
- `deploy/`：Docker、PWA、ASP.NET 和 Wails 交付实验；
- `internal/synth`：实验专用合成数据。

旧 README 中的毫秒、文件大小和通过数量没有对应原始报告留在当前目录，本次未复跑，因此不再作为当前事实保留。已经接受的产品决策只以 [`docs/decisions/`](../../../docs/decisions/README.md) 和正式实现为准。

本模块有自己的 `go.mod`，不进入根模块 `Check.ps1`。需要重新调查时先阅读对应命令源码和 `-h`，把输出写入仓库外临时目录。`deploy/wails-shell/README.md` 及其 `build/README.md` 是 Wails scaffold 自带说明，本次没有手工改写生成材料。
