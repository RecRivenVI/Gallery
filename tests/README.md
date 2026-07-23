# 正式测试与夹具约定

- Go 单元、数据和契约夹具放在对应包的 `testdata/`，测试通过公开包契约读取。
- 跨模块端到端测试放在本目录；不得导入 `Test-Bench` 的 Go module 或内部包。
- Walking Skeleton 的公开客户端位于 `pkg/galleryapi`；`galleryctl` 和客户端型验收不得导入后端 `internal` 包。
- 单作品/单媒体合成输入位于 `fixtures/walking-skeleton/`；测试先复制到 `t.TempDir()`，再执行需要改变或删除输入的失败路径。
- 普通测试只使用合成 Source、`t.TempDir()` AppDirs 和脱敏断言，不读取真实媒体根。
- 需要改名、损坏、链接或强杀的输入必须先复制到临时夹具；Source guard 在操作前后比较。
- SQLite、WAL、日志、缓存、二进制和测试输出不提交；可复现的小型 JSON 黄金结果可以提交。
- 大规模 Cleanroom 命令必须先读对应 README，并显式把输出写到验证台内部或系统临时目录。
- Web 单元与 mock 浏览器测试位于 `web/tests/` 和 `web/e2e/`；生产构建输出到 `internal/webapp/dist`，`Check.ps1` 会验证生成的 OpenAPI TypeScript 客户端与嵌入资产没有漂移。
- 真实后端浏览器 E2E 必须使用临时 AppDirs、隔离端口和合成数据，不得连接既有生产实例；Chrome/Edge 通过不代表 Firefox、真实移动设备或物理 LAN 多设备 Gate 通过。
