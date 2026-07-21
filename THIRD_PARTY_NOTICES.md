# 第三方材料说明

本文件记录仓库中经审计确认的、直接包含的第三方源码或资产（而非仅通过 `go.mod`/`package.json` 声明的外部依赖关系）。审计范围为 `git ls-files` 跟踪的全部文件；正式产品代码（`cmd/`、`internal/`、`pkg/`）与 `tests/` 下的测试 fixture 均为项目原创或合成数据，未发现需要在此登记的第三方直接材料。以下条目全部位于 `Test-Bench/cleanroom-lab/deploy/wails-shell/`，是历史 Wails 桌面壳技术对照实验的脚手架产物，不进入正式产品构建、CI 或发行。

| 路径 | 项目/来源 | 许可证 | 使用方式 | 说明 |
| --- | --- | --- | --- | --- |
| `frontend/src/assets/fonts/nunito-v16-latin-regular.woff2`、同目录 `OFL.txt` | Nunito 字体，Copyright 2016 The Nunito Project Authors | SIL Open Font License 1.1 | Wails 默认 Vanilla JS 模板自带字体资源 | 许可证正文（`OFL.txt`）已随字体文件一同保留在仓库中，满足 OFL 附带许可证的要求 |
| `frontend/wailsjs/runtime/*`、`frontend/wailsjs/go/main/*` | Wails JavaScript 运行时与生成绑定（[wailsapp/wails](https://github.com/wailsapp/wails)，作者 Lea Anthony） | MIT | Wails CLI 脚手架在构建时自动生成/内置的浏览器端运行时与 Go-JS 绑定代码 | `runtime.js` 保留原始版权注释；`wailsjs/runtime/package.json` 声明 `"license": "MIT"` 并指向上游仓库 |
| `build/appicon.png`、`frontend/src/assets/images/logo-universal.png` | Wails 项目默认模板资源（wailsapp/wails） | MIT | `wails init` 生成的默认应用图标与 Logo 占位图，未被替换为项目自有美术资源 | 随 Wails 模板整体以 MIT 许可证分发 |

## 说明

- 上述材料均为标准 Wails 脚手架工具（`wails init`/`wails build`）自动生成或内置，不是人工从其它项目复制粘贴的代码。
- 三项来源许可证（SIL OFL 1.1、MIT）均为宽松许可证，与仓库主体的 AGPL-3.0-only 兼容，不构成许可证冲突。
- 本文件基于当前 `git ls-files` 快照人工审计得出，不构成正式法律意见；如需合规评审，请咨询专业法律顾问。
- ASP.NET（`Test-Bench/cleanroom-lab/deploy/aspnet/`）与 PWA（`Test-Bench/cleanroom-lab/deploy/pwa/`）对照实验代码经审查为项目原创的最小示例代码，未发现来自第三方模板的直接复制内容，故未在此登记。
