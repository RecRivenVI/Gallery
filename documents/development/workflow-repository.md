# 仓库布局

生产代码采用根 Go module，Web 独立构建，文档按用途分类。完整目录规则以根 AGENTS 的 Go/Web 附则为准。

| 位置                         | 当前职责                                               |
| ---------------------------- | ------------------------------------------------------ |
| cmd/galleryd、cmd/galleryctl | 服务和最小 CLI 入口                                    |
| internal                     | 产品私有领域、用例、平台适配、HTTP、存储、版本标识     |
| api                          | OpenAPI 唯一源、嵌入包装、Go 生成器与客户端            |
| web/src                      | gallery/manage 双入口、runtime 会话与实时、design 组件 |
| tools/build                  | 先 Web 后 Go 的纯构建入口                              |
| tools/windows-portable       | 便携构建、升级辅助和发行模板                           |
| tools/testlab                | 可复用的合成语料、进程、报告和场景驱动工具             |
| validations                  | 真实环境/人工判断的可重复验证定义，不含结果            |
| documents                    | 七类文档与项目补充规则                                 |

小型 Go 夹具位于相应 testdata；scanner/testdata 的合成来源由 bootstrap/testlab 显式复用。tools 不得成为产品运行时依赖。实验、运行数据、旧报告和发行产物不在源码树。
