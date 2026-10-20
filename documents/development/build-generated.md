# 生成关系

修改来源，调用既有生成器；不手工编辑输出。

| 来源                                    | 输出                                     | 根目录命令                                 |
| --------------------------------------- | ---------------------------------------- | ------------------------------------------ |
| api/openapi.yaml、oapi-codegen.yaml     | api/openapi.gen.go                       | go generate ./api                          |
| api/openapi.yaml                        | web/src/api/schema.gen.ts                | npm --prefix web run generate:api          |
| web/public/favicon.svg                  | web/public/icons                         | npm --prefix web run generate:icons        |
| internal/rules/rule-package.schema.json | web/src/manage/ruleSchemaValidator.gen.* | npm --prefix web run generate:rules-schema |
| Git、api/openapi.yaml                   | web/public/gallery-web.json              | npm --prefix web run generate:manifest     |
| Web 源码和资源                          | internal/webapp/dist                     | npm --prefix web run build                 |

图标、预编译校验器、manifest 和生产 bundle 为忽略输出，重新构建即可恢复；Go/TS 接口生成源保留标准生成标记。先生成 Web 才能满足 Go embed。细节见[构建](build-gallery.md)。
