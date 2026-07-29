//go:build !windows

package main

import "fmt"

func holdControlWithoutDeleteSharing(string) (func() error, error) {
	return nil, fmt.Errorf("control 轮换阻断句柄只支持 Windows")
}
