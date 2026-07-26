# 全部目标来源规则包覆盖矩阵

> **本目录的手写规则包已不再是真实 Source 验证的规则来源。**
>
> 手写夹具与用户真实配置之间没有任何同步机制，一旦漂移，「规则验证通过」证明的就只是夹具自洽。
> 真实 Source 验证现在走 `testlabrulesimport`：从真实旧配置调用 `internal/rules/legacy.Convert`
> 产出逐平台规则包，再由 `testlabprobe -scenario=source-*` 消费（见 `tools/testlab/README.md`
> 「真实 Source 验证」）。本目录保留下来只作为**单来源结构的最小可读样例**与历史对照，
> 不再声称覆盖真实配置语义。

本目录下每个子目录对应 `Documents/指南/02-测试与发布门禁.md`「目标来源覆盖」一节列出的一个正式
目标来源，包含该来源的规则包（`bounded-subdir-v1.json`）。500,000 规模合成语料的字段生成逻辑见
`tools/testlab/internal/corpus`；合成语料本身不经过规则引擎（`testlabseed` 直接写入
`internal/catalog.Store`）。这些手写包目前只被旧的 `testlabprobe -scenario=media -real-media`
路径使用。

## 通用字段覆盖状态（全部 10 个来源）

| 语义 | 状态 | 表达方式 |
| --- | --- | --- |
| `structure.mode=author_work`（两级目录，`work_directory` glob `*/*`收窄为有界场景的 `*`） | ✅ | `path_match` primitive |
| `work_detection=leaf_with_visible_media` | ✅ | 由生产扫描器保证，规则只声明媒体分类 |
| `metadata_file=metadata.json` | ✅（Venera 除外，见下） | `path_match.config.metadata_file` |
| title/author/authorId/description/tags 字段映射与回退链 | ✅ | `selector`/`fallback`/`metadata_map` |
| 通用 Source URL 回退链（`postUrl→post_url→url→sourceUrl→source_url→permalink→link→source.url`） | ✅ | `fallback` primitive |
| 通用图片/视频扩展名覆盖 | ✅ | 多个 `media_classify` primitive |
| `cover.*`/`.cover.*` 显式封面 | ✅ | `cover_candidate` primitive |
| `.nocover` 禁用封面 | ✅ | `cover_disable_marker` primitive（原语注册表 `gallery-primitives-v2` 起提供，此前记为「未验证」的结论已被推翻） |
| 按名称 glob 隐藏媒体 | ✅ | `media_hidden` primitive（与用户 Overlay 的 `hidden` 是两列，互不覆盖） |
| 自然排序媒体（无显式封面时取第一张） | ✅ | `media_order` primitive + 生产扫描器默认封面选择 |

## 逐来源状态

| 来源 | 规则包 | 特殊字段 | 状态 |
| --- | --- | --- | --- |
| pixiv | `pixiv/bounded-subdir-v1.json`（+ 阶段 3 遗留 `shared/rules/pixiv-v1.json`，未迁移进本目录） | R-18 tag、`illust_ai_type=2` | 基础字段 ✅；手写包未声明 `badge`（原语已可用，见下） |
| pixivFANBOX | `pixivFANBOX/bounded-subdir-v1.json` | authorId 回退 `userId→creatorId` | ✅ |
| Gank | `Gank/bounded-subdir-v1.json` | MEGA 链接/压缩包解压预览隐藏、`1.<ext>` 高优先级封面 | 基础字段 ✅；两条条件隐藏 ⛔ 仍未表达，见 `Gank/README.md` |
| Fantia | `Fantia/bounded-subdir-v1.json` | R-18 tag | 基础字段 ✅；手写包未声明 `badge`（原语已可用，见下） |
| Patreon | `Patreon/bounded-subdir-v1.json` | creator 回退 `full_name→first_name` | ✅ |
| Pawchive | `Pawchive/bounded-subdir-v1.json` | author 五级回退、authorId 二级回退、description 三级回退、date 四级回退 | ✅ |
| X | `X/bounded-subdir-v1.json` | `dateTitle=true`、`twitter`/`x` 双 category | ✅ |
| 微博 | `微博/bounded-subdir-v1.json` | `dateTitle=true` | ✅（本轮补充 title/date 映射，此前阶段 3 遗留版本缺失） |
| 微博_Legacy | `微博_Legacy/bounded-subdir-v1.json` | 独立 rule_set_id/provider_namespace | ✅（规则与微博相同但物理独立，不与微博合并） |
| Venera | `Venera/bounded-subdir-v1.json` | `metadataRequired=false`、`authorKey=path_only` | ✅ |

## Badge/派生语义覆盖矩阵（R-18、`illust_ai_type`、媒体类型识别）

此前记为 ⛔ DEFERRED 的 Badge 语义**已经实现**：原语注册表递增到 `gallery-primitives-v4` 后提供
`badge`（按 `tags`/`metadata_pointer`+`metadata_values`/`media_suffix` 触发）、`media_hidden`、
`cover_disable_marker` 与 `presentation`；publication 侧由 `work_projections.badges_json` 承载。
DEFERRED 的前提（「没有 tag 值 → Badge 类型的映射原语」）不再成立。

| 语义 | 涉及来源 | 状态 | 表达方式 |
| --- | --- | --- | --- |
| 图片/视频类型按扩展名识别 | 全部 10 个 | ✅ | `media_classify` primitive |
| `R-18` tag 作为独立 Badge | pixiv、pixivFANBOX、Fantia | ✅ | `badge` primitive 的 `when.tags` |
| `illust_ai_type=2` 作为独立 Badge | pixiv | ✅ | `badge` primitive 的 `when.metadata_pointer` + `when.metadata_values` |
| 按媒体后缀触发的 Badge | 全部 | ✅ | `badge` primitive 的 `when.media_suffix` |

本目录的手写包尚未逐一补上这些 `badge` 声明——它们不再是真实 Source 验证的规则来源，补齐的价值有限；
真实语义由 `testlabrulesimport` 从旧配置的 `badges` 段转换而来，见 `internal/rules/legacy` 的
`convertBadge`。Gank 的两条条件隐藏仍未转换，原因见下一节与 `Gank/README.md`。

## 目录约定

```text
fixtures/rules/<来源>/bounded-subdir-v1.json   规则包本体
fixtures/rules/<来源>/README.md                仅当该来源存在 DEFERRED 项或其它需要单独说明的差异时提供
```
