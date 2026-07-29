package catalog

// AggregateCreatorSourceStatementForTest 暴露生产聚合语句给外部测试包执行计划断言；它只存在于
// 测试构建，不扩展正式包 API。
func AggregateCreatorSourceStatementForTest() string {
	return aggregateCreatorSourceStatement
}

// AggregateCreatorSourceCandidateStatementForTest 暴露 Work/关系到持久窄候选的生产语句。
func AggregateCreatorSourceCandidateStatementForTest() string {
	return aggregateCreatorSourceCandidateStatement
}

// CreatorCoversSmallAllowedStatementForTest 暴露小 allowed 生产查询的执行计划。
func CreatorCoversSmallAllowedStatementForTest() string { return creatorCoversSmallAllowedStatement }

// CreatorCoversSmallAllowedForScopesStatementForTest 暴露定向小 allowed 生产查询的执行计划。
func CreatorCoversSmallAllowedForScopesStatementForTest() string {
	return creatorCoversSmallAllowedForScopesStatement
}

// CreatorCoversSmallDeniedStatementForTest 暴露小 deny 生产查询的执行计划。
func CreatorCoversSmallDeniedStatementForTest() string { return creatorCoversSmallDeniedStatement }

// CreatorCoversSmallDeniedForScopesStatementForTest 暴露定向小 deny 生产查询的执行计划。
func CreatorCoversSmallDeniedForScopesStatementForTest() string {
	return creatorCoversSmallDeniedForScopesStatement
}
