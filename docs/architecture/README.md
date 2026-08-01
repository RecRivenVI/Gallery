# 架构文档

本目录描述当前实现的组件边界和数据流。API 字段与路由以 OpenAPI 为准，历史原因和替代方案以 ADR 为准。

- [系统概览](system-overview.md)：进程、模块、存储和主数据流。
- [领域模型与数据所有权](domain-model-and-data-ownership.md)：Source-derived、Canonical、Binding 与 Overlay。
- [扫描、Catalog 与任务](scanning-catalog-and-jobs.md)：扫描档案、Job、publication、恢复与 GC。
- [规则系统](rules-engine.md)：格式、规范化、编译、受限执行和生命周期。
- [查询、搜索与排序](query-search-and-sorting.md)：publication 查询、过滤、排序、total 与 cursor。
- [文件系统与媒体](filesystem-and-media.md)：只读边界、路径、内容确认、Range 与派生资源。
- [API、实时协议与安全](api-realtime-and-security.md)：HTTP、WebSocket、认证、授权与错误。
- [平台与客户端](platform-and-clients.md)：平台适配、Web/PWA、CLI 与发行边界。

每份文档末尾列出主要实现位置，便于从当前源码重新核对。
