package sourcelab

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// StateSchemaVersion 随 State 结构变化递增。
const StateSchemaVersion = 1

type persistedState struct {
	SchemaVersion int   `json:"schemaVersion"`
	State         State `json:"state"`
}

// LoadState 读取上一次运行留下的状态。
//
// 文件不存在不是错误：那只是「这是第一次运行」。把它当错误会逼调用方在首次运行时
// 传一个假状态，反而让「续跑」与「重头跑」不可区分。
func LoadState(path string) (*State, error) {
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("读取运行状态失败: %w", err)
	}
	var persisted persistedState
	if err := json.Unmarshal(data, &persisted); err != nil {
		return nil, fmt.Errorf("解析运行状态失败: %w", err)
	}
	if persisted.SchemaVersion != StateSchemaVersion {
		return nil, fmt.Errorf("运行状态 schemaVersion = %d，本工具只支持 %d", persisted.SchemaVersion, StateSchemaVersion)
	}
	return &persisted.State, nil
}

// SaveState 原子写出运行状态。状态只含 ID、计数与折叠哈希，不含任何路径或名字，
// 因此可以安全地留在授权测试根内。
func SaveState(state *State, path string) error {
	if path == "" || state == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(persistedState{SchemaVersion: StateSchemaVersion, State: *state}, "", "  ")
	if err != nil {
		return err
	}
	temp := path + ".tmp"
	file, err := os.OpenFile(temp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := file.Write(encoded); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(temp, path)
}
