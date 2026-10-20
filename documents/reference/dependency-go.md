# Go 依赖来源

版本与校验只以根 go.mod/go.sum 为准；本表登记直接依赖用途，不定义第二份版本。生成器在 tools/api-generation/go.mod/go.sum 固定，api/generate.go 调用，变更时同步本表和原生锁定来源。

| 依赖                                      | 用途                |
| ----------------------------------------- | ------------------- |
| github.com/coder/websocket                | WebSocket           |
| github.com/google/cel-go                  | 受限表达式          |
| github.com/oapi-codegen/runtime           | 生成客户端运行库    |
| github.com/pelletier/go-toml/v2           | TOML 规则导入       |
| github.com/santhosh-tekuri/jsonschema/v6  | 规则 Schema         |
| go.yaml.in/yaml/v3                        | YAML 规则和契约信息 |
| golang.org/x/crypto                       | 口令与安全原语      |
| golang.org/x/sys                          | 系统适配            |
| golang.org/x/text                         | 文本归一化          |
| google.golang.org/genproto/googleapis/api | CEL 协议类型        |
| modernc.org/sqlite                        | 进程内数据库        |

生成器使用独立工具 module，避免其传递依赖提升产品依赖版本；工具采用 oapi-codegen 原有固定版本，未升级运行时依赖。
