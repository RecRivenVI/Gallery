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

- **MEGA 链接触发**（"当 metadata 中 `content`/`links` 包含 MEGA 链接时，同样隐藏"）**技术上可以表达**。
  此前这里写的理由是「未能确认受限 CEL Profile 是否注册了 `has()` 与 `matches()`」——该疑问已经解决：
  `newCELRuntime` 是普通的 `cel.NewEnv` 加六个 dyn 变量（`source`/`path`/`file`/`metadata`/`candidate`/
  `params`），标准宏库默认可用，`has()` 与 `matches()` 都能用；真正的约束是 AST 256 节点、正则 512
  字符、成本 10000、执行 10 毫秒，本谓词远在其内。`metadata` 与 `file.path` 也都在求值上下文里，
  `condition{scope: media, effect: hide}` 的求值路径同样已经实现。

  因此本项**不再因「能力未知」而延期**，而是因为**取值形态未观察**：忠实移植需要知道被匹配的
  `content`/`links` 在真实 metadata 里是字符串还是数组——`matches()` 作用于数组会在求值期报错，而
  CEL 谓词求值出错会中断整个 Source 的扫描。取值形态只能从真实 metadata 观察，而那超出本轮允许的
  只读观察范围（本轮只观察目录名形状，不读 metadata 内容）。**凭猜写谓词比不写更糟**：不写只是少一条
  隐藏规则，写错则让整个平台扫不动。

## 后续验证建议

下一轮应：
1. 在允许读取 metadata 结构（只看类型形态，不看取值内容）的有界观察中，确认 `content`/`links` 是
   字符串还是数组；
2. 据此写出类型安全的谓词（必要时用 `type()` 或 `has()` 分支同时容纳两种形态），用最小合成夹具跑
   Compile 与 Dry Run，再把本项从 DEFERRED 提升为已表达并新增黄金夹具测试；
3. 压缩包触发规则若确需支持，应作为 `internal/rules` 规则原语的正式扩展提案（新增能访问同级候选文件
   的 primitive 或 condition scope），走产品规则系统演进流程，不在测试框架职责范围内。
