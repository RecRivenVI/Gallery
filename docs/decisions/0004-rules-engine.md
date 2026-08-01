# ADR-0004：规则系统

- 状态：Accepted
- 日期：2026-07-16

## 上下文

不同 Source 的目录、metadata 和展示差异需要可配置表达，但任意脚本或 Provider 硬编码会破坏安全、可解释性和可重现性。

## 决策

- RulePackage 使用 JSON Schema 描述，JSON/YAML/TOML 统一导入为规范 JSON。
- 运行语义由封闭 primitive registry 与受限 CEL profile 表达，不执行任意代码。
- 规范化产生 package hash、semantic hash 和带编译器/registry/参数版本的 IR hash。
- 发布产生不可变 RuleVersion；SourceRuleBinding 冻结版本、参数和 IR 身份。
- 提供 Validate、Compile、Dry Run、Explain、Trace、Diff、Impact、发布、回滚和弃用生命周期。
- 规则与 parameter schema 不能读取外部文件或网络，输入大小、深度和执行成本必须有硬上限。

## 理由

封闭语义可以静态分析、限制资源、提供诊断并保证扫描重现；内容寻址避免同名规则或编辑元数据改变运行身份；不可变版本让历史扫描和恢复可解释。

## 影响

- 新来源差异优先扩展通用 primitive 或配置，不在业务层加平台分支。
- 改变 primitive、CEL 或编译语义必须版本化，并进入 IR 身份。
- Web 表单是 Schema 的消费者，服务端编译器仍是最终权威。
- Legacy 转换器只报告可忠实表达的子集，不承诺旧产品兼容。

## 重新审议

只有现有封闭模型无法表达已确认的通用需求，且扩展仍不能有界实现时，才评估新的规则运行时。
