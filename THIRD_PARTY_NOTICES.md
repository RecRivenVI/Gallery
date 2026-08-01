# 第三方材料说明

本文件只登记直接保存在仓库中的第三方源码或资产。通过 `go.mod`、`package.json` 或 lockfile 获取的依赖由各自清单管理，不在此重复列出。

当前登记项都位于历史 Wails 实验 `experiments/testbench/cleanroom-lab/deploy/wails-shell/`，不进入正式 `galleryd`、`galleryctl` 或 Web 构建。

| 路径 | 来源 | 许可证 | 用途 |
| --- | --- | --- | --- |
| `frontend/src/assets/fonts/nunito-v16-latin-regular.woff2`、`OFL.txt` | Nunito Project | SIL Open Font License 1.1 | Wails 模板字体 |
| `frontend/wailsjs/runtime/`、`frontend/wailsjs/go/main/` | [Wails](https://github.com/wailsapp/wails) | MIT | Wails 生成的运行时与绑定 |
| `build/appicon.png`、`frontend/src/assets/images/logo-universal.png` | Wails 默认模板 | MIT | 实验壳占位图标与 Logo |

相关文件保留了随附许可证或来源元数据。新增、删除或替换直接包含的第三方材料时，应重新核对本表；本文件不是法律意见。
