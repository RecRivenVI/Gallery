//go:build !windows

package main

import (
	"context"
	"fmt"
)

func holdControlWithoutDeleteSharing(string) (func() error, error) {
	return nil, fmt.Errorf("control 轮换阻断句柄只支持 Windows")
}

func watchNextFileWithoutDeleteSharing(context.Context, string) (<-chan pendingFileHold, func() error, error) {
	return nil, nil, fmt.Errorf("恢复候选落位阻断只支持 Windows")
}
