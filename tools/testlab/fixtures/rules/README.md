# 规则包夹具

本目录保存十个目标来源的最小手写规则包。它们用于结构、Compile、Dry Run 和合成媒体测试，不是用户真实配置的镜像，也不是正式真实 Source 验证的规则来源。

真实 legacy schema v3 配置必须通过 `tools/testlab/cmd/rulesimport` 调用 `internal/rules/legacy.Convert`，再由 Source 场景消费转换产物。手写夹具与真实配置没有同步机制，不能用“夹具通过”证明真实语义完整。

## 目录

每个来源的 `bounded-subdir-v1.json` 描述一个两级 `author_work` 合成目录：

- `pixiv`
- `pixivFANBOX`
- `Gank`
- `Fantia`
- `Patreon`
- `Pawchive`
- `X`
- `微博`
- `微博_Legacy`
- `Venera`

对应输入位于 [`../synthetic/`](../synthetic/README.md)。500,000 等规模语料由 `tools/testlab/internal/corpus` 生成并直接写入生产 Catalog Store，不经过这些规则包。

## 共同覆盖

夹具覆盖作品目录匹配、常用 metadata pointer/fallback、图片与视频分类、显式封面、禁用封面标记、隐藏名称、自然媒体顺序和 presentation 字段。是否支持某项 primitive 以当前 rule package Schema、primitive registry 和对应测试为准；本 README 不复制完整 Schema。

Gank 的目录级条件隐藏和 metadata 文本形态限制见 [`Gank/README.md`](Gank/README.md)。其它来源只有在存在无法从规则包本体看出的限制时才增加局部说明。
