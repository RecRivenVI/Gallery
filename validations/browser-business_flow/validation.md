# browser-business_flow

## 目标

验证用户浏览和管理业务在真实隔离 galleryd 上成立。

## 运行

先构建 Web/Go，再运行 npm --prefix web run test:real:bootstrap -- -go-executable <go>；Windows 工具路径经命令参数提供。使用合成 Source、独立 AppDirs 和动态 loopback 端口，屏幕 1920×1080、静音。输出放仓库外；没有浏览器工具或工具链时报告无法比较，不连接现有服务。

## 通过标准

核心任务可由用户实际完成，权限/分页/重连与真实后端一致；不以 mock 或通过数量替代视觉和人工验收。
