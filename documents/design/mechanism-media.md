# 文件系统与媒体

## Source 只读边界

产品代码只以只读方式发现、stat、打开和读取 Source。数据库、日志、锁、备份、缓存、临时文件和 DerivedAsset 只能写入 AppDirs。扫描器没有 Source 写入端口；测试若需要改名、损坏或删除输入，必须先复制到临时目录。

启动时 `ValidateDisjoint` 规范化 AppDirs 与声明的 Source root，解析已存在路径中的链接并拒绝任意重叠。多个 Source root 也不能互相包含。

## 路径与文件身份

- 外部 API 不公开 Source 根绝对路径；
- 相对路径使用清理后的 Source 内表示，任何 `..`、绝对路径或链接逃逸都拒绝；
- Windows 比较键处理路径大小写与 reparse point，Unix 实现使用对应文件系统语义；
- 文件身份通过平台适配器产生版本化 key，不能只用路径、大小和 mtime 猜测替换。

Watcher 事件只是 dirty hint，完整 reconcile 负责收敛。链接、权限变化、设备离线和身份变化有不同结构化错误。

## File root

File root 由启动参数 `-file-root id=path` 声明，用于受授权的只读目录浏览。它：

- 不创建 Library、Source、Binding 或 Catalog；
- 不触发扫描；
- 可以是 Source 的祖先；
- 列表只返回相对条目和安全 metadata，不返回服务器绝对根；
- 通过 `files.browse` capability 授权。

因此 File root 与 Source 是两个独立概念。

## 内容状态

扫描发布的媒体可以是：

- `located_unverified`：位置存在，但尚无完整内容摘要；
- `content_verified`：已建立 ContentBlob 摘要和确认位置；
- 离线、消失或内容变化：读取时根据当前文件事实返回结构化失败。

未确认媒体不能进入依赖 ContentBlob/ETag 的正文读取或 DerivedAsset 路径。客户端可创建单媒体或同 Source 批量确认 Job。

## 正文读取

已确认媒体通过稳定 Media ID 和 query publication 解析到 ContentBlob 与 FileLocation。服务打开文件后复核身份和 stat，再提供：

- `HEAD`；
- 完整 `GET`；
- 单区间 byte Range；多区间请求拒绝；
- 强 ETag、`If-None-Match` 与 `If-Range`；
- `Accept-Ranges`、`Content-Range` 和正确的 200/206/304/416 语义。

完整 GET 在复制字节时同步复算 SHA-256，不增加第二次 I/O；Range 只读取请求区间，依靠文件身份和 stat 复核，不能假装验证了完整摘要。读取期间发生截断或替换返回内容变化错误。

匿名 Share 的媒体读取仍绑定分享范围和冻结 Blob。为降低内容嗅探风险，不可信内联类型可以强制作为 `application/octet-stream` 附件并附加 sandbox 策略。

## 读取并发

媒体读取有服务端闸门，名额耗尽返回可重试 `MEDIA_READ_BUSY`，而不是无界打开文件。当前名额和等待时间属于未冻结运行预算；客户端可以有界退避重试。

## DerivedAsset 与外部工具

DerivedAsset 由 ContentBlob、transform ID/version 和参数寻址，写入 Cache AppDir。当前公开创建接口只承诺受限 JPEG 缩略图 transform。生成必须先取得 `media.derive`，读取仍要求 `media.read`。

`ffprobe`/`ffmpeg` 只有在启动时显式声明绝对路径、版本和 SHA-256 后才可用。进程通过参数数组启动，不提供 shell 字符串入口；平台无法保证所声明的硬限制时应 fail-closed。

## 主要实现位置

- `internal/platform/filesystem/`
- `internal/platform/fileidentity/`
- `internal/fileroot/`
- `internal/media/`
- `internal/derived/`
- `internal/toolrunner/`
- `internal/transport/httpapi/server.go`
