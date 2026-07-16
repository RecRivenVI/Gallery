package domain

// CreatorMergePair 表示一次已生效的创作者合并对：Absorbed 被合并进 Target。
// 它是投影时把 Source-derived/Canonical 引用重定向到有效创作者的最小信息，
// 不承载权威事实；权威事实位于 control.db 的 canonical_creators.merged_into
// 与 creator_merges/creator_merge_members。
type CreatorMergePair struct {
	Absorbed      string
	Target        string
	TargetName    string
	TargetSortKey string
}
