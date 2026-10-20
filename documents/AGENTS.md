# 项目规范

本文件补充根目录的[仓库规范](../AGENTS.md)，对整个仓库生效，不放宽共用规则。

## 项目信息

| 项                 | 值                                                                                                 |
| ------------------ | -------------------------------------------------------------------------------------------------- |
| 产品显示名         | Gallery（画廊）；标识定义见 internal/version                                                       |
| 一句话             | 为个人已有媒体目录提供只读扫描、规则映射与浏览                                                     |
| 核心路径           | 添加来源 → 选择规则 → 扫描 → 浏览作品及原媒体 → 收藏 → 重扫保留用户事实（R-01）                    |
| 技术栈附则         | Go 服务与 Web 前端                                                                                 |
| 支持的平台         | Windows x64；RC 前唯一主动开发和验证目标                                                           |
| 标识               | Go module 取 go.mod；服务、CLI 标识取 internal/version 与 cmd                                      |
| 设计语言与组件库   | 现有 React Aria Components 与 design token；存量自建控件和技术术语违反项见 debt，暂停期间不重做 UI |
| 测试框架           | Go testing、Vitest、Playwright                                                                     |
| 文档主语言         | 简体中文                                                                                           |
| 首页语言           | 简体中文                                                                                           |
| 上游               | 无；独立净室产品                                                                                   |
| 常用测试设备或环境 | Windows x64、隔离 AppDirs、合成媒体；不使用生产实例                                                |

## 技术栈

| 组成                | 版本                                      | 唯一来源                                                   |
| ------------------- | ----------------------------------------- | ---------------------------------------------------------- |
| Go / Go 依赖        | 从声明读取                                | go.mod、go.sum                                             |
| Node/npm/Web 依赖   | 从声明读取                                | web/package.json engines/packageManager、package-lock.json |
| 产品版本            | Git 标签；未打标签的开发构建使用 dev 标识 | Git，构建时注入 internal/version.Version                   |
| HTTP/契约版本与类型 | 从契约读取                                | api/openapi.yaml；生成 Go/TS                               |
| 本机工具链/路径     | 不入库的本地配置                          | tools/build/local.json，参见 setup-local                   |

## 补充规则

- **X-01** Gallery 暂缓功能开发，等待 Gallery-Legacy 完工后再决定代码迁入；本轮不实现业务或 UI。范围由用户决定。
- **X-02** Source 永久只读；不得创建、改写、移动、重命名或删除来源内容。数据库、日志、缓存、锁、临时文件、备份和派生资源不得与 Source 重叠。
- **X-03** control.db 保存不可重建的账户、授权、规则生命周期、Binding、Overlay 等事实；catalog.db 是可重建投影。重扫/GC 不得覆盖用户事实。
- **X-04** CanonicalMedia、ContentBlob、FileLocation 分别表示 occurrence、内容和位置；路径不是永久业务身份。
- **X-05** Catalog 只发布完整 revision；查询与媒体使用一致 publication，不能观察半次扫描。跨库恢复以已发布事实对账，不假设跨库事务。
- **X-06** 来源差异只进入受限规则，不在通用业务/前端按 Provider 名称分支；后端统一排序、过滤、分页、授权和错误语义。
- **X-07** 规则不能执行任意代码、读取未提供文件、启动进程或联网；JSON Schema 外部引用必须拒绝。
- **X-08** 平台中立核心不直接依赖 Win32；差异经 platform 与 ports 隔离。既有非 Windows 文件不代表支持，暂停期间不运行或推进其它 GOOS/GOARCH。
- **X-09** 不读取、输出或提交 secret、Token、Cookie、私密 metadata、真实媒体或完整个人媒体路径。真实 Source 验证须当前明确授权和前后零写入 guard。
- **X-10** Personal 只能 loopback；LAN 只能显式私网/loopback，先从 loopback 初始化 Owner。不能以结构整理放松认证、Origin/Host 或媒体权限。
- **X-11** 现有 control/catalog migration 保留，不改历史 SQL/checksum；schema 变化另加连续 migration并同步验证。当前整理不做 schema 变化。
- **X-12** 正式 Go 核心不得依赖 testlab；CLI 业务只调用公开 API，构建标识可使用 internal/version。小型合成测试输入保留用于复现，不将它们当真实来源验收。
- **X-13** 已实现、已有测试、本轮运行、用户验收和发行状态必须分开陈述。Gallery 仍是 pre-alpha；历史记录不作为当前可用证据。
- **X-14** 高成本浏览器/升级/真实来源/签名验证按任务范围单独执行；不得连接、启动或停止现有生产服务。脚本清理文件遵守工作区回收站规则。
