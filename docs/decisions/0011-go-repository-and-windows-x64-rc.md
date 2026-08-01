# ADR-0011：Go 仓库结构与 Windows x64 RC

- 状态：Accepted
- 日期：2026-08-01

## 上下文

仓库曾按项目内部习惯混合文档、实验和公开 package，并同步维护多个平台声明。当前优先级是让 Go 后端成为清晰基座，并把有限验证资源集中到一个可交付的 Windows x64 候选。

## 决策

- 使用根 module、`cmd/`、`internal/`、公开 `api/` 与 `version/` 的 Go 原生布局。
- `web/` 作为可选组件，`tools/testlab/` 作为根 module 工具，`experiments/testbench/` 保持独立实验 module。
- 平台差异用 `internal/platform`、build constraint 和 OS 后缀显式隔离。
- Windows x64 是 RC 前唯一主动开发、CI、运行验证和发行目标。
- RC 前不新增或同步开发 Linux、macOS、其它 OS/架构；既有实现保留但不构成支持。
- 文档按 reference、architecture、decisions、design、development、operations、validation 等用途组织，不映射 package 树。

## 理由

清晰目录降低公开边界和生成物歧义；显式平台文件便于审查差异；单目标矩阵可以先完成完整正确性、安全、性能、Web 与发行证据，而不是维持多个浅层绿色结果。

## 影响

- 纯后端开发不应把 Node.js 变成编译前置。
- 非 Windows 变更需要等待 RC 后重新立项。
- 目录移动必须同步 import、生成配置、CI、脚本和文档。
- 仓库结构整理不改变产品完成度。

## 重新审议

Windows x64 RC 完成后，根据真实用户、存储和维护需求新增决策选择下一平台与门禁，不自动恢复旧矩阵。
