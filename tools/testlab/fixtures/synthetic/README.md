# 黄金夹具（Golden Fixtures）

每个来源一个最小合成作品目录（`<来源>/creator01/work01/`），演示该来源规则包
（`fixtures/rules/<来源>/bounded-subdir-v1.json`）声明的字段映射能否从一份具体
`metadata.json` 正确解析出 title/author/authorId/description/tags/date/sourceUrl。
每个作品目录只含一张 1x1 像素占位 PNG（`1.png`），不含任何真实媒体内容或真实用户
数据——全部 ID、昵称、文本均为示例值。

`Venera/creator01/work01/` 刻意**不含** `metadata.json`，验证 `metadataRequired=false`
时仍能形成作品，且 title/author 只由目录名（`path.author`）承载。

这些夹具用于规则 Dry Run/Compile/黄金断言（对应
`Documents/指南/02-测试与发布门禁.md`「目标来源覆盖」的黄金夹具矩阵要求），不是
500,000 规模的参考语料——后者由 `tools/testlab/cmd/seed` 通过
`tools/testlab/internal/corpus` 的确定性纯函数程序化生成，不依赖规则引擎。
