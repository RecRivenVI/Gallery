package bounds

import (
	"testing"
	"time"
)

func TestUnlimitedDetectsNameOnlyBounds(t *testing.T) {
	if !(Limits{}).Unlimited() {
		t.Fatal("零值必须被识别为不设限")
	}
	for _, limits := range []Limits{{MaxDirs: 1}, {MaxFiles: 1}, {MaxWallClock: time.Second}} {
		if limits.Unlimited() {
			t.Fatalf("%+v 不该被判定为不设限", limits)
		}
	}
}

func TestBudgetStopsAtDirectoryLimit(t *testing.T) {
	budget := Limits{MaxDirs: 2}.Start(nil)
	if !budget.AddDir() || !budget.AddDir() {
		t.Fatal("前两个目录应在预算内")
	}
	if budget.AddDir() {
		t.Fatal("第三个目录必须触顶")
	}
	outcome := budget.Outcome(time.Millisecond)
	if outcome.Completed || outcome.Reason != ReasonMaxDirs {
		t.Fatalf("outcome = %+v", outcome)
	}
	if got := outcome.Summary(); got == "" || got[:7] != "stopped" {
		t.Fatalf("触顶结论必须写明「因边界停止」: %q", got)
	}
}

func TestBudgetStopsAtFileLimit(t *testing.T) {
	budget := Limits{MaxFiles: 1}.Start(nil)
	if !budget.AddFile() {
		t.Fatal("第一个文件应在预算内")
	}
	if budget.AddFile() {
		t.Fatal("第二个文件必须触顶")
	}
	if budget.Outcome(0).Reason != ReasonMaxFiles {
		t.Fatalf("停止原因 = %q", budget.Reason)
	}
}

// TestBudgetStopsAtWallClock 用注入时钟验证墙钟边界，不真的等待。
func TestBudgetStopsAtWallClock(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	budget := Limits{MaxWallClock: time.Minute}.Start(func() time.Time { return now })
	if !budget.AddFile() {
		t.Fatal("未到期时应可继续")
	}
	now = now.Add(2 * time.Minute)
	if budget.AddFile() {
		t.Fatal("超过墙钟上限必须停止")
	}
	if budget.Outcome(2*time.Minute).Reason != ReasonWallClock {
		t.Fatalf("停止原因 = %q", budget.Reason)
	}
}

// TestCompletedOutcomeNeverClaimsBound 锁定「跑完了」与「撞到边界」互斥且各自可辨认。
func TestCompletedOutcomeNeverClaimsBound(t *testing.T) {
	budget := Limits{MaxDirs: 10}.Start(nil)
	budget.AddDir()
	outcome := budget.Outcome(time.Second)
	if !outcome.Completed || outcome.Reason != ReasonNone {
		t.Fatalf("outcome = %+v", outcome)
	}
	if summary := outcome.Summary(); summary[:9] != "completed" {
		t.Fatalf("完成结论 = %q", summary)
	}
}
