//go:build !windows

package main

import "fmt"

func requireSupportedPlatform() error {
	return fmt.Errorf("portable upgrade smoke 只能在 Windows 上执行")
}
