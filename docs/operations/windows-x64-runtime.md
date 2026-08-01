# Windows x64 运行

## 启动

开发构建或便携包都可直接启动 `galleryd`。Personal 示例：

```powershell
./galleryd.exe -mode personal -listen 127.0.0.1:8080
```

打开 `http://127.0.0.1:8080/` 使用画廊，打开 `/manage` 进入管理端。首次访问按页面完成配对。

默认不指定 `-listen` 时使用 `127.0.0.1:0`。实际地址、PID、启动 nonce 和 descriptor 协议写入 Runtime AppDir 的 `galleryd.json`。同一 AppDirs 只允许一个实例持有 `galleryd.lock`。

## 停止

在前台窗口按 `Ctrl+C`，等待进程退出。不要在数据库仍打开时覆盖 AppDirs、替换 `control.db` 或用旧版本直接打开已迁移的数据。

## 健康检查

```powershell
./galleryctl.exe -base-url http://127.0.0.1:8080 health
```

也可以读取 `GET /api/v1/health`。健康响应只说明进程与两库可应答，不等于所有 Source、任务或发布门禁正常。

## 数据目录

Windows 默认目录见[配置参考](../reference/configuration.md)。程序目录可替换，AppDirs 保存运行数据。`control.db` 是不可重建事实的主要备份对象；`catalog.db` 是可重建投影，但删除或重建仍应在产品维护流程中完成，不要手工操作正在使用的数据库。

## 升级边界

当前只有便携升级脚本和历史 schema 验证入口，没有自动更新器。升级前：

1. 创建并验证最新 control 备份；
2. 停止旧进程；
3. 把新程序放在新的程序目录；
4. 启动新版本并确认迁移和健康状态；
5. 保留旧程序与对应备份，直到新版本验证完成。

不要用旧程序打开已升级数据。正式支持的最低 control schema 由制品 manifest 与 `.github/windows-historical-baselines.json` 共同声明，不能从文档中的静态数字推断未来版本。
