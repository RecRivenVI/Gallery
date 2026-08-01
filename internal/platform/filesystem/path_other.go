//go:build !windows

package filesystem

// ComparisonKey 保留非 Windows 路径的大小写；Windows x64 RC 之前不扩展其平台语义。
func ComparisonKey(path string) string {
	return path
}
