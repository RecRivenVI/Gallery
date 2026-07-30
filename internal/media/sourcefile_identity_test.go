//go:build windows || aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package media_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/RecRivenVI/gallery/internal/contract/fault"
	"github.com/RecRivenVI/gallery/internal/media"
	"github.com/RecRivenVI/gallery/internal/platform/fileidentity"
	"github.com/RecRivenVI/gallery/internal/ports"
)

type unavailableFileIdentity struct{}

func (unavailableFileIdentity) Identify(*os.File) (ports.FileIdentityCandidate, error) {
	return ports.FileIdentityCandidate{}, ports.ErrFileIdentityUnavailable
}

func TestLocateAndHashUseSameHandlePlatformIdentity(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "media.bin")
	payload := []byte("same handle platform identity")
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	provider := fileidentity.OS{}
	located, err := media.LocateSourceFileWithIdentity(root, "media.bin", provider)
	if err != nil {
		t.Fatal(err)
	}
	if located.PlatformIdentityKind != ports.FileIdentityKindV1 || located.PlatformIdentityValue == "" {
		t.Fatalf("最终定位未返回平台身份: %+v", located)
	}
	hashed, err := media.HashSourceFileWithOptions(root, "media.bin", media.HashOptions{
		ExpectedSize: located.Size, ExpectedModTimeNanos: located.ModTimeNanos, HasExpectedIdentity: true,
		ExpectedPlatformIdentityKind:  located.PlatformIdentityKind,
		ExpectedPlatformIdentityValue: located.PlatformIdentityValue,
		FileIdentityProvider:          provider,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !hashed.HasObservedModTime || hashed.ModTimeNanos != located.ModTimeNanos ||
		hashed.PlatformIdentityKind != located.PlatformIdentityKind ||
		hashed.PlatformIdentityValue != located.PlatformIdentityValue {
		t.Fatalf("Hash 结果未保持同一最终观察: locate=%+v hash=%+v", located, hashed)
	}

	oldPath := filepath.Join(t.TempDir(), "old-media.bin")
	if err := os.Rename(path, oldPath); err != nil {
		t.Fatal(err)
	}
	replacement := append([]byte(nil), payload...)
	replacement[0] ^= 0x55
	if err := os.WriteFile(path, replacement, 0o600); err != nil {
		t.Fatal(err)
	}
	oldInfo := mustStat(t, oldPath)
	if err := os.Chtimes(path, oldInfo.ModTime(), oldInfo.ModTime()); err != nil {
		t.Fatal(err)
	}
	_, err = media.HashSourceFileWithOptions(root, "media.bin", media.HashOptions{
		ExpectedSize: located.Size, ExpectedModTimeNanos: located.ModTimeNanos, HasExpectedIdentity: true,
		ExpectedPlatformIdentityKind:  located.PlatformIdentityKind,
		ExpectedPlatformIdentityValue: located.PlatformIdentityValue,
		FileIdentityProvider:          provider,
	})
	var structured *fault.Error
	if !errors.As(err, &structured) || structured.Code != fault.CodeContentChangedDuringHash {
		t.Fatalf("同 stat 路径替换未由平台身份拒绝: %v", err)
	}
}

func TestFileIdentityUnavailableFallsBackWithoutInventingIdentity(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "media.bin"), []byte("fallback"), 0o600); err != nil {
		t.Fatal(err)
	}
	located, err := media.LocateSourceFileWithIdentity(root, "media.bin", unavailableFileIdentity{})
	if err != nil {
		t.Fatal(err)
	}
	if located.PlatformIdentityKind != "" || located.PlatformIdentityValue != "" {
		t.Fatalf("不可用 provider 不得伪造身份: %+v", located)
	}
	hashed, err := media.HashSourceFileWithOptions(root, "media.bin", media.HashOptions{
		ExpectedSize: located.Size, ExpectedModTimeNanos: located.ModTimeNanos, HasExpectedIdentity: true,
		FileIdentityProvider: unavailableFileIdentity{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if hashed.PlatformIdentityKind != "" || hashed.PlatformIdentityValue != "" || hashed.Blob.Digest == "" {
		t.Fatalf("降级路径应只缺少平台身份，完整 digest 仍必须存在: %+v", hashed)
	}
}

func mustStat(t *testing.T, path string) os.FileInfo {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info
}
