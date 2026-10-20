# 平台与客户端架构

Gallery 是 Windows x64 的 Go 服务，Web 为同源消费者；当前没有正式桌面壳、安装器或自动更新器。开发暂停，范围见项目说明。

| 组件                             | 责任                                                    |
| -------------------------------- | ------------------------------------------------------- |
| internal/ports、platform         | AppDirs、路径/文件身份、锁、磁盘、进程和工具适配        |
| cmd/galleryd、internal/bootstrap | 服务生命周期、存储与后台服务装配                        |
| cmd/galleryctl                   | 公开 API 的 version/health 最小 CLI；只共享内部构建标识 |
| web/src/gallery                  | 用户浏览与静态 PWA 壳                                   |
| web/src/manage                   | 独立 HTML 管理入口，不受 PWA 导航 fallback 接管         |
| web/src/runtime                  | 会话、错误、实时、查询和主题运行支持                    |
| tools/build                      | 先 Web 后 Go 构建，不承担自建门禁                       |
| tools/windows-portable           | 本地便携包/升级辅助，模板不是已交付制品                 |

Windows 是唯一主动目标；既有非 Windows 文件不代表支持。PWA 不缓存 API、媒体或授权响应。未来壳只能承载和集成 OS，不能另拥有规则、数据库或授权语义；相关方向仍待用户决定。
