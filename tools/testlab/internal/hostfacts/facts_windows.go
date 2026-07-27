//go:build windows

package hostfacts

import (
	"encoding/binary"
	"fmt"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// Windows 侧的环境事实全部来自内核/注册表，不 shell out 到 PowerShell 或 wmic：
// 外部进程会引入 PATH、执行策略与本地化输出格式三重不确定性，而这些事实是门禁
// 结论的一部分，不能依赖「大多数机器上恰好能跑通」的路径。

const (
	ioctlVolumeGetVolumeDiskExtents = 0x00560000
	ioctlStorageQueryProperty       = 0x002D1400

	storageDeviceProperty             = 0
	storageDeviceSeekPenaltyProperty  = 7
	propertyStandardQuery             = 0
	volumeNameGUID                    = 0x1
	volumeNameDOS                     = 0x0
	storagePropertyQueryHeaderSize    = 12
	storageDeviceDescriptorBufferSize = 2048
	volumeDiskExtentsBufferSize       = 4096
)

// busTypeNames 覆盖 STORAGE_BUS_TYPE 中与本机验证相关的取值；未知编号如实打印
// 数字，不假装认识。
var busTypeNames = map[uint32]string{
	1: "SCSI", 2: "ATAPI", 3: "ATA", 4: "1394", 5: "SSA", 6: "Fibre", 7: "USB",
	8: "RAID", 9: "iSCSI", 10: "SAS", 11: "SATA", 12: "SD", 13: "MMC",
	14: "Virtual", 15: "FileBackedVirtual", 16: "StorageSpaces", 17: "NVMe",
	18: "SCM", 19: "UFS",
}

func collectHost() (hostInfo, error) {
	var info hostInfo
	var problems []string

	if version := windows.RtlGetVersion(); version != nil {
		product, display := windowsProductNames()
		build := fmt.Sprintf("%d.%d.%d", version.MajorVersion, version.MinorVersion, version.BuildNumber)
		parts := make([]string, 0, 3)
		if product != "" {
			parts = append(parts, product)
		}
		if display != "" {
			parts = append(parts, display)
		}
		parts = append(parts, build)
		info.osVersion = strings.Join(parts, " ")
	} else {
		problems = append(problems, "RtlGetVersion 返回 nil")
	}

	model, err := processorNameString()
	if err != nil {
		problems = append(problems, "CPU 型号: "+err.Error())
	}
	info.cpuModel = model

	total, err := physicalMemoryBytes()
	if err != nil {
		problems = append(problems, "物理内存: "+err.Error())
	}
	info.memoryTotalBytes = total

	if len(problems) > 0 {
		return info, fmt.Errorf("%s", strings.Join(problems, "; "))
	}
	return info, nil
}

// windowsProductNames 读取面向人的版本名（例如 "Windows 11 Pro for Workstations"
// 与 "24H2"）。取不到时返回空串——RtlGetVersion 的数字版本已经足以标识环境，不用
// 编造产品名补位。
func windowsProductNames() (product, display string) {
	key, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Windows NT\CurrentVersion`, registry.QUERY_VALUE)
	if err != nil {
		return "", ""
	}
	defer key.Close()
	product, _, _ = key.GetStringValue("ProductName")
	display, _, _ = key.GetStringValue("DisplayVersion")
	return product, display
}

func processorNameString() (string, error) {
	key, err := registry.OpenKey(registry.LOCAL_MACHINE, `HARDWARE\DESCRIPTION\System\CentralProcessor\0`, registry.QUERY_VALUE)
	if err != nil {
		return "", err
	}
	defer key.Close()
	name, _, err := key.GetStringValue("ProcessorNameString")
	if err != nil {
		return "", err
	}
	return cleanDeviceString(name), nil
}

// memoryStatusEx 镜像 MEMORYSTATUSEX；x/sys/windows 未导出 GlobalMemoryStatusEx，
// 因此这里按其固定布局自行声明并经 LazySystemDLL 调用。
type memoryStatusEx struct {
	Length               uint32
	MemoryLoad           uint32
	TotalPhys            uint64
	AvailPhys            uint64
	TotalPageFile        uint64
	AvailPageFile        uint64
	TotalVirtual         uint64
	AvailVirtual         uint64
	AvailExtendedVirtual uint64
}

var (
	modKernel32               = windows.NewLazySystemDLL("kernel32.dll")
	procGlobalMemoryStatusEx  = modKernel32.NewProc("GlobalMemoryStatusEx")
	errGlobalMemoryStatusFail = fmt.Errorf("GlobalMemoryStatusEx 调用失败")
)

func physicalMemoryBytes() (int64, error) {
	if err := procGlobalMemoryStatusEx.Find(); err != nil {
		return 0, err
	}
	status := memoryStatusEx{}
	status.Length = uint32(unsafe.Sizeof(status))
	ret, _, callErr := procGlobalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&status)))
	if ret == 0 {
		if callErr != nil {
			return 0, callErr
		}
		return 0, errGlobalMemoryStatusFail
	}
	return int64(status.TotalPhys), nil
}

// collectStorage 把 dbPath 一路解析到承载它的物理盘：
//
//  1. 打开目录句柄（FILE_FLAG_BACKUP_SEMANTICS）并用 GetFinalPathNameByHandle
//     取最终路径。这一步是关键：目录联接与符号链接在这里被彻底展开，逻辑盘符
//     与真实盘符不一致会被直接看见，而不是被当成同一块盘。
//  2. 由最终路径的卷 GUID 打开卷设备，用 IOCTL_VOLUME_GET_VOLUME_DISK_EXTENTS
//     取承载该卷的物理盘编号（跨盘卷会有多个）。
//  3. 打开 \\.\PhysicalDriveN（access=0，无需管理员），用
//     IOCTL_STORAGE_QUERY_PROPERTY 取设备描述符（厂商/产品/总线）与 seek penalty
//     描述符（是否有寻道代价）判定介质。
//
// 任何一步失败都记录原因并返回 unknown，绝不回退成「按盘符猜」。
func collectStorage(dbPath string) Storage {
	result := Storage{Medium: MediumUnknown}
	absolute, err := filepath.Abs(dbPath)
	if err != nil {
		result.Errors = append(result.Errors, "解析绝对路径失败: "+err.Error())
		return result
	}
	result.LogicalDrive = driveLetterOf(absolute)

	handle, err := openDirectory(absolute)
	if err != nil {
		result.Errors = append(result.Errors, "打开数据库目录句柄失败: "+err.Error())
		return result
	}
	defer windows.CloseHandle(handle)

	guidPath, err := finalPathName(handle, volumeNameGUID)
	if err != nil {
		result.Errors = append(result.Errors, "GetFinalPathNameByHandle(GUID) 失败: "+err.Error())
		return result
	}
	dosPath, dosErr := finalPathName(handle, volumeNameDOS)
	if dosErr == nil {
		result.PhysicalDrive = driveLetterOf(strings.TrimPrefix(dosPath, `\\?\`))
	}
	result.ResolvedThroughLink = result.PhysicalDrive != "" && result.LogicalDrive != "" &&
		!strings.EqualFold(result.PhysicalDrive, result.LogicalDrive)

	volumeDevice, volumeID, err := volumeDevicePath(guidPath)
	if err != nil {
		result.Errors = append(result.Errors, err.Error())
		return result
	}
	result.VolumeID = volumeID

	disks, err := volumeDiskNumbers(volumeDevice)
	if err != nil {
		result.Errors = append(result.Errors, "取卷的物理盘 extent 失败: "+err.Error())
		return result
	}
	result.PhysicalDiskNumbers = disks

	models := make([]string, 0, len(disks))
	buses := make([]string, 0, len(disks))
	mediums := make([]string, 0, len(disks))
	evidences := make([]string, 0, len(disks))
	for _, disk := range disks {
		descriptor, seekPenaltyKnown, incursSeekPenalty, diskErr := queryPhysicalDisk(disk)
		if diskErr != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("PhysicalDrive%d 查询失败: %v", disk, diskErr))
			continue
		}
		if descriptor.model != "" {
			models = append(models, descriptor.model)
		}
		if descriptor.busType != "" {
			buses = append(buses, descriptor.busType)
		}
		switch {
		case seekPenaltyKnown && !incursSeekPenalty:
			mediums = append(mediums, MediumSSD)
			evidences = append(evidences, fmt.Sprintf("PhysicalDrive%d seekPenalty=false", disk))
		case seekPenaltyKnown && incursSeekPenalty:
			mediums = append(mediums, MediumHDD)
			evidences = append(evidences, fmt.Sprintf("PhysicalDrive%d seekPenalty=true", disk))
		case descriptor.busType == "NVMe":
			mediums = append(mediums, MediumSSD)
			evidences = append(evidences, fmt.Sprintf("PhysicalDrive%d busType=NVMe（seek penalty 描述符不可用）", disk))
		default:
			mediums = append(mediums, MediumUnknown)
			evidences = append(evidences, fmt.Sprintf("PhysicalDrive%d 无 seek penalty 描述符且总线不足以判定", disk))
		}
	}
	result.Model = strings.Join(dedupe(models), " + ")
	result.BusType = strings.Join(dedupe(buses), " + ")
	result.MediumEvidence = strings.Join(evidences, "; ")
	result.Medium = combineMediums(mediums)
	return result
}

// combineMediums 合并跨盘卷的逐盘结论：只要有一块盘判不出来，整卷就是 unknown；
// 混合介质（SSD+HDD 跨盘）同样不能折叠成其中一种，否则报告会声称一个不存在的
// 均质存储。
func combineMediums(mediums []string) string {
	if len(mediums) == 0 {
		return MediumUnknown
	}
	first := mediums[0]
	for _, medium := range mediums[1:] {
		if medium != first {
			return MediumUnknown
		}
	}
	return first
}

func dedupe(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func driveLetterOf(path string) string {
	volume := filepath.VolumeName(path)
	if len(volume) == 2 && volume[1] == ':' {
		return strings.ToUpper(volume[:1])
	}
	return ""
}

func openDirectory(path string) (windows.Handle, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return windows.InvalidHandle, err
	}
	return windows.CreateFile(name, 0, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
}

func finalPathName(handle windows.Handle, flags uint32) (string, error) {
	buffer := make([]uint16, windows.MAX_PATH)
	for {
		n, err := windows.GetFinalPathNameByHandle(handle, &buffer[0], uint32(len(buffer)), flags)
		if err != nil {
			return "", err
		}
		if int(n) < len(buffer) {
			return windows.UTF16ToString(buffer[:n]), nil
		}
		buffer = make([]uint16, n+1)
	}
}

// volumeDevicePath 从 \\?\Volume{GUID}\... 形式的最终路径中截出可直接 CreateFile
// 的卷设备名（不带尾部反斜线），并返回裸 GUID 作为稳定卷标识。
func volumeDevicePath(guidPath string) (device, volumeID string, err error) {
	const prefix = `\\?\Volume{`
	if !strings.HasPrefix(guidPath, prefix) {
		return "", "", fmt.Errorf("最终路径不是卷 GUID 形式，无法定位卷设备")
	}
	end := strings.Index(guidPath, "}")
	if end < 0 {
		return "", "", fmt.Errorf("卷 GUID 形式不完整")
	}
	device = guidPath[:end+1]
	volumeID = guidPath[len(`\\?\Volume`) : end+1]
	return device, volumeID, nil
}

func volumeDiskNumbers(devicePath string) ([]int, error) {
	name, err := windows.UTF16PtrFromString(devicePath)
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateFile(name, 0, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE,
		nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(handle)

	buffer := make([]byte, volumeDiskExtentsBufferSize)
	var returned uint32
	if err := windows.DeviceIoControl(handle, ioctlVolumeGetVolumeDiskExtents, nil, 0,
		&buffer[0], uint32(len(buffer)), &returned, nil); err != nil {
		return nil, err
	}
	if returned < 8 {
		return nil, fmt.Errorf("VOLUME_DISK_EXTENTS 返回长度 %d 过短", returned)
	}
	count := int(binary.LittleEndian.Uint32(buffer[0:4]))
	disks := make([]int, 0, count)
	for i := 0; i < count; i++ {
		offset := 8 + i*24
		if offset+4 > int(returned) {
			return nil, fmt.Errorf("VOLUME_DISK_EXTENTS 声明 %d 个 extent，但缓冲区只有 %d 字节", count, returned)
		}
		disks = append(disks, int(binary.LittleEndian.Uint32(buffer[offset:offset+4])))
	}
	if len(disks) == 0 {
		return nil, fmt.Errorf("卷没有报告任何物理盘 extent")
	}
	return dedupeInts(disks), nil
}

func dedupeInts(values []int) []int {
	seen := make(map[int]struct{}, len(values))
	result := make([]int, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

type deviceDescriptor struct {
	model   string
	busType string
}

func queryPhysicalDisk(disk int) (descriptor deviceDescriptor, seekPenaltyKnown, incursSeekPenalty bool, err error) {
	name, err := windows.UTF16PtrFromString(fmt.Sprintf(`\\.\PhysicalDrive%d`, disk))
	if err != nil {
		return descriptor, false, false, err
	}
	// access=0 只请求设备元数据，不请求读写权限；这是非管理员进程也能成功打开
	// 物理盘并发起 IOCTL_STORAGE_QUERY_PROPERTY 的前提。
	handle, err := windows.CreateFile(name, 0, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE,
		nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		return descriptor, false, false, err
	}
	defer windows.CloseHandle(handle)

	if raw, queryErr := storageQuery(handle, storageDeviceProperty, storageDeviceDescriptorBufferSize); queryErr == nil {
		descriptor = parseDeviceDescriptor(raw)
	} else {
		err = fmt.Errorf("设备描述符: %w", queryErr)
	}

	if raw, queryErr := storageQuery(handle, storageDeviceSeekPenaltyProperty, 64); queryErr == nil && len(raw) >= 9 {
		seekPenaltyKnown = true
		incursSeekPenalty = raw[8] != 0
	}
	return descriptor, seekPenaltyKnown, incursSeekPenalty, err
}

func storageQuery(handle windows.Handle, propertyID uint32, outSize int) ([]byte, error) {
	input := make([]byte, storagePropertyQueryHeaderSize)
	binary.LittleEndian.PutUint32(input[0:4], propertyID)
	binary.LittleEndian.PutUint32(input[4:8], propertyStandardQuery)
	output := make([]byte, outSize)
	var returned uint32
	if err := windows.DeviceIoControl(handle, ioctlStorageQueryProperty, &input[0], uint32(len(input)),
		&output[0], uint32(len(output)), &returned, nil); err != nil {
		return nil, err
	}
	return output[:returned], nil
}

// parseDeviceDescriptor 按 STORAGE_DEVICE_DESCRIPTOR 的固定布局取厂商、产品与
// 总线类型。序列号字段刻意不读取：它是可识别的硬件标识，对性能结论没有价值。
func parseDeviceDescriptor(raw []byte) deviceDescriptor {
	var descriptor deviceDescriptor
	if len(raw) < 36 {
		return descriptor
	}
	vendor := nulTerminatedAt(raw, binary.LittleEndian.Uint32(raw[12:16]))
	product := nulTerminatedAt(raw, binary.LittleEndian.Uint32(raw[16:20]))
	busType := binary.LittleEndian.Uint32(raw[28:32])
	model := cleanDeviceString(vendor + " " + product)
	descriptor.model = model
	if name, ok := busTypeNames[busType]; ok {
		descriptor.busType = name
	} else if busType != 0 {
		descriptor.busType = fmt.Sprintf("bus-%d", busType)
	}
	return descriptor
}

func nulTerminatedAt(raw []byte, offset uint32) string {
	if offset == 0 || int(offset) >= len(raw) {
		return ""
	}
	tail := raw[offset:]
	if end := strings.IndexByte(string(tail), 0); end >= 0 {
		return string(tail[:end])
	}
	return string(tail)
}
