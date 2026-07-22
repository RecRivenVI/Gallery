// Package config 加载本机测试路径配置（Documents/本地/testlab.local.json）。该
// 文件不入库，只存在于本机工作树；仓库中只提交
// Documents/本地/testlab.local.example.json 模板。任何 tools/testlab 命令都不得
// 在配置缺失时猜测或扫描磁盘寻找路径，必须报出明确错误并提示用户先复制模板并
// 填写真实路径。
package config

import (
	"encoding/json"
	"fmt"
	"os"
)

// TestRoots 是两个物理测试根路径。
type TestRoots struct {
	SSD string `json:"ssd"`
	HDD string `json:"hdd"`
}

// GoToolchain 记录固定 Go 工具链路径，供需要重新编译 galleryd 的命令使用。
type GoToolchain struct {
	Windows string `json:"windows"`
	WSL     string `json:"wsl"`
}

// Config 是 testlab.local.json 的完整结构。Sources 按本文档「全部正式目标来源」
// 使用的逻辑 ID（pixiv/pixivFANBOX/Gank/Fantia/Patreon/Pawchive/X/微博/微博_Legacy/
// Venera）为键，值是该来源在本机的物理根路径。
type Config struct {
	SchemaVersion int               `json:"schemaVersion"`
	TestRoots     TestRoots         `json:"testRoots"`
	Sources       map[string]string `json:"sources"`
	GoToolchain   GoToolchain       `json:"goToolchain"`
}

// Load 从显式给定的路径读取本机测试配置。path 为空或文件不存在时返回明确错误，
// 不得回退为猜测常见路径或递归扫描磁盘。
func Load(path string) (Config, error) {
	if path == "" {
		return Config{}, fmt.Errorf("必须显式指定本地测试配置路径（-config 或等效参数），不得在缺失时猜测或扫描磁盘")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("读取本地测试配置失败（%s）：%w；请先复制 Documents/本地/testlab.local.example.json 为 Documents/本地/testlab.local.json 并按本机真实路径填写", path, err)
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("解析本地测试配置失败（%s）：%w", path, err)
	}
	if cfg.TestRoots.SSD == "" || cfg.TestRoots.HDD == "" {
		return Config{}, fmt.Errorf("本地测试配置（%s）缺少 testRoots.ssd 或 testRoots.hdd", path)
	}
	return cfg, nil
}

// SourceRoot 返回给定逻辑来源 ID 的本机物理路径；来源缺失时返回明确错误，不得
// 静默回退到空字符串或其它来源的路径。
func (c Config) SourceRoot(logicalID string) (string, error) {
	root, ok := c.Sources[logicalID]
	if !ok || root == "" {
		return "", fmt.Errorf("本地测试配置缺少来源 %q 的物理路径", logicalID)
	}
	return root, nil
}
