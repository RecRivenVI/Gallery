# 规则系统

## 职责

规则是 Source 目录与 metadata 差异进入产品语义的唯一入口。它负责识别作品边界、稳定键、字段候选、媒体分类与顺序、封面、发布日期、展示配置和受限条件；客户端只消费结果，不按 Provider 名称重新推导。

## 格式与限制

权威编辑 Schema 是 `internal/rules/rule-package.schema.json`。入口接受 JSON、YAML 或 TOML，最终都转换为同一份规范 JSON。

当前硬边界包括：

- 单份规则输入及最终 canonical 结果不超过 8 MiB；
- JSON/Schema/导入格式的容器嵌套不超过 256 层；
- 未知字段、未知 primitive、非法目标和重复语义按结构化诊断拒绝；
- parameter schema 必须自包含，外部 `$ref`、文件和网络加载被拒绝；
- 规则不能直接读取文件系统、启动进程或访问网络。

## 规范化与身份

规则管线执行：解析 → Schema 校验 → 默认值实例化 → Schema 感知 canonical JSON → 编译 → hash。

| 身份 | 作用 |
| --- | --- |
| package hash | 编辑文档的 canonical 内容身份 |
| semantic hash | 排除纯编辑元数据后的运行语义身份 |
| Rule IR hash | semantic hash、编译器、CEL profile、primitive/extension registry 和冻结参数共同决定的执行身份 |

`RuleVersion` 以 semantic hash 不可变寻址；Source Binding 再冻结参数或 ParameterSet revision 与 IR。扫描不得在执行途中改用新草稿。

## Primitive 与 CEL

Primitive registry 是封闭词表，当前版本由 `PrimitiveRegistryVersion` 标识。已实现类别覆盖路径匹配与捕获、selector/fallback、metadata 映射、稳定键、媒体分类/隐藏/排序、封面候选与禁用标记、作品日期、Badge、条件和平台呈现等。

CEL 只用于受限布尔谓词和简单值判断。编译器限制表达式字节、正则字符、AST 节点、输入 JSON、数组元素、成本和执行时间；没有任意 host function。CEL profile 版本进入 IR 身份，改变限制或语义需要显式版本演进。

## 生命周期

`RulePackage` 有草稿、发布、回滚、弃用和删除约束：

- 草稿按 revision/ETag 保存，冲突不覆盖后来内容；
- 发布前必须成功校验并生成不可变 RuleVersion；
- 回滚只移动 current 指针到已有可执行版本，不重写历史版本；
- 正被 Source Binding 使用的版本不能任意删除；
- ParameterSet 也按 revision 更新、复制、弃用并可执行影响分析；
- Dry Run、Explain 和 Trace 只使用显式合成输入，不读取真实 Source。

## Legacy 转换器

`internal/rules/legacy` 是把已知 legacy schema v3 形状转换为当前 RulePackage 的受限工具，不构成对旧产品的兼容承诺。转换器会为无法忠实表达的字段返回说明，而不是猜测语义。当前覆盖边界见该目录的 `coverage-matrix.md`。

## 主要实现位置

- `internal/rules/package.go`
- `internal/rules/normalize.go`
- `internal/rules/import.go`
- `internal/rules/cel.go`
- `internal/rules/lifecycle.go`
- `internal/rules/rule-package.schema.json`
- `internal/application/rules_lifecycle.go`
