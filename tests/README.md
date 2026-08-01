# 测试与夹具约定

本目录保存跨 package 的正式测试和小型合成夹具。package 内部测试继续与实现同目录，专用输入放入相邻 `testdata/`。

## 边界

- 普通测试只使用 `t.TempDir()` AppDirs、合成 Source 和去敏断言，不读取真实媒体根。
- 需要改名、损坏、链接、删除或强杀的 Source 输入先复制到临时目录；原夹具和真实 Source 保持只读。
- Source 相关测试在操作前后比较 guard。任何意外新增、删除或修改均使验证失败。
- 跨模块客户端测试通过公开 API；不得导入后端 `internal` 包来绕过协议。
- SQLite、WAL、日志、缓存、二进制、浏览器 trace 和大体积结果不提交。可复现的小型 JSON 黄金结果可以提交。
- 真实后端浏览器测试使用独立 AppDirs、loopback 端口和合成身份，不连接已有实例。

## Web 测试位置

- `web/src/**/*.test.ts(x)`：组件和逻辑测试；
- `web/tests/setup.ts`：Vitest 公共设置；
- `web/e2e/`：Playwright mock 与真实后端场景。

Mock 只能验证前端状态机，不能证明浏览器请求头、真实 capability、服务端授权或协议字段一致。涉及这些边界时必须使用生成类型、契约测试和真实浏览器到真实 `galleryd` 的隔离链。

仓库检查入口和当前门禁定义见 [`docs/development/testing-and-release-gates.md`](../docs/development/testing-and-release-gates.md)。
