package sourceguard

import (
	"fmt"
	"time"
)

// Guard 把「触碰真实 Source 之前拍一次、之后校一次」变成不可绕过的默认路径。
//
// 此前每个场景各自记得调用 Walk 两次；只要某一步忘了，那一步就完全没有零写入证据，
// 而报告里看不出区别。Guard 反过来：每个真实 Source 操作都必须经由 Around 执行，
// 任一阶段校验失败都返回错误，由调用方转成非零退出。
type Guard struct {
	root     string
	alias    string
	options  Options
	baseline Manifest
	checks   []Check
}

// Check 是一次阶段校验的脱敏结论：只含阶段名、计数与逐条差异数，不含任何名字。
type Check struct {
	Stage           string `json:"stage"`
	OK              bool   `json:"ok"`
	FileCount       int    `json:"fileCount"`
	DirCount        int    `json:"dirCount"`
	LinkCount       int    `json:"linkCount"`
	TotalBytes      int64  `json:"totalBytes"`
	Added           int    `json:"added,omitempty"`
	Removed         int    `json:"removed,omitempty"`
	Modified        int    `json:"modified,omitempty"`
	HashedFileCount int    `json:"hashedFileCount,omitempty"`
	HashStopReason  string `json:"hashStoppedByBound,omitempty"`
	ElapsedMs       int64  `json:"elapsedMs"`
}

// Summary 给出可直接写进 Finding detail 的脱敏描述。内容哈希因边界停止时一并写出，
// 使报告读者不会把有界内容校验误读为全量内容校验。
func (c Check) Summary() string {
	summary := fmt.Sprintf("stage=%s files=%d dirs=%d links=%d bytes=%d added=%d removed=%d modified=%d elapsedMs=%d",
		c.Stage, c.FileCount, c.DirCount, c.LinkCount, c.TotalBytes, c.Added, c.Removed, c.Modified, c.ElapsedMs)
	if c.HashedFileCount > 0 {
		summary += fmt.Sprintf(" hashedFiles=%d", c.HashedFileCount)
	}
	if c.HashStopReason != "" {
		summary += " hashStoppedByBound=" + c.HashStopReason
	}
	return summary
}

// NewGuard 立刻拍下基线清单。空清单直接失败（见 WalkWithOptions 的第 3 条规则）。
func NewGuard(root, alias string, options Options) (*Guard, error) {
	baseline, err := WalkWithOptions(root, options)
	if err != nil {
		return nil, fmt.Errorf("建立 Source guard 基线失败: %w", err)
	}
	baseline.RootAlias = alias
	return &Guard{root: root, alias: alias, options: options, baseline: baseline}, nil
}

// Baseline 返回基线清单（脱敏用途由调用方决定）。
func (g *Guard) Baseline() Manifest { return g.baseline }

// Checks 返回本次运行已完成的全部阶段校验。
func (g *Guard) Checks() []Check { return g.checks }

// Verify 重新遍历并与基线比较，记录一条阶段校验。基线本身不被更新：所有阶段都对照
// **最初那份**清单，因此中途某一步的写入不会被后续基线刷新掩盖。
func (g *Guard) Verify(stage string) (Check, error) {
	started := time.Now()
	current, err := WalkWithOptions(g.root, g.options)
	if err != nil {
		check := Check{Stage: stage, OK: false, ElapsedMs: time.Since(started).Milliseconds()}
		g.checks = append(g.checks, check)
		return check, fmt.Errorf("阶段 %s 的 Source guard 遍历失败: %w", stage, err)
	}
	diff := DiffPersisted(g.baseline.Persisted(), current.Persisted())
	check := Check{
		Stage: stage, OK: g.baseline.Equal(current) && !diff.Changed(),
		FileCount: current.FileCount, DirCount: current.DirCount, LinkCount: current.LinkCount,
		TotalBytes: current.TotalBytes,
		Added:      diff.Added, Removed: diff.Removed, Modified: diff.Modified,
		HashedFileCount: current.HashedFileCount, HashStopReason: current.HashStopReason,
		ElapsedMs: time.Since(started).Milliseconds(),
	}
	g.checks = append(g.checks, check)
	if !check.OK {
		return check, fmt.Errorf("阶段 %s 检出对只读 Source 的写入（新增 %d、删除 %d、修改 %d 条），本次结果无效",
			stage, diff.Added, diff.Removed, diff.Modified)
	}
	return check, nil
}

// Around 在执行一个真实 Source 操作前后各校验一次，任一次不一致都返回错误。
//
// 前置校验不是多余的：它把「上一步就已经破坏了 Source」与「本步破坏了 Source」区分
// 开，否则一次失败会污染此后全部阶段的归因。
func (g *Guard) Around(stage string, operation func() error) error {
	if _, err := g.Verify(stage + "/before"); err != nil {
		return err
	}
	opErr := operation()
	_, verifyErr := g.Verify(stage + "/after")
	if opErr != nil {
		return opErr
	}
	return verifyErr
}
