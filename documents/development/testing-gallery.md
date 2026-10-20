# 测试 Gallery

采用技术栈原生工具，不设置持续集成或仓库自建门禁。结论按“一致”“有差异”“无法比较”表达，通过数量仅用于识别执行范围。

## 后端

先完成 Web 构建，再从根目录执行。用 go.mod 指定版本的 go；免安装时把 go 换成其完整路径：

```powershell
go build ./cmd/... ./internal/... ./api/... ./tools/...
go test ./cmd/... ./internal/... ./api/... ./tools/...
go vet ./cmd/... ./internal/... ./api/... ./tools/...
```

显式包集合避免将 web/node_modules 中的第三方 Go 文件纳入本项目测试。内部版本包和公开 API 的测试随上述集合运行。并发实现发生变化时才运行相应 race；本轮只迁移引用，不变更并发机制。

## Web

```powershell
npm --prefix web run build
npm --prefix web run lint
npm --prefix web run format:check
npm --prefix web test -- --reporter=dot
```

真实浏览器、真实后端业务链、签名/制品和大目录不自动追加；定义见根 validations。mock 只能说明组件状态，不能证明真实权限/请求头或视觉验收。失败最多原样复跑一次；已有缺陷不在结构整理中修复。

## 输入和结果

单元测试使用临时 AppDirs 与小型合成 testdata。不得连接现有实例或操作真实媒体。结果与日志放仓库外；正式测试工具见[框架入口](testing-testlab.md)，既有局限见[技术债](../project/debt-repository_standard.md)。
