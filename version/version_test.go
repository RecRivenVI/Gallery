package version

import (
	"regexp"
	"testing"
)

func TestDefaultVersionIsSemVerAndDevelopmentVersionMatches(t *testing.T) {
	pattern := regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$`)
	if !pattern.MatchString(DefaultVersion) {
		t.Fatalf("DefaultVersion 不是 SemVer：%q", DefaultVersion)
	}
	if Version != DefaultVersion {
		t.Fatalf("普通测试构建不应注入发行版本：Version=%q DefaultVersion=%q", Version, DefaultVersion)
	}
}
