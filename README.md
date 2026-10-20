# Gallery（画廊）

Gallery 是本地优先的通用个人媒体目录：只读扫描已有来源，以可配置规则建立目录，浏览作品和媒体，并保留收藏等用户事实。

项目为 pre-alpha，当前暂停功能开发，等待 Gallery-Legacy 完工后再决定迁入代码；已有入口与测试不代表用户验收或正式发行。

## 支持的平台

Windows x64 是当前唯一主动开发和验证目标。其它平台文件不构成支持承诺。

## 快速开始

当前没有正式发行包。源码构建先生成 Web，再构建 Go；工具版本取依赖声明，准备方式见[本机环境](documents/development/setup-local.md)。`<go>` 是 Go 可执行文件路径，`<output>` 是仓库外的构建输出目录：

```powershell
./tools/build/Build.ps1 -GoExecutable '<go>' -OutputDirectory '<output>'
```

在输出目录运行服务，打开它输出的本机地址后完成配对：

```powershell
./galleryd.exe -mode personal -listen 127.0.0.1:8080
```

## 文档入口

- [范围](documents/project/overview-gallery.md)与[当前状态](documents/project/status-gallery.md)
- [使用](documents/usage/guide-start.md)与[备份恢复](documents/usage/guide-backup.md)
- [构建顺序](documents/development/build-gallery.md)、[工作流](documents/development/workflow-development.md)与[测试](documents/development/testing-gallery.md)
- [系统架构](documents/design/architecture-system.md)、[架构决策](documents/design/overview-decisions.md)、[配置](documents/reference/configuration-service.md)
- [项目规范](documents/AGENTS.md)与[技术债](documents/project/debt-repository_standard.md)

## 许可

使用 [GNU AGPL v3](LICENSE)。第三方材料说明见 [NOTICE](NOTICE)。
