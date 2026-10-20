# 查询、搜索与排序

## 查询快照

作品查询必须绑定一个合法 query publication。未指定时服务端选择当前 publication；响应始终返回实际使用的 `queryPublicationId`、Catalog revision、Overlay projection revision、排序/排名协议版本和 dependency set。

后续作品详情、媒体列表、内容确认和 DerivedAsset 请求可以显式携带同一 publication，避免把不同代次的数据拼在一起。

## 授权与范围

查询先取得 publication 的 Source 成员集合，再按当前 Session/API Token 对所需 capability 批量授权。局部 Source deny 只裁剪该 Source，不把整个查询错误地变为空或全局拒绝。Library、Source、Creator 与媒体封面也遵守相同成员和 `media.read` 交集。

## 结构化过滤

`filter` 是服务端解析的 JSON AST：`all`、`any`、`not` 或一个 `field/op/value` 叶子恰好选择一种。未知字段、操作符、类型、尾随 JSON 和非法形状返回 `VALIDATION_ERROR`。

当前实现限制最多 6 层、64 个节点。注册字段覆盖 Library、Source、Provider、Creator、媒体状态与部分 Overlay 字段；精确词表以 `internal/query/filter.go` 和 OpenAPI 为准，不在文档复制维护。

## 搜索

搜索文本先经 Unicode 规范化和受限计划生成，再使用 Catalog 中的 FTS/候选投影。匹配字段包括标题、Creator、Tag 和安全文件名；响应的 `matches` 使用原显示值和 rune 偏移，不暴露绝对或相对路径。

排名协议当前为 v2，按匹配类别与字段优先级产生有界 tier。具体权重仍属于待冻结查询策略；改变协议解释必须递增版本并使旧 cursor 过期。

## 排序

当前公开作品排序：

- `title_asc`、`title_desc`；
- `date_asc`、`date_desc`，缺失日期始终位于有值项之后；
- `progress_asc`、`progress_desc`；
- `name_asc`、`name_desc` 作为标题排序兼容别名收敛到相同查询指纹。

排序使用 publication 中物化的 NaturalSortKey 与稳定 Work ID 作为最终 tie-breaker。客户端不得接收一页后本地重排。

## Cursor

作品 cursor 是 HMAC 签名的严格 base64url claims，绑定：查询指纹、授权 scope、query publication、排序/排名协议、最后键、Work ID、lease 与过期时间。当前 lease 默认 5 分钟。

签名、结构或授权指纹无效返回 `CURSOR_INVALID`；协议升级、过期或不可再读取的 publication 返回可重试 `CURSOR_EXPIRED`。客户端应从第一页重新读取，不修改 cursor 内容。

Creator、Job、安全资源、Binding issue 和结构决策也使用各自绑定查询条件的 keyset cursor，不能跨列表复用。

## Total

响应 total 有三种模式：

- `exact`：预算内精确值；
- `lower_bound`：命中超过预算，只返回下限；
- `omitted`：客户端显式跳过统计。

当前 `TotalBudget` 为 5000，是 PRE_FREEZE 实现值，不是稳定 API SLA。广泛查询必须保持有界，不能为了显示精确页数执行无上限 COUNT。

## Overlay 与读己之写

dependency set 说明本次请求实际依赖哪些 Overlay 字段。写入 Overlay 后，新 projection publication 异步产生；旧查询仍保持旧快照。客户端可以读取 live `favorite`/`progress` 表达即时状态，但不能把它们混入旧 publication 的分页排序。

## 主要实现位置

- `internal/query/`
- `internal/querytext/`
- `internal/contract/query/`
- `internal/catalog/aggregate_cover.go`
- `internal/transport/httpapi/server.go`
