# ADR-0005：查询协议

- 状态：Accepted
- 日期：2026-07-16

## 上下文

大型 Catalog 需要稳定分页、授权裁剪和跨更新一致性。传统 offset 分页、客户端排序和无界精确 COUNT 会在数据变化或大结果集下产生重复、遗漏和不可控成本。

## 决策

- 查询绑定不可变 query publication。
- 过滤使用服务器解析的封闭 JSON AST，未知字段和操作符拒绝。
- 搜索、排序、命中表达、total 和 cursor 都带显式协议版本。
- 分页使用 HMAC 签名 keyset cursor，绑定查询指纹、授权 scope、publication、最后键和 lease。
- total 支持 `exact`、`lower_bound` 与 `omitted`，广泛查询不得强制无界 COUNT。
- 服务端负责排序与最终 tie-breaker，客户端不得本地重排一页结果。
- 查询响应返回 dependency set，说明本次请求实际依赖的 Overlay/资源字段。

## 理由

publication 与 keyset cursor 保持跨页一致；签名和授权指纹阻止 cursor 跨查询或越权复用；有界 total 让普通浏览不被精确页数拖垮。

## 影响

- 排名、排序键或 cursor claims 的解释变化必须递增协议版本。
- 旧协议 cursor 在升级后返回可恢复的过期错误。
- API Freeze 前可以调整预算，但必须保留三态 total 的表达。
- 客户端应支持从第一页刷新，而不是解析或修补 cursor。

## 重新审议

若稳定快照租约在真实规模上成本不可接受，应基于测量重新设计 lease 或 pagination，但不能退回 offset 与客户端排序。
