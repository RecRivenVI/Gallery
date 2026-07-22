# Gank 规则包

## 覆盖状态

| 语义 | 状态 | 说明 |
| --- | --- | --- |
| title/author/authorId/description/tags/date 字段映射 | ✅ 已表达 | 见 `bounded-subdir-v1.json` |
| 通用 Source URL 回退链 | ✅ 已表达 | `postUrl → post_url → url → sourceUrl → source_url → permalink → link → source.url` |
| 通用媒体扩展名/封面规则 | ✅ 已表达 | 覆盖图片/视频/压缩包扩展名与 `cover.*`/`.cover.*` |
| MEGA 链接触发的解压预览隐藏（`^[1-9]\.[^.]+$`） | ⛔ DEFERRED | 见下 |
| 压缩包触发的解压预览隐藏（`^[1-9]\.[^.]+$`） | ⛔ DEFERRED | 见下 |
| `1.<ext>` 高优先级静态封面候选 | 🟡 部分表达 | `cover_candidate` glob `1.*` score 90（低于显式 `cover.*` 的 100），**不依赖**上述两条隐藏条件是否成立 |

## DEFERRED 原因

规则引擎的 `condition` primitive（`scope: "media"`, `effect: "hide"`）按**单个媒体文件**逐一求值 CEL
谓词，求值上下文只包含该作品的 `metadata`（work 级 `metadata.json`）与当前候选文件的 `path`/`size`，
不包含"该作品目录下还有哪些其它文件"这一目录级事实。因此：

- **压缩包触发**（"当作品目录含 zip/rar/7z/... 时，`^[1-9]\.[^.]+$` 的预览媒体默认隐藏"）本质上是一个
  跨文件的目录级条件（"是否存在某个兄弟文件匹配另一个 glob"），当前 `condition` primitive 的求值上下文
  无法表达这种"看到其它候选文件"的谓词，本轮未在 Provider 代码中新增特例分支来强行实现（这会违反
  "规则是 Source 差异的唯一解释入口"的产品边界），因此标记为 DEFERRED。

- **MEGA 链接触发**（"当 metadata 中 `content`/`links` 包含 MEGA 链接时，同样隐藏"）理论上可以通过
  `condition` 引用一个检查 `metadata.content`/`metadata.links` 是否包含 MEGA 域名字符串的 CEL 谓词
  表达，但本轮未能在时间预算内确认 Gallery 受限 CEL Profile 是否注册了 `has()`（可选字段存在性）和
  `matches()`（正则）宏/函数供这种字符串包含判断使用——`internal/rules/cel.go` 中存在对 `.matches("…")`
  调用的正则提取（用于成本估算），暗示 `matches()` 很可能可用，但本轮未编写并实际运行编译/Dry Run
  测试来确认，因此同样标记为 DEFERRED，而不是提交一段未经验证、可能编译失败或行为不符预期的 CEL
  表达式。

## 后续验证建议

下一轮应：
1. 用最小合成夹具（一个含 `metadata.json{"content":"...mega.nz..."}` 与 `1.png`/`2.png` 的作品目录）
   实际调用规则 Dry Run/Compile 接口，确认 `condition` + CEL `matches()`/`has()` 是否按预期工作；
2. 若确认可行，把 MEGA 链接规则从 DEFERRED 提升为已表达并新增黄金夹具测试；
3. 压缩包触发规则若确需支持，应作为 `internal/rules` 规则原语的正式扩展提案（新增能访问同级候选文件
   的 primitive 或 condition scope），走产品规则系统演进流程，不在测试框架职责范围内。
