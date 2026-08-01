# Testlab

`tools/testlab` 是 Gallery 的可复用验证框架。它组合生产 Store、真实 `galleryd`、HTTP 客户端、合成语料和只读 Source guard；它不是产品运行时依赖，也不把历史一次性结果内置为当前结论。

## 组成

| 路径 | 职责 |
| --- | --- |
| `cmd/seed` | 建立确定性合成 Catalog publication |
| `cmd/probe` | 驱动查询、媒体、性能和真实 Source 场景 |
| `cmd/publication-perf` | 测量完整 publication 变化矩阵 |
| `cmd/rulesimport` | 调用正式 legacy converter 生成逐 Source 规则包 |
| `cmd/guard` | 创建和比较 Source 零写入清单 |
| `cmd/inventory` | 枚举 testlab manifest 与报告 |
| `cmd/web-e2e` | 驱动真实后端浏览器场景 |
| `cmd/historical-upgrade` | 验证历史 control schema 前向升级 |
| `cmd/portable-upgrade` | 验证 Windows 便携升级与恢复窗口 |
| `internal/` | 边界、语料、进程、报告、host facts、规则索引和 Source guard 公共实现 |
| `stages/` | 扫描、查询、媒体、安全和真实 Source 的组合场景 |
| `fixtures/` | 小型规则和目录夹具 |

## 使用方式

先从命令自身读取当前参数，不复制历史报告中的长命令：

```powershell
go run ./tools/testlab/cmd/seed -h
go run ./tools/testlab/cmd/probe -h
go run ./tools/testlab/cmd/publication-perf -h
go run ./tools/testlab/cmd/rulesimport -h
go run ./tools/testlab/cmd/guard -h
```

可重复 smoke 由 Go 测试直接提供：

```powershell
go test ./tools/testlab/stages/stage3 ./tools/testlab/stages/stage4
go test ./tools/testlab/stages/stage5/security
```

本文件只说明入口；是否执行、使用何种规模和何种真实 Source 必须服从当前任务授权与 [`docs/development/testing-and-release-gates.md`](../../docs/development/testing-and-release-gates.md)。

## 数据与路径

- AppRoot、报告、日志和续跑状态必须位于明确授权的测试根，不得落入 Source 或 Git 工作树。
- 本机路径配置使用被忽略的 `docs/development/examples/testlab.local.json`；模板为 `testlab.local.example.json`。
- `rulesimport` 的输出含 Source 根路径，属于本机制品，禁止提交。
- 报告只保留代号、计数、耗时、稳定错误类别和折叠摘要，不输出 secret、metadata 原文、完整 URL 或媒体绝对路径。
- 首次运行使用空 AppRoot；续跑必须核对语料、publication、工具参数和环境指纹，漂移时 fail closed。

## 规模和结论

规模标签及预算由测试代码和门禁文档共同约束。`reference` 不是自由文本：seed/probe 会核对 Work 数、Source 形状、Creator 关系、publication 和 Catalog revision。`cold-process` 只重启进程，不清空操作系统文件缓存，因此不能描述为冷存储测试。

任何报告都必须区分：

- 已计划、已执行、失败、超时和未执行的组合；
- correctness 与 latency budget；
- warm、cold-process、真实冷存储和不同存储类型；
- 合成 Source 与明确授权的真实 Source。

历史 1M/10M 结果只保存在 [`docs/validation/scale-test-archive.md`](../../docs/validation/scale-test-archive.md)，不作为当前 Gate。

## 真实 Source

真实 Source 路径必须显式提供。`sourcelab` 在每个读取阶段前后执行 guard；墙钟、目录或文件边界触发时必须报告“因边界停止”，不能写成全量完成。内容哈希 guard 默认关闭，启用时同时设置文件数或字节数上限。

生产扫描仍以完整 Source 根为输入；有界模式限制预检和墙钟，不会把生产 discovery 改造成子树扫描。实际 Hash 取消只有在公共 Job API 已观察到目标 Hash 处于 running 后才构成有效证据。

## 维护要求

- testlab 可以导入生产内部包，但产品代码不得反向依赖 testlab。
- 新场景复用现有报告、进程、环境和 Source guard；不要建立阶段专用的平行框架。
- 生成的报告 schema、原子保存和隐私裁剪由 `internal/report` 统一维护。
- 手写规则夹具只用于小型结构测试；真实 legacy 配置转换使用 `cmd/rulesimport`。
