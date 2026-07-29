package rules_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestEmbeddedIANAZoneinfoSurvivesPortableRuntime 锁定 Windows 独立发行包的时区边界。
// `-trimpath` 构建的二进制不能假设目标机器仍有构建时 Go SDK 的 zoneinfo.zip；规则编译
// 与时间呈现必须在空 GOROOT、无外部 ZONEINFO 的进程中仍能解析正式配置使用的 IANA 时区。
func TestEmbeddedIANAZoneinfoSurvivesPortableRuntime(t *testing.T) {
	if os.Getenv("GALLERY_TZDATA_TEST_HELPER") == "1" {
		if _, err := time.LoadLocation("Asia/Shanghai"); err != nil {
			t.Fatalf("独立 Windows 运行时缺少嵌入 IANA 时区数据: %v", err)
		}
		return
	}

	emptyRoot := t.TempDir()
	command := exec.Command(os.Args[0], "-test.run=^TestEmbeddedIANAZoneinfoSurvivesPortableRuntime$")
	command.Env = append(os.Environ(),
		"GALLERY_TZDATA_TEST_HELPER=1",
		"GOROOT="+emptyRoot,
		"ZONEINFO="+filepath.Join(emptyRoot, "missing-zoneinfo.zip"),
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("隔离时区子进程失败: %v\n%s", err, output)
	}
}
