package catalog

// AggregateCreatorSourceStatementForTest 暴露生产聚合语句给外部测试包执行计划断言；它只存在于
// 测试构建，不扩展正式包 API。
func AggregateCreatorSourceStatementForTest() string {
	return aggregateCreatorSourceStatement
}
