package hostfacts

import (
	"strings"
	"testing"
)

// TestCollectReturnsMeasuredFactsForCurrentRoot 在当前受支持平台（Windows/Linux）
// 上要求 CPU/内存/OS/SQLite 四项必须真的采到；它们不依赖任何特殊权限，采不到就是
// 采集实现坏了，而不是环境限制。存储介质允许为 unknown（例如 WSL 的 9p 挂载），
// 但那时必须给出原因，不允许静默的空结论。
func TestCollectReturnsMeasuredFactsForCurrentRoot(t *testing.T) {
	facts := Collect(t.TempDir())

	if facts.CPULogicalCores <= 0 {
		t.Errorf("CPULogicalCores = %d, want > 0", facts.CPULogicalCores)
	}
	if facts.GoMaxProcs <= 0 {
		t.Errorf("GoMaxProcs = %d, want > 0", facts.GoMaxProcs)
	}
	if facts.SQLiteVersion == "" {
		t.Errorf("SQLiteVersion 为空；errors=%v", facts.Errors)
	}
	if facts.SQLiteLibrary == "" {
		t.Error("SQLiteLibrary 为空")
	}
	switch facts.OSFamily {
	case "windows", "linux":
		if facts.CPUModel == "" {
			t.Errorf("CPUModel 为空；errors=%v", facts.Errors)
		}
		if facts.MemoryTotalBytes <= 0 {
			t.Errorf("MemoryTotalBytes = %d, want > 0；errors=%v", facts.MemoryTotalBytes, facts.Errors)
		}
		if facts.OSVersion == "" {
			t.Errorf("OSVersion 为空；errors=%v", facts.Errors)
		}
	}
	if facts.Storage.Medium == MediumUnknown && len(facts.Storage.Errors) == 0 && facts.Storage.MediumEvidence == "" {
		t.Error("介质为 unknown 时必须给出 errors 或 evidence，不允许无理由的未知结论")
	}
	if facts.Storage.Medium != MediumUnknown && facts.Storage.MediumEvidence == "" {
		t.Errorf("介质判定为 %q 却没有记录依据", facts.Storage.Medium)
	}
}

// TestFactsNeverLeakPathsOrAddresses 复核环境事实不会把绝对路径/设备路径带进报告：
// report.Report.Save 的敏感内容防线会拒绝这些内容，而存储采集天然接触大量路径。
func TestFactsNeverLeakPathsOrAddresses(t *testing.T) {
	facts := Collect(t.TempDir())
	texts := append([]string{
		facts.OSVersion, facts.CPUModel, facts.Storage.Model, facts.Storage.BusType,
		facts.Storage.MediumEvidence, facts.Storage.VolumeID, facts.Storage.LogicalDrive, facts.Storage.PhysicalDrive,
	}, facts.Errors...)
	texts = append(texts, facts.Storage.Errors...)
	for _, text := range texts {
		for _, marker := range []string{`:\`, `\\`, "http://", "https://"} {
			if strings.Contains(text, marker) {
				t.Errorf("环境事实 %q 含敏感标记 %q", text, marker)
			}
		}
	}
}

func TestCrossCheckStorageClassReportsConflictWithoutOverriding(t *testing.T) {
	measured := Storage{Medium: MediumHDD, MediumEvidence: "PhysicalDrive3 seekPenalty=true"}

	if conflict, detail := CrossCheckStorageClass("", measured); conflict || detail != "" {
		t.Errorf("空人工标注不应构成冲突: conflict=%v detail=%q", conflict, detail)
	}
	if conflict, _ := CrossCheckStorageClass("hdd", measured); conflict {
		t.Error("一致的人工标注不应构成冲突")
	}
	conflict, detail := CrossCheckStorageClass("ssd", measured)
	if !conflict {
		t.Fatal("人工标注 ssd 与实测 hdd 必须报告冲突")
	}
	if !strings.Contains(detail, "以实测为准") {
		t.Errorf("冲突说明未声明以实测为准: %q", detail)
	}
}

func TestCrossCheckStorageClassDoesNotConflictWhenMediumUnknown(t *testing.T) {
	measured := Storage{Medium: MediumUnknown, Errors: []string{"卷没有报告任何物理盘 extent"}}
	conflict, detail := CrossCheckStorageClass("ssd", measured)
	if conflict {
		t.Error("实测未知时不得把人工标注判成冲突")
	}
	if detail == "" {
		t.Error("实测未知时必须说明无法交叉核对")
	}
}

func TestMissingFieldsListsEveryGateRequirement(t *testing.T) {
	empty := Facts{Storage: Storage{Medium: MediumUnknown}}
	missing := empty.MissingFields()
	for _, want := range []string{"osVersion", "cpuModel", "cpuLogicalCores", "memoryTotalBytes", "sqliteVersion", "goMaxProcs", "storage.medium"} {
		found := false
		for _, got := range missing {
			if got == want {
				found = true
			}
		}
		if !found {
			t.Errorf("MissingFields() = %v, 缺少 %q", missing, want)
		}
	}
	if empty.Complete() {
		t.Error("空 Facts 不应报告 Complete")
	}

	full := Facts{
		OSVersion: "os", CPUModel: "cpu", CPULogicalCores: 8, MemoryTotalBytes: 1,
		SQLiteVersion: "3.0.0", GoMaxProcs: 2, Storage: Storage{Medium: MediumSSD},
	}
	if !full.Complete() || len(full.MissingFields()) != 0 {
		t.Errorf("完整 Facts 报告为不完整: missing=%v", full.MissingFields())
	}
}
