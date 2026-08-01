# Legacy schema v3 转换覆盖

本文件说明 `internal/rules/legacy` 的当前转换边界。它是代码旁的维护摘要，不复制 RulePackage Schema 或全部字段实现；最终行为以 `convert.go`、`platform.go`、`unknown.go` 和测试为准。

## 转换入口

`Convert` 只接受 `schema_version: 3`，并为每个启用平台生成一份规范 JSON 规则包，同时返回：

- `Packages`：按平台 ID 索引的规则包；
- `SourceRoots`：显式声明的只读 Source 根；
- `FileRoots`：启用的文件根；
- `Unconverted`：未识别、无法表达或与 Gallery 固定语义不同的字段及原因。

转换是一次性导入，不是运行时兼容层。产物仍须由正式规则编译器和服务端生命周期校验。

## 已承接的主要语义

- Library 级 metadata 文件、媒体扩展名、隐藏名称、显式封面和禁用标记；
- 文件根声明；
- 展示时区、显示格式、排序选项和平台 presentation；
- `author_work` 两级目录及作品目录名标题回退；
- metadata 的标题、作者、作者 ID、描述、标签、日期和 Source URL 取值链；
- 图片/视频分类、媒体顺序、封面候选、Badge；
- `$path.datetime` 的受限目录日期模式；
- 未声明字段的递归差集登记。

Gallery 固定以 UTC 存储时刻、保留路径 code point、按第一张可见媒体回退封面，并由 Catalog 实现聚合封面。旧配置取值与这些规则一致时属于等价承接；不一致时进入 `Unconverted`。

## 明确限制

| 语义 | 当前处理 |
| --- | --- |
| `$path.author` 作为作者稳定标识 | 无法转换；`stable_key` 只接受 metadata pointer |
| `$path.author` 作为其它字段取值 | 只有作者名字可由 `path_capture` 承接；其它取值不猜测 |
| 多项作者 ID fallback | 无法转换为只接受单 pointer 的 `stable_key` |
| 子目录媒体 | 扫描器只读取作品目录的直接子文件；请求递归时登记差异 |
| 标题、描述展示型归一化 | 不转换；Gallery 保留来源文本 |
| 花括号字符串标签列表 | 不拆分为多个标签，登记语义差异 |
| 同目录兄弟文件条件 | 当前 CEL 输入没有目录清单，无法表达 |
| metadata 文本条件隐藏 | 输入形态未知时不生成可能在真实数据上报错的 CEL |
| 带无法表达条件的封面候选 | 整条候选不生成，回退到显式封面或第一张可见媒体 |
| 不能安全降级的正则 | 拒绝近似转换 |

## 未识别字段

`Config` 是显式部分映射。`unknown.go` 将原始 JSON 树与声明类型递归比较，未声明字段写入 `Unconverted`，避免 `encoding/json` 静默丢弃。`json.RawMessage` 表示由专用逻辑处理的开放子树，不在通用差集中展开。

调用方必须审查全部 `Unconverted` 项。“转换函数返回成功”只表示产物可生成，不表示旧配置每项语义均被承接。

## 维护要求

- 新增或改变 legacy 字段时同时更新声明、转换逻辑、差集测试和黄金结果；
- 不能表达的语义保留具体 JSON 路径和原因，不用默认值伪装成功；
- 不从真实 metadata 猜测字段类型，不建立平台专用 provider 分支；
- 完整 RulePackage 格式只维护在 `internal/rules/rule-package.schema.json` 和 Go 实现中。
