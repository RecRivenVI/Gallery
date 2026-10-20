# minimatch CommonJS 兼容适配器

`@redocly/openapi-core` 1.x 需要 minimatch 5 风格的可调用 CommonJS 导出，而当前 Web 工具链使用 minimatch 10。此私有 package 把 `minimatch-modern.minimatch` 重新暴露为可调用导出，并复制其余命名成员。

它只服务构建和契约检查，不进入 Gallery 浏览器运行时。版本由本目录 `package.json` 和 `web/package-lock.json` 固定；升级 Redocly 或 minimatch 时应先验证该适配器是否仍有必要。
