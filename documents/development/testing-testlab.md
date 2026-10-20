# 使用 Testlab

Testlab 是复用生产 Store、进程、HTTP 和合成数据的验证工具，不是产品运行时依赖。普通测试使用临时 AppDirs，真实场景按根 validations 定义执行。

## 命令

从仓库根目录用选定的 Go 可执行文件运行；参数先查帮助，不复制历史运行路径：

```powershell
go run ./tools/testlab/cmd/seed -h
go run ./tools/testlab/cmd/probe -h
go run ./tools/testlab/cmd/publication-perf -h
go run ./tools/testlab/cmd/rulesimport -h
go run ./tools/testlab/cmd/guard -h
```

seed 构造固定 Catalog，probe 驱动查询/媒体/来源场景；guard 比较零写入清单，rulesimport 调用正式一次性 converter；web-e2e 和 portable/historical-upgrade 驱动隔离进程组合。产品不得反向导入 testlab。

## 输入与结论

输出、报告、数据库和复制夹具位于仓库外授权测试根。本机路径如需记录只写被忽略的 tools/testlab/local.json，工具仍要求显式传参，不暗读生产路径。

reference 规模核对作品数、来源形状、关系、publication 和 revision；cold-process 不代表冷盘。seed 直接构造投影不能证明扫描吞吐。结果须区分一致、有差异、无法比较及未执行项，旧百万/千万数字不作当前证据。

真实 Source 必须当前授权并执行前后 guard；触发墙钟/枚举边界应报告因边界停止。Hash 取消只有观察目标 running 后才有效，不把夹具或文件名视为运行证明。
