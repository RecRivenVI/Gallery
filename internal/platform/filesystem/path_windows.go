//go:build windows

package filesystem

import "strings"

// ComparisonKey 返回 Windows 路径比较所用的规范键。
func ComparisonKey(path string) string {
	return strings.ToLower(path)
}
