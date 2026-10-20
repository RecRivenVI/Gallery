# 合成规则夹具

每个来源目录包含一个最小 `creator01/work01/` 作品，用于验证相邻规则包的字段映射和媒体分类。metadata、ID、昵称、文本和 1×1 PNG 都是合成内容，不含真实用户数据或真实媒体。

`Venera/creator01/work01/` 刻意没有 `metadata.json`，用于覆盖 metadata 可选和路径取值。其它夹具只表达对应规则测试需要的最小差异，不应发展成真实 Source 的长期副本。

这些文件不代表规模语料。规模测试由 `tools/testlab/internal/corpus` 确定性生成。
