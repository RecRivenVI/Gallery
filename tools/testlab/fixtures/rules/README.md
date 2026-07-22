# 全部目标来源规则包覆盖矩阵

本目录下每个子目录对应 `Documents/指南/02-测试与发布门禁.md`「目标来源覆盖」一节列出的一个正式
目标来源，包含该来源的规则包（`bounded-subdir-v1.json`，用于有界真实 Source 验证）。500,000 规模
合成语料的字段生成逻辑见 `tools/testlab/internal/corpus`；合成语料本身不经过规则引擎（`testlabseed`
直接写入 `internal/catalog.Store`），规则包只在**真实 Source 有界验证**（`testlabprobe -scenario=media
-real-media`）中被实际编译、绑定和执行。

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
| `.nocover` 禁用封面 | ❓ 未验证 | 规则 Schema 未见对应 primitive kind，需要下一轮确认生产扫描器是否在 primitive 之外的固定约定层处理该文件名 |
| 自然排序媒体（无显式封面时取第一张） | ✅ | `media_order` primitive + 生产扫描器默认封面选择 |

## 逐来源状态

| 来源 | 规则包 | 特殊字段 | 状态 |
| --- | --- | --- | --- |
| pixiv | `pixiv/bounded-subdir-v1.json`（+ 阶段 3 遗留 `shared/rules/pixiv-v1.json`，未迁移进本目录） | R-18 tag、`illust_ai_type=2` | 基础字段 ✅；R-18/AI 标注 Badge 语义 ⛔ DEFERRED，见下 |
| pixivFANBOX | `pixivFANBOX/bounded-subdir-v1.json` | authorId 回退 `userId→creatorId` | ✅ |
| Gank | `Gank/bounded-subdir-v1.json` | MEGA 链接/压缩包解压预览隐藏、`1.<ext>` 高优先级封面 | 基础字段 ✅；两条特殊隐藏规则 ⛔ DEFERRED，见 `Gank/README.md` |
| Fantia | `Fantia/bounded-subdir-v1.json` | R-18 tag | 基础字段 ✅；R-18 Badge ⛔ DEFERRED |
| Patreon | `Patreon/bounded-subdir-v1.json` | creator 回退 `full_name→first_name` | ✅ |
| Pawchive | `Pawchive/bounded-subdir-v1.json` | author 五级回退、authorId 二级回退、description 三级回退、date 四级回退 | ✅ |
| X | `X/bounded-subdir-v1.json` | `dateTitle=true`、`twitter`/`x` 双 category | ✅ |
| 微博 | `微博/bounded-subdir-v1.json` | `dateTitle=true` | ✅（本轮补充 title/date 映射，此前阶段 3 遗留版本缺失） |
| 微博_Legacy | `微博_Legacy/bounded-subdir-v1.json` | 独立 rule_set_id/provider_namespace | ✅（规则与微博相同但物理独立，不与微博合并） |
| Venera | `Venera/bounded-subdir-v1.json` | `metadataRequired=false`、`authorKey=path_only` | ✅ |

## Badge/派生语义覆盖矩阵（R-18、`illust_ai_type`、媒体类型识别）

| 语义 | 涉及来源 | 状态 | 原因 |
| --- | --- | --- | --- |
| 图片/视频类型按扩展名识别 | 全部 10 个 | ✅ | `media_classify` primitive，见上表 |
| `R-18` tag | pixiv、pixivFANBOX、Fantia | ⛔ DEFERRED | Tag 本身可由 `metadata_map` 的 `tags` 字段正常提取（不需要特殊表达）；"R-18 作为可查询/可展示的独立 Badge"（区别于普通 tag 字符串的结构化布尔标记）目前没有证据表明规则 Schema 或查询字段注册表提供了"tag 值 → Badge 类型"的映射原语，本轮未在 Provider 代码中新增特例分支强行实现，标记为 DEFERRED，等待产品侧先确认 Badge 是否作为正式查询字段登记 |
| `illust_ai_type=2` 元数据保留 | pixiv | 🟡 部分表达 | `metadata_map` 可以把任意 JSON Pointer 映射进 `tags`/`description` 等既有字段作为文本保留，但"保留为独立可查询字段"同样没有对应的查询字段注册表证据，标记为 DEFERRED；若只要求"扫描后仍能在原始 metadata 中找回该字段"（不要求可查询），当前 `path_match.metadata_file` 已经满足 |

DEFERRED 项不得被硬编码进 Provider 代码（违反"规则是 Source 差异的唯一解释入口"边界），需要在规则
Schema 或查询字段注册表侧先有对应原语后，再回到本目录补齐规则表达与黄金夹具测试。

## 目录约定

```text
fixtures/rules/<来源>/bounded-subdir-v1.json   规则包本体
fixtures/rules/<来源>/README.md                仅当该来源存在 DEFERRED 项或其它需要单独说明的差异时提供
```
