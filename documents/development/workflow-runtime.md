# 开发运行

先用[构建入口](build-gallery.md)取得二进制。以下命令从仓库根目录执行；<output> 是仓库外构建目录，<app> 是独立临时 AppDirs，不得与 Source 重叠：

```powershell
& '<output>/galleryd.exe' -mode personal -listen 127.0.0.1:8080 -app-root '<app>'
```

打开输出的地址访问用户端，/manage/ 为管理端。Personal 仅 loopback。LAN 先在 loopback 初始化 Owner；具体启动参数以[配置定义](../reference/configuration-service.md)为准。

Ctrl+C 请求优雅退出。不得为测试停止既有用户服务。控制库恢复在下次启动应用；备份操作见[用户指南](../usage/guide-backup.md)。
