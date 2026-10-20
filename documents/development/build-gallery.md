# 构建 Gallery

先构建 Web 资源，再构建嵌入资源的 Go 服务。生产 bundle 不入库，干净检出直接执行 Go 构建会因缺少 embed 输入失败。

## 统一入口

从仓库根目录执行。`<go>` 为 go.mod 要求的 Go 可执行文件；`<output>` 为仓库外输出目录：

```powershell
./tools/build/Build.ps1 -GoExecutable '<go>' -OutputDirectory '<output>'
```

该入口只调用 npm/Go 构建并从 Git 注入版本，不运行自建检查器、测试或 CI。依赖目录不存在时先执行：

```powershell
npm --prefix web ci
```

## 手动步骤

```powershell
npm --prefix web run build
go build ./cmd/... ./internal/... ./api/... ./tools/...
```

第二条只确认编译，制品输出和 Git 版本注入使用统一入口。正式版本来自当前 SemVer Git 标签；无标签的构建标为 dev，不为构建新建 tag。HTTP 契约版本属于独立协议标识，来自 api/openapi.yaml。

## 便携模板

Windows 打包辅助位于 tools/windows-portable，模板位于其 templates 中。输出目录必须显式指定到仓库外；从 Git 标签取得版本，不以文档或手填字符串定义发行号。签名、历史升级、浏览器和真实 Source 验证独立按 validations 定义运行；本次整理不产生正式发行包。
