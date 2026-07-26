package sourceguard

import (
	"io/fs"
	"testing"

	"github.com/RecRivenVI/gallery/internal/platform/filesystem"
)

// TestLocalLinkPredicateMatchesPlatformAuthority 锁定本包的 isLink 与生产
// internal/platform/filesystem.IsLink 逐 mode 位一致。
//
// 本包刻意不 import 那个包：sourceguard 会被 testlabprobe 链接，而 probe 的既定边界是
// 「只导入 pkg/galleryapi 与标准库」，让它间接链接 internal/* 会削弱那条边界。测试文件
// 不参与 probe 的构建，因此可以直接 import 权威实现来防止两处判定漂移——判定语义只有
// 一份事实源，复制的是实现而不是决定权。
func TestLocalLinkPredicateMatchesPlatformAuthority(t *testing.T) {
	modes := []fs.FileMode{
		0,
		fs.ModeDir,
		fs.ModeSymlink,
		fs.ModeIrregular,
		fs.ModeDir | fs.ModeSymlink,
		fs.ModeDir | fs.ModeIrregular,
		fs.ModeNamedPipe,
		fs.ModeSocket,
		fs.ModeDevice,
		fs.ModeCharDevice,
		fs.ModeSetuid,
		fs.ModePerm,
	}
	for _, mode := range modes {
		if got, want := isLink(mode), filesystem.IsLink(mode); got != want {
			t.Fatalf("isLink(%v) = %v，权威实现 = %v；两处链接判定已漂移", mode, got, want)
		}
	}
}
