# ADR-0010：已确认媒体正文读取

- 状态：Accepted
- 日期：2026-07-26

## 上下文

媒体路径会变化，文件也可能在扫描后被替换。每个 Range 请求都重读全文件会破坏流式性能，而只相信路径、大小和 mtime 又不足以保护完整读取。

## 决策

- 只有 `content_verified` 媒体可以通过稳定 Media ID 读取正文或生成 DerivedAsset。
- 请求绑定 query publication，并从 CanonicalMedia → ContentBlob → FileLocation 解析。
- 打开文件后复核平台文件身份、大小和时间证据。
- 完整 GET 在输出字节的同一遍读取中复算 SHA-256，与已发布 ContentBlob 比对。
- Range 只读取单一区间，不额外读取全文件；它依赖打开时身份/stat 复核。
- 提供强 ETag、`If-None-Match`、`If-Range` 和标准 200/206/304/416 语义。
- 未确认内容通过持久 verification Job 建立摘要，不能在 GET 路径隐式升级状态。

## 理由

稳定 ID 与 publication 保持 API 语义；完整读取复算摘要能发现身份未变但内容替换；Range 保持有界 I/O；显式确认 Job 让成本、取消和失败可观察。

## 影响

- `located_unverified` 返回 `CONTENT_NOT_VERIFIED`，客户端可以创建确认 Job。
- 读取期间截断或变化返回结构化内容变化错误。
- 匿名 Share 也必须冻结范围与 Blob，不能仅凭路径放行。
- 并发闸门和重试预算属于独立待冻结运行参数。

## 重新审议

若目标存储无法提供可靠文件身份，需要为该平台定义等价的打开后证据或明确降低支持级别，不能静默取消复核。
