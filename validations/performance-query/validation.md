# performance-query

## 目标

对固定 publication、来源和关系分布比较查询、维护与退化成本。

## 运行

使用 testlab seed/probe/publication-perf 的帮助选择固定规模；数据和报告放仓库外，记录 Windows/磁盘、参数、候选身份与缓存状态；分别执行 warm/cold-process、并发和维护，不运行非 Windows。

## 通过标准

相同输入下结果语义一致，超预算有有界行为；实际延迟、超时和未执行组合同时报告，cold-process 不外推为冷盘。预算未冻结时只作调查。
