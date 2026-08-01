# 设计文档

本目录说明 Gallery 当前已经实现的界面设计约束。设计文档回答“界面如何组织和表现”，不定义服务端业务语义、HTTP 契约或未来开发优先级。

## 当前材料

- [Web 设计](web/README.md)：React 双入口的共享设计语言与交互边界。
- [共享设计系统](web/design-system.md)：token、基础组件、主题、密度和可访问性约束。
- [用户端体验](web/gallery-experience.md)：浏览、检索、作品和媒体查看流程。
- [管理端体验](web/administration-experience.md)：扫描、规则、安全、维护和治理流程。
- [动效系统](web/motion-system.md)：反馈层级、连续布局和减弱动画规则。

## 权威边界

- 产品范围见 [`docs/reference/product-definition.md`](../reference/product-definition.md)。
- 路由、数据流和客户端边界见 [`docs/architecture/platform-and-clients.md`](../architecture/platform-and-clients.md)。
- HTTP 契约以 [`internal/contract/api/openapi.yaml`](../../internal/contract/api/openapi.yaml) 为准。
- 可复用组件与 token 的最终实现位于 [`web/src/design/`](../../web/src/design/README.md)。

候选设计、竞品调查和未采纳方案不得混入当前设计说明。需要长期保留的候选方案应独立标明状态、验证条件和与当前设计的差异。
