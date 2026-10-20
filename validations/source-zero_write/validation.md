# source-zero_write

## 目标

验证已明确授权的 Source 扫描、确认、查询、媒体读取前后零写入。

## 运行

从根目录查询 go run ./tools/testlab/cmd/guard -h 与 probe -h，显式提供当前授权根、隔离 AppDirs、输出根和墙钟/文件边界；每阶段前后 guard。未获真实来源授权只用合成目录。

## 通过标准

树的内容和元数据守卫一致；扫描/取消/publication 终态可解释；边界停止不能标全量完成，不泄露路径或 metadata。
