package descriptor

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	version "github.com/RecRivenVI/gallery/version"
)

type Descriptor struct {
	ProtocolVersion int    `json:"protocolVersion"`
	APIVersion      string `json:"apiVersion"`
	Address         string `json:"address"`
	PID             int    `json:"pid"`
	StartupNonce    string `json:"startupNonce"`
	Ownership       string `json:"ownership"`
}

func New(address string) (Descriptor, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return Descriptor{}, err
	}
	return Descriptor{
		ProtocolVersion: version.DescriptorVersion, APIVersion: version.APIVersion,
		Address: address, PID: os.Getpid(), StartupNonce: hex.EncodeToString(raw[:]), Ownership: "user-started",
	}, nil
}

func Publish(runtimeDir string, value Descriptor) (string, error) {
	path := filepath.Join(runtimeDir, "galleryd.json")
	temp, err := os.CreateTemp(runtimeDir, ".galleryd-*.tmp")
	if err != nil {
		return "", err
	}
	tempName := temp.Name()
	ok := false
	defer func() {
		_ = temp.Close()
		if !ok {
			_ = os.Remove(tempName)
		}
	}()
	if err := temp.Chmod(0o600); err != nil {
		return "", err
	}
	encoder := json.NewEncoder(temp)
	encoder.SetEscapeHTML(true)
	if err := encoder.Encode(value); err != nil {
		return "", err
	}
	if err := temp.Sync(); err != nil {
		return "", err
	}
	if err := temp.Close(); err != nil {
		return "", err
	}
	// bootstrap 在调用 Publish 前已经取得 AppDirs 独占锁，因此既有 descriptor 只可能
	// 属于上一个异常退出的进程。Windows 的 os.Rename 不保证覆盖既有目标；先删除陈旧
	// 文件再发布，崩溃窗口最多留下“无就绪信号”，绝不能继续暴露旧 PID/端口。
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return "", err
	}
	if err := os.Rename(tempName, path); err != nil {
		return "", err
	}
	ok = true
	return path, nil
}

func RemoveIfOwned(path, startupNonce string) error {
	content, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var current Descriptor
	if err := json.Unmarshal(content, &current); err != nil {
		return err
	}
	if current.StartupNonce != startupNonce {
		return fmt.Errorf("runtime descriptor ownership 已改变")
	}
	return os.Remove(path)
}
