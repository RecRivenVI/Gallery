package creators

func CreatorPageStatementForTest(includeMerged, hasMerges bool, order string, hasAnchor bool) string {
	statement, _ := creatorPageStatement(includeMerged, hasMerges, order, hasAnchor)
	return statement
}
