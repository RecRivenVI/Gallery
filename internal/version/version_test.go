package version

import "testing"

func TestUninjectedBuildIsDevelopmentVersion(t *testing.T) {
	if Version != "dev" {
		t.Fatalf("未注入版本的测试构建应为 dev，实际 %q", Version)
	}
}
