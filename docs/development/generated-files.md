# 生成文件说明

生成文件应修改源并运行现有生成器，不得手工维护输出。

| 生成源 | 输出 | 命令 |
| --- | --- | --- |
| `internal/contract/api/openapi.yaml`、`oapi-codegen.yaml` | `api/openapi.gen.go` | `go generate ./...` |
| `internal/contract/api/openapi.yaml` | `web/src/api/schema.gen.ts` | 在 `web/` 执行 `npm run generate:api` |
| `web/public/favicon.svg` | `web/public/icons/gallery-192.png`、`gallery-512.png` | `npm run generate:icons` |
| `internal/rules/rule-package.schema.json` | `web/src/manage/ruleSchemaValidator.gen.cjs` 与声明文件 | `npm run generate:rules-schema` |
| Web 源码与 public 资产 | `internal/webapp/dist` | `npm run build` |

`npm run generate` 聚合三个 Web 生成步骤，`npm run build` 会先执行该聚合再编译 TypeScript 与 Vite 产物。

生成后应检查：

- 只有预期输出变化；
- 输出仍为 UTF-8 与 LF；
- OpenAPI 的 121 个操作与服务端路由保持一致；
- `gallery-web.json` 的 Web、contract 与 API 版本可被 `internal/webapp` 接受；
- 没有把本机路径、时间戳或随机标识写入确定性产物。

`THIRD_PARTY_NOTICES.md`、Markdown 架构文档和发行说明模板是人工维护文件，不是从 OpenAPI 自动生成的 API 副本。
