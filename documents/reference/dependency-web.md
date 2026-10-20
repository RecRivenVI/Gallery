# Web 依赖来源

版本只取 web/package.json/package-lock.json；Node 版本在 engines，npm 在 packageManager。所有依赖固定，不从本文复制版本。

| 依赖组                             | 用途                                                       |
| ---------------------------------- | ---------------------------------------------------------- |
| React、React DOM、Router           | 双入口页面和路由                                           |
| TanStack Query、openapi-fetch      | API 访问、缓存与类型                                       |
| React Aria Components              | 可访问性基础组件                                           |
| RJSF、AJV、lossless-json           | 规则 Schema 表单和精确 JSON                                |
| Vite、PWA plugin、Workbox、sharp   | 资源构建、静态壳、图标生成                                 |
| TypeScript、ESLint、Prettier       | 类型、原生 lint、格式                                      |
| Vitest、Testing Library、jsdom     | 单元和组件测试                                             |
| Playwright、axe                    | 浏览器与可访问性场景                                       |
| openapi-typescript、minimatch 适配 | 契约生成工具链；适配说明见 dependency-minimatch_adapter.md |
