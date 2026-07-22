# tools/testlab

阶段无关、可复用、可持续扩展的 Gallery 测试框架，取代原 `tools/stage4/**`（历史归档见
`Documents/证据/阶段3-4大规模测试归档.md`）。阶段 3、阶段 4 及未来阶段共用本目录下的公共模块与
目录规范，不各自重新实现 Runner、Source guard、进程管理或报告格式。

## 目录结构

```text
tools/testlab/
├── cmd/
│   ├── seed/       构建并发布合成 Catalog（testlabseed，直接调用 internal/catalog.Store）
│   ├── probe/      通过真实 galleryd + HTTP 驱动查询/媒体场景（testlabprobe）
│   ├── guard/      独立零写入 guard 快照/校验（testlabguard）
│   └── inventory/  列出两个测试根已有的 manifest/report（testlabinventory）
├── internal/
│   ├── config/       加载 Documents/本地/testlab.local.json
│   ├── corpus/       确定性合成语料生成规则（纯函数，不依赖 internal/*）
│   ├── environment/  Session 建立（一次性配对）
│   ├── process/      galleryd 子进程生命周期
│   ├── report/       Finding/LatencySample/Report，脱敏与原子持久化
│   └── sourceguard/   真实 Source 只读清单与零写入校验
├── stages/
│   └── stage4/
│       ├── query/  结构化过滤/搜索/排序/Ranking/Total/Cursor/性能矩阵
│       └── media/  真实/合成 Source 建立、按需确认、Range/ETag、DerivedAsset
├── fixtures/
│   ├── rules/       全部 10 个目标来源的规则包，见 fixtures/rules/README.md
│   └── synthetic/   小型合成目录夹具
└── schemas/         （规则/结果 Schema 以 internal/rules、internal/report 为唯一权威，本目录只放跨阶段共用的补充 Schema，避免重复定义）
```

`stages/stage3/{scan,catalog,jobs,recovery}` 目录已按模板建立，尚无内容——阶段 3 现有测试证据仍在
`Documents/证据/验证记录.md` 与 `Documents/证据/阶段3-4大规模测试归档.md`，本轮未把阶段 3 遗留脚本
重写进本框架，留待阶段 3 下一轮正式压力测试时补齐，不预先塞入空文件。

## 规模分级

见 `Documents/指南/02-测试与发布门禁.md`「正式验证规模分级」：`smoke`(1k)/`integration`(10k)/
`preflight`(100k)/`reference`(500k) 是标准 Gate；`≥1,000,000` 是显式启用的非推荐诊断场景。

```powershell
# smoke（1k）
& $env:GALLERY_GO run ./tools/testlab/cmd/seed -approot <root>/appdirs/query-1k -scale 1000 -tier smoke -manifest-out <root>/manifests/query-1k.json
& $env:GALLERY_GO run ./tools/testlab/cmd/probe -go $env:GALLERY_GO -repo . -approot <root>/appdirs/query-1k -log <root>/logs/query-1k.log -scenario all -manifest <root>/manifests/query-1k.json -results-out <root>/reports/query-1k.json -tier smoke

# ≥1,000,000（非推荐诊断场景，必须显式确认）
& $env:GALLERY_GO run ./tools/testlab/cmd/seed -approot <root>/appdirs/query-nonrec -scale 2000000 -allow-nonrecommended-scale -tier nonrecommended -manifest-out <root>/manifests/query-nonrec.json
```

## 本地路径配置

真实 Source 验证与两个测试根的物理路径不写入仓库，从 `Documents/本地/testlab.local.json`（已被
`.gitignore` 忽略）读取；模板见 `Documents/本地/testlab.local.example.json`。`internal/config.Load`
在路径缺失时报出明确错误，不猜测或扫描磁盘。

## 已知框架修复（相对旧 `tools/stage4`）

- `LatencySample` 新增 `PlannedRuns`/`TimedOutRuns`/`NotAttemptedRuns` 字段与 `IdentityOK()`，修复
  此前把"组合截止时间耗尽、从未派发的请求"折叠进 `FailedRuns` 导致
  `successfulRuns+failedRuns != attemptedRuns` 的统计恒等式违反。
- `Report.Save` 在临时文件 rename 前增加显式 `fsync`。
- `testlabseed`/`testlabprobe` 新增 `-tier`/`-allow-nonrecommended-scale` 显式规模保护，默认拒绝
  `>=1,000,000`。

## 已知限制

- `ApplyCatalogCandidateOverlays`（生产 `internal/catalog.Store` 方法）对整个 revision 一次性全量
  处理，不支持增量/分批调用；`testlabseed` 因此仍在内存中累积完整 Overlay facts 后一次性调用，
  500k 规模下约数十 MB，不构成实际内存压力，但不能声称"已分批应用 Overlay"。详见
  `cmd/seed/seed.go` 内的注释与 `Documents/证据/阶段3-4大规模测试归档.md`。
- Gank 的 MEGA 链接/压缩包解压预览隐藏、pixiv/pixivFANBOX/Fantia 的 R-18 Badge、pixiv 的
  `illust_ai_type` Badge 语义标记为 DEFERRED，见 `fixtures/rules/README.md`。
