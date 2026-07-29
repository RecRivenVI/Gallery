package creators

func CreatorPageStatementForTest(includeMerged, hasMerges bool, order string, hasAnchor bool) string {
	statement, _ := creatorPageStatement(includeMerged, hasMerges, order, hasAnchor)
	return statement
}

func GovernancePageStatementForTest(global, hasAnchor bool) string {
	statement, _ := governancePageStatement(global, hasAnchor)
	return statement
}
