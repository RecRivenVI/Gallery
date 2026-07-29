# tools/testlab

阶段无关、可复用、可持续扩展的 Gallery 测试框架，取代原 `tools/stage4/**`（历史归档见
`Documents/证据/阶段3-4大规模测试归档.md`）。阶段 3、阶段 4 及未来阶段共用本目录下的公共模块与
目录规范，不各自重新实现 Runner、Source guard、进程管理或报告格式。

## 目录结构

```text
tools/testlab/
├── cmd/
│   ├── seed/        构建并发布合成 Catalog（testlabseed，直接调用 internal/catalog.Store）
│   ├── publication-perf/ 完整 Catalog publication 变化矩阵（生产 Store + 原子部分报告）
│   ├── probe/       通过真实 galleryd + HTTP 驱动查询/媒体/真实 Source 场景（testlabprobe）
│   ├── rulesimport/ 把真实旧配置转换成逐平台规则包与产物索引（testlabrulesimport）
│   ├── guard/       独立零写入 guard 快照/校验（testlabguard）
│   └── inventory/   列出两个测试根已有的 manifest/report（testlabinventory）
├── internal/
│   ├── bounds/       显式边界（目录数/文件数/墙钟）与「因边界停止」的如实结论
│   ├── config/       加载 Documents/本地/testlab.local.json
│   ├── corpus/       确定性合成语料生成规则（纯函数，不依赖 internal/*）
│   ├── environment/  Session 建立（一次性配对）
│   ├── legacyrules/  调用 internal/rules/legacy.Convert 的转换器（testlab 内唯一导入 internal/rules 的包）
│   ├── process/      galleryd 子进程生命周期
│   ├── report/       Finding/LatencySample/Report，脱敏与原子持久化
│   ├── ruleindex/    转换产物索引的读写、平台脱敏代号与 rule_set_id 派生（纯标准库）
│   ├── seeding/      生产式合成 publication 构建（CLI 与自动 smoke 共用）
│   └── sourceguard/  真实 Source 只读清单、有界枚举与零写入校验
├── stages/
│   ├── sourcelab/     真实只读 Source 的 orchestrator：转换产物驱动、全程 guard、有界/全量/续跑模式
│   ├── stage3/        生产契约 smoke：扫描档案、publication、Job/Attempt 恢复与 Source 零写入
│   ├── stage4/
│   │   ├── smoke_test.go 普通 go test 持续入口（1k、真实 bootstrap/loopback HTTP）
│   │   ├── query/     结构化过滤/搜索/排序/Ranking/Total/Cursor/性能矩阵
│   │   └── media/     真实/合成 Source 建立、按需确认、Range/ETag、DerivedAsset
│   └── stage5/
│       └── security/  LAN Owner、Session、Grant、API Token、路径/metadata/媒体/恢复攻击输入与安全报告闭环
├── fixtures/
│   ├── rules/       手写规则包样例，已不是真实 Source 验证的规则来源，见 fixtures/rules/README.md
│   └── synthetic/   小型合成目录夹具
└── schemas/         （规则/结果 Schema 以 internal/rules、internal/report 为唯一权威，本目录只放跨阶段共用的补充 Schema，避免重复定义）
```

`stages/stage3/smoke_test.go` 直接组合生产 `application`、`scanner`、`hashjob`、`jobs`、`catalog`
与 `recovery` 契约，在临时 AppDirs 和合成只读 Source 上复核 `index`/`incremental`/`verify`、完整
SHA-256、publication 不可变性、同一 Job 多 Attempt 恢复与 Source guard。它是可重复的正确性 smoke，
不替代 `Documents/证据/验证记录.md` 和 `Documents/证据/阶段3-4大规模测试归档.md` 中的真实规模证据，
也不代表 HDD、SMB/NAS 或正式性能门禁已经完成。

`stages/stage4/smoke_test.go` 通过 `go test ./tools/testlab/stages/stage4` 自动构建 1,000 Work 的确定性
publication，启动生产 `bootstrap.RunWithReady`，并经真实 loopback HTTP/生成客户端执行 40 项查询、6 项
Cursor 与 20 项媒体/DerivedAsset finding；`creator.id` 使用另一个真实扫描的小型 Source 建立 control/Catalog
双库身份后逐 ID 验证。测试使用临时 AppDirs 和合成 Source，锁定完整 finding 名集合、
脱敏报告，以及首次 index scan 后至按需确认/Derived 完成期间的 Source 零写入；不运行性能矩阵，也不替代 500,000 Reference、HDD/SMB/NAS 或真实 Source 门禁。

## 规模分级

见 `Documents/指南/02-测试与发布门禁.md`「正式验证规模分级」：`smoke`(1k)/`integration`(10k)/
`preflight`(100k)/`reference`(500k) 是标准 Gate；`≥1,000,000` 是显式启用的非推荐诊断场景。

```powershell
# smoke（1k）
& $env:GALLERY_GO run ./tools/testlab/cmd/seed -approot <root>/appdirs/query-1k -scale 1000 -tier smoke -manifest-out <root>/manifests/query-1k.json
& $env:GALLERY_GO run ./tools/testlab/cmd/probe -go $env:GALLERY_GO -repo . -approot <root>/appdirs/query-1k -log <root>/logs/query-1k.log -scenario all -manifest <root>/manifests/query-1k.json -results-out <root>/reports/query-1k.json -tier smoke

# 多 Source preflight（100k；显式覆盖 cloneUnchangedSources）
& $env:GALLERY_GO run ./tools/testlab/cmd/seed -approot <root>/appdirs/query-100k -scale 100000 -sources 10 -tier preflight -manifest-out <root>/manifests/query-100k.json

# 正式 Query Reference 语料；reference 标签会在构建前强制 500k、10 个权威目标来源槽位和每 Work 两条 Creator 关系
& $env:GALLERY_GO run ./tools/testlab/cmd/seed `
  -approot <root>/appdirs/query-reference-500k `
  -scale 500000 -sources 10 -tier reference `
  -manifest-out <root>/manifests/query-reference-500k.json

# 热缓存、并发查询矩阵；每个组合显式预热，报告逐项记录 limit/concurrency/runs/P95/P99
& $env:GALLERY_GO run ./tools/testlab/cmd/probe `
  -go $env:GALLERY_GO -repo . `
  -approot <root>/appdirs/query-reference-500k `
  -log <root>/logs/query-reference-500k-warm.log `
  -scenario perf -perf-matrix full -perf-cache warm -perf-warmup-runs 3 `
  -runs 30 -perf-p99-runs 100 -perf-scenario-timeout 30m `
  -manifest <root>/manifests/query-reference-500k.json `
  -results-out <root>/reports/query-reference-500k-warm.json -tier reference

# 冷进程矩阵；每个组合前重启 galleryd，但不清空操作系统文件缓存，不能冒充冷存储读
& $env:GALLERY_GO run ./tools/testlab/cmd/probe `
  -go $env:GALLERY_GO -repo . `
  -approot <root>/appdirs/query-reference-500k `
  -log <root>/logs/query-reference-500k-cold-process.log `
  -scenario perf -perf-matrix full -perf-cache cold-process -perf-warmup-runs 0 `
  -runs 30 -perf-p99-runs 100 -perf-scenario-timeout 60m `
  -manifest <root>/manifests/query-reference-500k.json `
  -results-out <root>/reports/query-reference-500k-cold-process.json -tier reference

# 任一窗口因 -perf-scenario-timeout 在组合边界中止后，以新的日志文件继续同一报告；
# warm/cold-process 分别重复自己的原命令，只增加 -resume，scenario timeout 可按维护窗口调整。
& $env:GALLERY_GO run ./tools/testlab/cmd/probe `
  -go $env:GALLERY_GO -repo . `
  -approot <root>/appdirs/query-reference-500k `
  -log <root>/logs/query-reference-500k-warm-window02.log `
  -scenario perf -resume -perf-matrix full -perf-cache warm -perf-warmup-runs 3 `
  -runs 30 -perf-p99-runs 100 -perf-scenario-timeout 45m `
  -manifest <root>/manifests/query-reference-500k.json `
  -results-out <root>/reports/query-reference-500k-warm.json -tier reference

# ≥1,000,000（非推荐诊断场景，必须显式确认）
& $env:GALLERY_GO run ./tools/testlab/cmd/seed -approot <root>/appdirs/query-nonrec -scale 2000000 -allow-nonrecommended-scale -tier nonrecommended -manifest-out <root>/manifests/query-nonrec.json
```

`reference` 不是自由文本标签：seed 会在创建 AppRoot 前拒绝非 500,000、非 10 Source 的参数，并
实际生成每 Work 两条 Creator 关系；manifest 与 report 同时记录十目标来源代号和关系数。probe 在任何
计时请求前先经真实 HTTP 核对当前 active publication/Catalog revision 与 manifest 一致，错配 AppRoot
会直接失败而不会开始矩阵。这个预检不进入分位数；warm 模式随后逐组合显式预热，cold-process 模式逐
组合重启 `galleryd`，但两者都不清空操作系统文件缓存。

查询矩阵从启动时的 `0/N`、每个完整组合到终态都原子写入同一个 `results-out`。分窗到期会以非零
退出码和失败 terminal finding 明示“尚未完成”；后续 `-resume` 只保留完整成功的组合前缀并继续下一项。
组合顺序/次数、单请求与单组合超时、缓存模式、warmup、实测主机/存储、语料、当前 query publication
或 Catalog revision 任一漂移都会 fail-closed，既有失败组合也不能靠续跑洗成成功。只有
`-perf-scenario-timeout` 可以跨窗口调整，因为它只定义本次维护窗口；每个窗口应使用新的 `-log` 文件，
避免下一次进程启动截断上一窗口日志。已完整成功的报告再次 `-resume` 是 no-op。

## Publication 变化矩阵

`publication-perf` 不只计时指针切换：每个样本都通过生产
`BeginCandidate → Stage → ApplyCatalogCandidateOverlays → ValidateCandidate → Publish`，并复核完整
WorkProjection、每 Work 恰好两条带 role/ordinal 的 WorkCreator 关系、MediaProjection、SourceMedia、ContentBlob、FileLocation、FTS 和
search candidate。基线固定为多 Source，主 Source 持有 50% 作品；因此单次真实 Source
publication 可以精确改变全库 1%/10%/50% 的 WorkProjection，不会把「主 Source 内变化比例」
写成「全库变化比例」。10 Source 形状会按规范顺序把槽位绑定为 Pixiv、Pixiv FANBOX、
Gank、Fantia、Patreon、Pawchive、X、微博、微博 Legacy 和 Venera 的非敏感代号，并写入报告。

```powershell
# 语义/报告预检；数值不构成正式门禁结论
& $env:GALLERY_GO run ./tools/testlab/cmd/publication-perf `
  -approot <root>/appdirs/publication-preflight `
  -report-out <root>/reports/publication-preflight.json `
  -scale 1000 -sources 10 -primary-share 0.50 `
  -ratios 0.01,0.10,0.50 -samples 2 -tier preflight

# 正式 Reference 形状；任一参数降级都会在运行前被拒绝
& $env:GALLERY_GO run ./tools/testlab/cmd/publication-perf `
  -approot <root>/appdirs/publication-reference-500k `
  -report-out <root>/reports/publication-reference-500k.json `
  -scale 500000 -sources 10 -primary-share 0.50 `
  -ratios 0.01,0.10,0.50 -samples 20 -tier reference

# 中断后使用同一 AppRoot 与原子报告继续剩余样本；环境或矩阵漂移会 fail-closed
& $env:GALLERY_GO run ./tools/testlab/cmd/publication-perf `
  -approot <root>/appdirs/publication-reference-500k `
  -report-out <root>/reports/publication-reference-500k.json `
  -scale 500000 -sources 10 -primary-share 0.50 `
  -ratios 0.01,0.10,0.50 -samples 20 -tier reference -resume
```

首次运行的 `-approot` 必须不存在或为空，`-report-out` 必须在其外部，避免报告自身污染空间水位。
工具在 baseline 和每个完成样本后原子刷新报告；`-resume` 会核对原报告、当前 AppRoot、主机/存储、
完整两关系候选形状和已完成样本，收敛遗留 staging candidate 后只执行剩余样本。若进程在 Publish 后、
报告 checkpoint 前中断，已发布但没有完整计时的那次不会冒充有效样本，续跑会使用更高 revision 重测。
首跑若通过 ProcessorAffinity 限定可见 CPU，续跑必须使用相同亲和性；否则环境指纹会以
`cpuLogicalCores` 漂移拒绝，防止把不同资源上限的样本混入同一份性能报告。
报告同时记录 `goMaxProcs`；只改 `GOMAXPROCS` 也会被续跑指纹拒绝。
`reference` 强制 500,000 Work、10 Source、50% 主 Source、三个正式比例与每比例至少
20 个完整样本；报告分开记录 Begin/Stage/Overlay/Validate/Publish/GC/checkpoint、峰值空间、
旧快照在构建各边界仍可读及 nearest-rank P50/P95。它不清空 OS 文件缓存，必须按报告的
`cacheState=warm` 解读；HDD/SMB/NAS、完整哈希吞吐和 Degradation 仍是独立门禁。

## 真实 Source 验证（转换产物驱动）

真实 Source 验证不使用 `fixtures/rules/` 下的手写规则包：手写夹具与用户真实配置之间没有任何同步
机制，一旦漂移，「规则验证通过」证明的只是夹具自洽。正式路径是两步。

**第一步：转换真实旧配置。** 旧配置路径必须显式给出，工具不猜测也不扫描磁盘；产物含真实平台根
路径，因此输出目录必须在授权测试根内，写进任何 Git 工作树会被直接拒绝。

```powershell
& $env:GALLERY_GO run ./tools/testlab/cmd/rulesimport `
  -legacy-config <旧配置 gallery-rules.json 的绝对路径> `
  -out-dir <测试根>/rules-import
```

标准输出只有平台**代号**（`p-xxxxxxxx`，由平台 ID 稳定派生）、原语数与未转换字段聚合，不打印平台名、
路径或配置内容。记下需要验证的那个代号。

**第二步：按模式驱动真实 Source。** 每个模式都会在每次触碰 Source 的操作前后自动做 guard 快照与
校验；任一阶段检出写入即整轮以非零退出码失败。`-approot` 与 `-log` 位于授权测试根，`-state` 是续跑
状态文件（只含 ID、计数与折叠哈希）。

```powershell
$common = @(
  "-go", $env:GALLERY_GO, "-repo", ".",
  "-approot", "<测试根>/appdirs/<代号>",
  "-log", "<测试根>/logs/<代号>.log",
  "-rules-index", "<测试根>/rules-import",
  "-platform-code", "<代号>",
  "-state", "<测试根>/state/<代号>.json"
)

# 1) 有界模式：显式上限，超限即停并如实报告「因边界停止」
& $env:GALLERY_GO run ./tools/testlab/cmd/probe @common -scenario source-bounded `
  -max-dirs 200 -max-files 2000 -max-wall-clock 10m -max-media-items-bounded 12 `
  -storage-class hdd -results-out <测试根>/reports/<代号>-bounded.json

# 2) 全量 index：完整枚举 + metadata 解析 + publication；SSD 全量内容哈希
& $env:GALLERY_GO run ./tools/testlab/cmd/probe @common -scenario source-index `
  -storage-class ssd -results-out <测试根>/reports/<代号>-index.json

#    HDD 平台改为只对有界子集哈希
& $env:GALLERY_GO run ./tools/testlab/cmd/probe @common -scenario source-index `
  -storage-class hdd -max-media-items-bounded 32 -results-out <测试根>/reports/<代号>-index.json

# 3) 续跑证明：两次 incremental，确认时间折叠哈希不变即「没有重做已完成工作」
& $env:GALLERY_GO run ./tools/testlab/cmd/probe @common -scenario source-incremental `
  -storage-class ssd -results-out <测试根>/reports/<代号>-incremental.json

# 4) 对照组：verify 必须真正推进确认时间，且内容身份不漂移
& $env:GALLERY_GO run ./tools/testlab/cmd/probe @common -scenario source-verify `
  -storage-class ssd -results-out <测试根>/reports/<代号>-verify.json
```

要点：

- **`-storage-class` 决定内容哈希范围**：`ssd` → 全量（生产 `incremental` 档案对全部媒体建立
  ContentBlob）；其它值 → 有界（只对前 `-max-media-items-bounded` 个媒体做按需确认）。可用
  `-hash-scope full|bounded` 显式覆盖。
- **`-max-wall-clock` 是硬边界**：扫描阶段超时会主动取消 Scan Job；`source-bounded` 随后的按需
  确认阶段另外以同一数值作为全部目标共享的总墙钟，而不是让每个媒体分别获得一份超时预算。确认阶段
  触顶时会取消当前 Job，并要求 30 秒内收敛终态；取消迟滞同样以失败报告，不会把被截断的运行说成跑完了。
- **guard 内容哈希默认关闭**。`-guard-hash-content` 能发现「大小与 mtime 都不变的原地改写」，但必须
  同时给出 `-guard-max-hash-files` 或 `-guard-max-hash-bytes`，否则拒绝启动；触顶时报告写明
  `hashStoppedByBound`，不得当作已全量校验内容。
- **`index` 档案只对首次扫描有效**。Source 已发布后再跑 `source-index` 会走「复用既有索引」分支并
  如实报告，而不是失败或偷偷改跑 `incremental`。
- 报告只含平台代号、计数、字节数、耗时、错误码分类与折叠哈希；不含绝对路径、目录名、metadata 原文
  或完整 URL。转换产物索引本身含真实根路径，属本地制品，不得提交。

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
- `testlabseed -sources N` 让每个 Source 依次走生产 `BeginCandidate/Stage/Overlay/Validate/Publish`；
  Manifest 与 probe 的 `corpus` 区段分别记录逐 Source Begin clone、完整 Validate 与短 Publish 耗时。
  `GALLERY_TESTLAB_SEED_SOURCES` 只保留为旧脚本兼容入口。

### Source guard 的三处安全修复

1. **链接根不再让 guard 空转。** `sourceguard.Walk` 此前用 `filepath.Walk`，其根判定走 `os.Lstat`；
   Windows junction 在那里报告为 `fs.ModeIrregular` 且 `IsDir()=false`，于是遍历只产出根自身一条，
   清单恒为 `fileCount=0/dirCount=0`。空清单与空清单自比必然相等，`testlabguard verify` 会打印
   `PASS` 却什么都没有守护——数据量最大的两个 HDD 平台恰好都是 junction 根。现在根按 `os.Stat`
   （跟随）判定并正常递归；子树内部的链接仍不跟随，但**作为独立条目计入清单**，使「链接被替换成
   真实目录」这类改动改变 guard 摘要。
2. **空清单一律判失败。** `Walk`/`SaveManifest`/`LoadManifest` 都拒绝 0 文件 + 0 目录 + 0 链接的
   清单，防止本类缺陷再次静默复发。
3. **落盘清单不再持久化真实目录名。** 逐条 `relativePath`（即真实作者名与作品目录名）换成其
   SHA-256 摘要；这些名字对验证毫无作用（比较只用计数与 guard 摘要），却是纯粹的泄露面。摘要仍逐条
   唯一，因此 `verify` 现在能回答「新增/删除/修改了多少条」——修复前这些条目根本读不回来。

遍历改用 `os.Lstat` 而不是 `DirEntry.Info()` 还顺带修掉一处假阳性：Windows 上 ReadDir 返回的属性取自
父目录的目录项缓存，子目录 mtime 在那里惰性刷新，同一棵未被修改的树连续两次遍历会得到不同的 guard
摘要。

## 已知限制

- `ApplyCatalogCandidateOverlays`（生产 `internal/catalog.Store` 方法）对整个 revision 一次性全量
  处理，不支持增量/分批调用；`testlabseed` 因此仍在内存中累积完整 Overlay facts 后一次性调用，
  500k 规模下约数十 MB，不构成实际内存压力，但不能声称"已分批应用 Overlay"。详见
  `cmd/seed/seed.go` 内的注释与 `Documents/证据/阶段3-4大规模测试归档.md`。
- Gank 的 MEGA 链接/压缩包解压预览隐藏依赖同目录兄弟文件这一目录级事实，当前原语与 CEL 上下文
  不提供该输入，转换器把它登记为未转换项而不是猜测，见 `fixtures/rules/README.md` 与
  `fixtures/rules/Gank/README.md`。
- 转换产物**不得**把旧配置的 `metadata.author_id` 喂给作品的 `external_id`：同一作者的多个作品共享
  同一个 author_id，一旦它成为作品 external_id，扫描解析阶段就会命中 `duplicate_external_id` 并以
  `BINDING_REVIEW_REQUIRED` 阻塞该 Source 的 publication，任何「一个作者有多个作品」的真实平台都会
  在第一次扫描卡住。这条由 `internal/legacyrules` 的
  `TestConvertNeverMapsAuthorIDToWorkExternalID` 与 `stages/sourcelab` 的端到端用例（合成树刻意让
  同一作者拥有多个作品）里外两层保护。
- `sourcelab` 的有界模式只能对**枚举**与**墙钟**设界。生产扫描器总是扫描整个 Source 根，规则的
  `work_directory` glob 又固定为 `*/*`，无法只让它扫描根下的一部分作者目录；用链接做有界镜像也
  不可行，因为扫描器按 `LINK-1` 裁决跳过链接、不跟随。因此「有界」的真实含义是：先做有界普查，再在
  墙钟上限内跑扫描、超限主动取消，而不是让扫描只看一部分目录。
- 单媒体按需确认当前仍会为冻结身份重新执行整个 Source 的 discovery/规则解析；因此
  `-max-media-items-bounded` 只限制确认目标数量，不等于把前置枚举工作量按同一数量裁剪。大 Source 必须
  同时保留 `-max-wall-clock`，并把逐目标重复扫描成本作为独立性能结论。
