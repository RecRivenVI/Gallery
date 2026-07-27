//go:build linux

package hostfacts

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

func collectHost() (hostInfo, error) {
	var info hostInfo
	var problems []string

	if model, err := firstFieldValue("/proc/cpuinfo", []string{"model name", "Model", "Processor"}); err == nil {
		info.cpuModel = cleanDeviceString(model)
	} else {
		problems = append(problems, "CPU 型号: "+err.Error())
	}

	if total, err := memTotalBytes(); err == nil {
		info.memoryTotalBytes = total
	} else {
		problems = append(problems, "物理内存: "+err.Error())
	}

	if version := linuxOSVersion(); version != "" {
		info.osVersion = version
	} else {
		problems = append(problems, "操作系统版本不可用")
	}

	if len(problems) > 0 {
		return info, fmt.Errorf("%s", strings.Join(problems, "; "))
	}
	return info, nil
}

func linuxOSVersion() string {
	parts := make([]string, 0, 2)
	if pretty, err := osReleaseValue("PRETTY_NAME"); err == nil && pretty != "" {
		parts = append(parts, pretty)
	}
	var uname unix.Utsname
	if err := unix.Uname(&uname); err == nil {
		release := strings.TrimRight(string(uname.Release[:]), "\x00")
		if release != "" {
			parts = append(parts, "kernel "+release)
		}
	}
	return strings.Join(parts, " ")
}

func osReleaseValue(key string) (string, error) {
	file, err := os.Open("/etc/os-release")
	if err != nil {
		return "", err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		name, value, found := strings.Cut(line, "=")
		if !found || name != key {
			continue
		}
		return strings.Trim(strings.TrimSpace(value), `"`), nil
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return "", fmt.Errorf("/etc/os-release 中没有 %s", key)
}

func firstFieldValue(path string, keys []string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		name, value, found := strings.Cut(scanner.Text(), ":")
		if !found {
			continue
		}
		name = strings.TrimSpace(name)
		for _, key := range keys {
			if name == key {
				return strings.TrimSpace(value), nil
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return "", fmt.Errorf("%s 中没有 %v", path, keys)
}

func memTotalBytes() (int64, error) {
	content, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, err
	}
	for _, line := range strings.Split(string(content), "\n") {
		if !strings.HasPrefix(line, "MemTotal:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			break
		}
		kb, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			return 0, err
		}
		return kb * 1024, nil
	}
	return 0, fmt.Errorf("/proc/meminfo 中没有 MemTotal")
}

// collectStorage 在 Linux 上同样走到物理盘：先展开路径上的全部符号链接，再按
// st_dev 找到 /sys/dev/block 下的块设备，若它是分区则上溯到整盘，最后读
// queue/rotational 与 device/model。
//
// WSL2 的 /mnt/<盘符> 是 9p/drvfs 挂载，没有对应的块设备，这条路径会如实停在
// unknown——那正是需要被看见的事实：在 WSL 里测出来的存储数字不代表 Linux 原生
// ext4，更不代表宿主物理盘。
func collectStorage(dbPath string) Storage {
	result := Storage{Medium: MediumUnknown}
	absolute, err := filepath.Abs(dbPath)
	if err != nil {
		result.Errors = append(result.Errors, "解析绝对路径失败: "+err.Error())
		return result
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		result.Errors = append(result.Errors, "展开符号链接失败: "+err.Error())
		resolved = absolute
	}
	result.ResolvedThroughLink = resolved != absolute

	var stat syscall.Stat_t
	if err := syscall.Stat(resolved, &stat); err != nil {
		result.Errors = append(result.Errors, "stat 失败: "+err.Error())
		return result
	}
	major, minor := unix.Major(uint64(stat.Dev)), unix.Minor(uint64(stat.Dev))
	result.VolumeID = fmt.Sprintf("%d:%d", major, minor)
	if major == 0 {
		result.Errors = append(result.Errors, fmt.Sprintf("设备号 %d:%d 属于虚拟文件系统（例如 WSL 的 9p/drvfs），没有可判定介质的块设备", major, minor))
		return result
	}

	blockPath := fmt.Sprintf("/sys/dev/block/%d:%d", major, minor)
	target, err := filepath.EvalSymlinks(blockPath)
	if err != nil {
		result.Errors = append(result.Errors, "定位 /sys 块设备失败: "+err.Error())
		return result
	}
	disk := target
	if _, err := os.Stat(filepath.Join(target, "partition")); err == nil {
		disk = filepath.Dir(target)
	}
	diskName := filepath.Base(disk)

	if model, err := os.ReadFile(filepath.Join(disk, "device", "model")); err == nil {
		result.Model = cleanDeviceString(string(model))
	}
	if vendor, err := os.ReadFile(filepath.Join(disk, "device", "vendor")); err == nil && result.Model != "" {
		result.Model = cleanDeviceString(string(vendor) + " " + result.Model)
	}

	rotational, err := os.ReadFile(filepath.Join(disk, "queue", "rotational"))
	if err != nil {
		result.Errors = append(result.Errors, "读取 queue/rotational 失败: "+err.Error())
		return result
	}
	switch strings.TrimSpace(string(rotational)) {
	case "0":
		result.Medium = MediumSSD
	case "1":
		result.Medium = MediumHDD
	default:
		result.Errors = append(result.Errors, "queue/rotational 取值无法识别")
		return result
	}
	result.MediumEvidence = fmt.Sprintf("/sys/block/%s/queue/rotational=%s", diskName, strings.TrimSpace(string(rotational)))
	return result
}
