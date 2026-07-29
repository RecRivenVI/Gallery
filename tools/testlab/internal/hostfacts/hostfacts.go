// Package hostfacts 自动采集一次 testlab 运行的环境事实：CPU 型号与逻辑核数、
// 物理内存总量、操作系统版本、SQLite 版本，以及**数据库实际所在物理盘**的介质与
// 型号。
//
// 为什么必须自动采集而不是人工标注：`Documents/指南/02-测试与发布门禁.md` 的
// Reference Performance Gate 要求「每次结果必须同时记录 CPU、内存、存储型号/介质、
// 操作系统、SQLite/搜索库版本、冷/热缓存、并发数、样本规模、运行次数和分位数；
// 缺少任一项的旧数字只能作为方向性证据」。人工 flag 没有任何校验，写错、写旧、
// 忘写都不会被发现，因此本包把这些事实全部改为实测，人工标注只作为交叉核对输入
// （见 CrossCheckStorageClass），不一致时报告冲突而不是让人工值覆盖实测值。
//
// 介质判定不看盘符：逻辑路径看起来在某个盘、实际经目录联接（junction）或符号链接
// 落在另一块物理盘上是本仓库已经实测过的真实情形。Windows 实现先用
// GetFinalPathNameByHandle 把路径解析到最终卷，再由卷取磁盘 extent、由物理盘取
// 设备描述符与 seek penalty；Linux 实现先 EvalSymlinks 再按 st_dev 走
// /sys/dev/block 找到承载分区的整盘。任何一步取不到事实都如实标 unknown 并写明
// 原因，不猜测。
package hostfacts

import (
	"context"
	"database/sql"
	"fmt"
	"runtime"
	"slices"
	"strings"
	"time"

	// 仅用于 SELECT sqlite_version()：本包打开的是一个进程内 :memory: 连接，
	// 不打开、不读取、也不写入 control.db/catalog.db。probe「不直接读写 SQLite
	// 数据库」的边界因此不被破坏，而门禁要求的「SQLite 版本」得到的是本次二进制
	// 真正链接的版本，不是人工填写的字符串。
	_ "modernc.org/sqlite"
)

// ResumeDifferences 返回会使两次性能窗口不可合并的环境字段名，不返回字段值。
// recorded.GoMaxProcs==0 仅兼容 EV-99 之前的旧报告；新报告从生成起严格匹配。
func ResumeDifferences(recorded, current Facts) []string {
	comparisons := []struct {
		name  string
		equal bool
	}{
		{"osFamily", recorded.OSFamily == current.OSFamily},
		{"arch", recorded.Arch == current.Arch},
		{"osVersion", recorded.OSVersion == current.OSVersion},
		{"cpuModel", recorded.CPUModel == current.CPUModel},
		{"cpuLogicalCores", recorded.CPULogicalCores == current.CPULogicalCores},
		{"memoryTotalBytes", recorded.MemoryTotalBytes == current.MemoryTotalBytes},
		{"sqliteVersion", recorded.SQLiteVersion == current.SQLiteVersion},
		{"sqliteLibrary", recorded.SQLiteLibrary == current.SQLiteLibrary},
		{"goVersion", recorded.GoVersion == current.GoVersion},
		{"goMaxProcs", recorded.GoMaxProcs == 0 || recorded.GoMaxProcs == current.GoMaxProcs},
		{"storage.medium", recorded.Storage.Medium == current.Storage.Medium},
		{"storage.model", recorded.Storage.Model == current.Storage.Model},
		{"storage.busType", recorded.Storage.BusType == current.Storage.BusType},
		{"storage.volumeId", recorded.Storage.VolumeID == current.Storage.VolumeID},
		{"storage.physicalDiskNumbers", slices.Equal(recorded.Storage.PhysicalDiskNumbers, current.Storage.PhysicalDiskNumbers)},
	}
	differences := make([]string, 0, len(comparisons))
	for _, comparison := range comparisons {
		if !comparison.equal {
			differences = append(differences, comparison.name)
		}
	}
	return differences
}

// 存储介质分类取值。unknown 是合法结论，表示本次运行没有取到可信证据；它必须被
// 如实写进报告，不得由调用方替换成一个「看起来合理」的猜测值。
const (
	MediumSSD     = "ssd"
	MediumHDD     = "hdd"
	MediumUnknown = "unknown"
)

// Storage 描述数据库实际落盘位置的存储事实。字段刻意不含任何绝对路径、卷挂载点
// 或设备路径：Report.Save 的敏感内容防线会拒绝这些内容，而它们对性能结论也没有
// 价值。序列号同样不采集——它是可识别的硬件标识，对门禁没有用处。
type Storage struct {
	// Medium 是实测介质分类（ssd/hdd/unknown），不是人工标注。
	Medium string `json:"medium"`
	// MediumEvidence 写明该结论的具体来源（例如 seek penalty 描述符、总线类型、
	// /sys rotational），使读者可以判断这条结论的强度。
	MediumEvidence string `json:"mediumEvidence,omitempty"`
	// Model 是物理盘的厂商+产品标识，例如 "Samsung SSD 990 PRO 2TB"。
	Model string `json:"model,omitempty"`
	// BusType 是物理盘总线类型（NVMe/SATA/USB/...）。
	BusType string `json:"busType,omitempty"`
	// PhysicalDiskNumbers 是承载该卷的物理盘编号；跨盘卷（RAID/Storage Spaces）
	// 会有多个。
	PhysicalDiskNumbers []int `json:"physicalDiskNumbers,omitempty"`
	// LogicalDrive 是调用方给出的逻辑路径所在盘符（Windows，例如 "D"）。
	LogicalDrive string `json:"logicalDrive,omitempty"`
	// PhysicalDrive 是解析全部链接后真正落地的盘符（Windows，例如 "F"）。
	PhysicalDrive string `json:"physicalDrive,omitempty"`
	// ResolvedThroughLink 为 true 表示逻辑路径与最终卷不在同一个盘符上，即该路径
	// 经过了目录联接或符号链接。只看盘符做介质判定在这种情况下必然是错的。
	ResolvedThroughLink bool `json:"resolvedThroughLink"`
	// VolumeID 是最终卷的稳定标识（Windows 卷 GUID / Linux 设备号），用于确认多次
	// 运行确实落在同一个卷上。
	VolumeID string `json:"volumeId,omitempty"`
	// Errors 记录采集过程中每一步的失败原因；非空时 Medium 通常为 unknown。
	Errors []string `json:"errors,omitempty"`
}

// Facts 是一次运行的完整环境事实快照，整体写入 report.Report.Environment。
type Facts struct {
	OSFamily         string   `json:"osFamily"`
	Arch             string   `json:"arch"`
	OSVersion        string   `json:"osVersion"`
	CPUModel         string   `json:"cpuModel"`
	CPULogicalCores  int      `json:"cpuLogicalCores"`
	MemoryTotalBytes int64    `json:"memoryTotalBytes"`
	SQLiteVersion    string   `json:"sqliteVersion"`
	SQLiteLibrary    string   `json:"sqliteLibrary"`
	GoVersion        string   `json:"goVersion"`
	GoMaxProcs       int      `json:"goMaxProcs"`
	Storage          Storage  `json:"storage"`
	Errors           []string `json:"errors,omitempty"`
}

// Complete 报告门禁要求的全部环境字段是否都取到了实测值。缺任一项时结果只能作为
// 方向性证据，调用方必须据此在报告里产生一条明确的失败 finding，而不是照常宣称
// 通过。
func (f Facts) Complete() bool {
	return f.OSVersion != "" && f.CPUModel != "" && f.CPULogicalCores > 0 &&
		f.MemoryTotalBytes > 0 && f.SQLiteVersion != "" && f.GoMaxProcs > 0 && f.Storage.Medium != MediumUnknown
}

// MissingFields 列出缺失的门禁必需字段，供报告写出具体缺了什么。
func (f Facts) MissingFields() []string {
	var missing []string
	if f.OSVersion == "" {
		missing = append(missing, "osVersion")
	}
	if f.CPUModel == "" {
		missing = append(missing, "cpuModel")
	}
	if f.CPULogicalCores <= 0 {
		missing = append(missing, "cpuLogicalCores")
	}
	if f.MemoryTotalBytes <= 0 {
		missing = append(missing, "memoryTotalBytes")
	}
	if f.SQLiteVersion == "" {
		missing = append(missing, "sqliteVersion")
	}
	if f.GoMaxProcs <= 0 {
		missing = append(missing, "goMaxProcs")
	}
	if f.Storage.Medium == MediumUnknown {
		missing = append(missing, "storage.medium")
	}
	return missing
}

// CrossCheckStorageClass 用人工 `-storage-class` 标注交叉核对实测介质。返回的
// conflict 为 true 时调用方必须把冲突写成失败 finding：人工标注不得覆盖实测结论，
// 因为整份报告的可信度正建立在「环境事实是测出来的」这一点上。manual 为空表示
// 本次没有人工标注，不构成冲突。
func CrossCheckStorageClass(manual string, storage Storage) (conflict bool, detail string) {
	manual = strings.ToLower(strings.TrimSpace(manual))
	if manual == "" {
		return false, ""
	}
	if storage.Medium == MediumUnknown {
		return false, fmt.Sprintf("人工标注 storage-class=%s 无法交叉核对：实测介质为 unknown（%s）", manual, strings.Join(storage.Errors, "; "))
	}
	if manual != storage.Medium {
		return true, fmt.Sprintf("人工标注 storage-class=%s 与实测介质 %s 不一致（依据: %s）；以实测为准", manual, storage.Medium, storage.MediumEvidence)
	}
	return false, ""
}

// Collect 采集全部环境事实。dbPath 应指向本次运行实际使用的数据库目录（AppDirs 的
// data 目录）；介质判定就是对该路径最终落地的物理盘做的，不是对进程工作目录或
// 仓库所在盘做的。
func Collect(dbPath string) Facts {
	facts := Facts{
		OSFamily:        runtime.GOOS,
		Arch:            runtime.GOARCH,
		CPULogicalCores: runtime.NumCPU(),
		GoVersion:       runtime.Version(),
		GoMaxProcs:      runtime.GOMAXPROCS(0),
		SQLiteLibrary:   "modernc.org/sqlite",
	}
	host, err := collectHost()
	if err != nil {
		facts.Errors = append(facts.Errors, "host: "+err.Error())
	}
	facts.OSVersion = host.osVersion
	facts.CPUModel = host.cpuModel
	facts.MemoryTotalBytes = host.memoryTotalBytes
	if host.logicalCores > 0 {
		facts.CPULogicalCores = host.logicalCores
	}

	version, err := sqliteVersion()
	if err != nil {
		facts.Errors = append(facts.Errors, "sqlite: "+err.Error())
	}
	facts.SQLiteVersion = version

	facts.Storage = collectStorage(dbPath)
	return facts
}

// hostInfo 是各平台实现返回的通用主机事实；取不到的字段保持零值，由 Collect 统一
// 转成缺失项，不用占位字符串伪装成已知。
type hostInfo struct {
	osVersion        string
	cpuModel         string
	logicalCores     int
	memoryTotalBytes int64
}

func sqliteVersion() (string, error) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		return "", err
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var version string
	if err := db.QueryRowContext(ctx, "SELECT sqlite_version()").Scan(&version); err != nil {
		return "", err
	}
	return version, nil
}

// cleanDeviceString 把设备描述符里的定长/补空格字符串整理成单行紧凑文本。
func cleanDeviceString(value string) string {
	value = strings.ReplaceAll(value, "\x00", " ")
	return strings.Join(strings.Fields(value), " ")
}
