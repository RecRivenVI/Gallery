//go:build windows || aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package fileidentity_test

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/RecRivenVI/gallery/internal/platform/fileidentity"
	"github.com/RecRivenVI/gallery/internal/ports"
)

func exerciseStableFileIdentity(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	firstPath := dir + string(os.PathSeparator) + "first.bin"
	secondPath := dir + string(os.PathSeparator) + "second.bin"
	linkPath := dir + string(os.PathSeparator) + "first-link.bin"
	renamePath := dir + string(os.PathSeparator) + "renamed.bin"

	mustWriteFile(t, firstPath, []byte("first"))
	mustWriteFile(t, secondPath, []byte("second"))
	first := mustOpen(t, firstPath)
	defer first.Close()
	second := mustOpen(t, secondPath)
	defer second.Close()

	provider := fileidentity.OS{}
	firstID := mustIdentify(t, provider, first)
	secondID := mustIdentify(t, provider, second)
	if firstID == secondID {
		t.Fatal("不同文件获得了相同候选身份")
	}
	if !strings.HasPrefix(firstID.Encoded, "gallery-file-identity:v1:") {
		t.Fatal("候选身份缺少版本化前缀")
	}
	if strings.Contains(firstID.Encoded, dir) || strings.Contains(firstID.Encoded, "first.bin") {
		t.Fatal("候选身份泄露了路径")
	}

	if err := os.Link(firstPath, linkPath); err != nil {
		t.Fatal("创建硬链接失败")
	}
	link := mustOpen(t, linkPath)
	linkID := mustIdentify(t, provider, link)
	_ = link.Close()
	if linkID != firstID {
		t.Fatal("同一文件的硬链接身份不一致")
	}

	if err := os.Rename(firstPath, renamePath); err != nil {
		t.Fatal("重命名测试文件失败")
	}
	rename := mustOpen(t, renamePath)
	renameID := mustIdentify(t, provider, rename)
	_ = rename.Close()
	if renameID != firstID {
		t.Fatal("同一文件重命名后身份改变")
	}
	mustWriteFile(t, renamePath, []byte("changed-content"))
	changed := mustOpen(t, renamePath)
	changedID := mustIdentify(t, provider, changed)
	_ = changed.Close()
	if changedID != firstID {
		t.Fatal("候选位置身份被错误实现为内容身份")
	}

	// 旧句柄继续指向原文件；在原路径创建替代文件后不能被路径状态混淆。
	mustWriteFile(t, firstPath, []byte("replacement"))
	openHandleID := mustIdentify(t, provider, first)
	replacement := mustOpen(t, firstPath)
	replacementID := mustIdentify(t, provider, replacement)
	_ = replacement.Close()
	if openHandleID != firstID {
		t.Fatal("路径替换改变了已打开句柄的身份")
	}
	if replacementID == firstID {
		t.Fatal("路径替代文件与旧句柄身份混淆")
	}
}

func TestRejectsInvalidAndNonRegularHandles(t *testing.T) {
	provider := fileidentity.OS{}
	if _, err := provider.Identify(nil); !errors.Is(err, ports.ErrFileIdentityInvalidHandle) {
		t.Fatalf("nil 句柄错误不稳定：%v", err)
	}

	dir, err := os.Open(t.TempDir())
	if err != nil {
		t.Fatal("打开临时目录失败")
	}
	defer dir.Close()
	if _, err := provider.Identify(dir); !errors.Is(err, ports.ErrFileIdentityNotRegular) {
		t.Fatalf("非普通文件错误不稳定：%v", err)
	}

	path := t.TempDir() + string(os.PathSeparator) + "closed.bin"
	mustWriteFile(t, path, []byte("closed"))
	closed := mustOpen(t, path)
	if err := closed.Close(); err != nil {
		t.Fatal("关闭测试句柄失败")
	}
	if _, err := provider.Identify(closed); !errors.Is(err, ports.ErrFileIdentityInvalidHandle) {
		t.Fatalf("已关闭句柄错误不稳定：%v", err)
	}
}

func mustWriteFile(t *testing.T, path string, content []byte) {
	t.Helper()
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal("写入测试文件失败")
	}
}

func mustOpen(t *testing.T, path string) *os.File {
	t.Helper()
	file, err := openTestFile(path)
	if err != nil {
		t.Fatal("打开测试文件失败")
	}
	return file
}

func mustIdentify(t *testing.T, provider ports.FileIdentityProvider, file *os.File) ports.FileIdentityCandidate {
	t.Helper()
	identity, err := provider.Identify(file)
	if err != nil {
		t.Fatalf("读取候选身份失败：%v", err)
	}
	if identity.Encoded == "" {
		t.Fatal("候选身份为空")
	}
	return identity
}
