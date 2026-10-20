# 开发工作流

当前只做已获授权的维护，功能开发暂停；范围见[项目定位](../project/overview-gallery.md)。

1. 阅读根规范、项目规范、进度和相关技术债，确认当前工作树，保留现有改动。
2. 多步任务先写 plan；改契约从 api/openapi.yaml 开始，按[生成关系](build-generated.md)生成。
3. 按[构建顺序](build-gallery.md)先 Web 后 Go，再按[测试说明](testing-gallery.md)选择直接相关测试。
4. 使用 gofmt、go vet 与 Web 自带 Prettier/ESLint；不建立额外检查器或 CI。
5. 文档同步遵循根规范 W-01；当前状态写 status，待办和违反项写 backlog/debt，运行日志放仓库外。
6. 用户授权提交时，单次检查点标题为 Update，无正文；本轮获准正常推送 main，不强推，不创建 tag。

本机路径只放被忽略的 tools/build/local.json 或命令参数，不写入源码、共享文档或 Git 配置。
