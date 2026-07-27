//go:build !windows && !linux

package hostfacts

import (
	"fmt"
	"runtime"
)

// 本仓库当前只在 Windows 11 x64（正式目标）与 Linux（WSL2/CI runner）上运行
// testlab。其它平台不提供实测采集实现，因此如实返回「未实现」而不是拼一个看起来
// 像样的占位值：Reference Performance Gate 明确要求缺任一环境项的数字只能作为
// 方向性证据，伪造完整性比缺失更有害。
func collectHost() (hostInfo, error) {
	return hostInfo{}, fmt.Errorf("%s 上未实现主机事实采集", runtime.GOOS)
}

func collectStorage(string) Storage {
	return Storage{
		Medium: MediumUnknown,
		Errors: []string{fmt.Sprintf("%s 上未实现物理盘介质判定", runtime.GOOS)},
	}
}
