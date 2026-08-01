//go:build !windows && !darwin

package appdirs

import (
	"os"
	"path/filepath"
)

// Defaults 保留既有 XDG 目录映射；Windows x64 RC 之前不把这些平台列为验证目标。
func Defaults() (Dirs, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Dirs{}, err
	}
	config := envOr("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	data := envOr("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	state := envOr("XDG_STATE_HOME", filepath.Join(home, ".local", "state"))
	cache := envOr("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	return Dirs{
		Config: filepath.Join(config, "gallery"), Data: filepath.Join(data, "gallery"),
		State: filepath.Join(state, "gallery"), Cache: filepath.Join(cache, "gallery"),
		Logs: filepath.Join(state, "gallery", "logs"), Temp: filepath.Join(cache, "gallery", "tmp"),
		Runtime: filepath.Join(state, "gallery", "run"),
	}, nil
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
