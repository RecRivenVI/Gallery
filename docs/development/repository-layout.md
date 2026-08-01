# 仓库结构与开发范围

## 结构原则

Gallery 使用单一根 Go module、薄命令入口和按能力划分的私有实现。Go 后端是基座，`web/` 是可选构建组件；`experiments/` 是隔离实验，不参与正式产品 package 集合。

```text
gallery/
├── cmd/
│   ├── galleryd/          # 服务进程入口
│   └── galleryctl/        # 公开 API 的最小 CLI
├── api/                   # 生成的公开 Go DTO 与客户端
├── version/               # 产品、服务和协议版本标识
├── internal/
│   ├── application/       # Library、Source、Binding、治理用例
│   ├── auth/              # 认证、账户、Token、Grant、Share
│   ├── catalog/           # Catalog staging、publication、GC
│   ├── contract/          # OpenAPI、错误、查询与实时契约
│   ├── platform/          # OS 适配器
│   ├── query/             # 查询计划与物化读取
│   ├── rules/             # 规则格式、编译与执行
│   ├── scanner/           # 扫描与内容确认
│   ├── storage/           # SQLite 与 migration
│   ├── transport/httpapi/ # HTTP 路由和适配
│   └── webapp/            # 内嵌 Web 资产处理器
├── web/                   # React/TypeScript 双入口 Web/PWA
├── tools/testlab/         # 根 module 内的验证工具
├── tests/                 # 跨 package 夹具与约定
├── scripts/               # Windows 检查、制品和升级脚本
├── packaging/             # Windows 便携包与测试快照资源
├── docs/                  # 长期维护文档
└── experiments/testbench/ # 独立 module 的历史技术实验
```

## Package 规则

- `cmd/<name>` 只处理参数、信号、装配和退出码。
- 默认产品代码进入 `internal/`；只有承担仓库外兼容责任的代码才进入公开 package。
- 当前公开 package 只有 `api` 与 `version`。
- package 目录使用简短、全小写的领域或能力名；不建立 `common`、`utils`、`helpers` 等无所有权目录。
- 测试通常与 package 同目录，夹具放相邻 `testdata/`；`tests/` 不建立第二套产品源码树。
- `tools/testlab` 属于根 module，但不是运行时依赖；`experiments/testbench` 的独立 module 不进入正式检查范围。

## 平台文件

平台差异使用文件后缀和 build constraint 共同表达：

- `name_windows.go`：Windows 实现；
- `name_darwin.go`、`name_linux.go`：单一系统实现；
- `name_unix.go`：明确列出的 Unix 系统共享实现；
- `name_other.go`：有明确负向约束的回退实现；
- 只有行为真的依赖架构时才增加 `_amd64`、`_arm64` 等后缀。

通用文件不得用 `runtime.GOOS` 或 `runtime.GOARCH` 选择产品行为。当前平台适配集中在 `internal/platform/` 以及少量 Windows 验证工具中。

## 当前 OS 范围

Windows x64 是 RC 前唯一主动开发、运行验证与发行目标。RC 前：

- 不同步推进 Linux、macOS、其它 `GOOS` 或其它 `GOARCH`；
- 保留现有非 Windows 文件，但不把它们描述为受支持实现；
- 共享签名变化如需机械更新回退文件，不扩展其能力或新增平台结论；
- CI 与便携制品继续显式设置 `GOOS=windows`、`GOARCH=amd64`。

## Web 边界

`galleryd` 嵌入 `internal/webapp/dist`，但后端架构仍由 Go API 定义。修改纯 Go 核心不应要求 Node.js；修改或重建 Web 时才进入 `web/package.json` 的 npm 流程。用户端 `/` 与管理端 `/manage` 共用后端和会话，但使用不同 HTML 外壳，只有用户端进入 PWA scope。

## 文本与生成物

所有 tracked 文本使用 UTF-8 与 LF。`.gitattributes` 规定 Git 换行，`.editorconfig` 规定编辑器默认值。生成文件留在所属 package 或组件内，来源与命令见[生成文件说明](generated-files.md)。

结构调整必须同轮更新 import、生成配置、脚本、CI、文档链接和发行资源；目录变得整齐不会自动提高功能成熟度。
