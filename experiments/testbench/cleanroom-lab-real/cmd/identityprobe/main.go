// identityprobe 验证 Media、ContentBlob、FileLocation 与 DerivedAsset 必须分离。
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

type observation struct {
	Operation       string `json:"operation"`
	LogicalMediaID  string `json:"logical_media_id"`
	SamePath        bool   `json:"same_path"`
	SameFileID      bool   `json:"same_file_id"`
	SameQuickHash   bool   `json:"same_quick_hash"`
	SameContentBlob bool   `json:"same_content_blob"`
}

type report struct {
	SchemaVersion            int           `json:"schema_version"`
	Observations             []observation `json:"observations"`
	QuickCollisionProved     bool          `json:"quick_collision_proved"`
	SameBlobTwoWorks         bool          `json:"same_blob_two_works"`
	DuplicateMediaSameWork   bool          `json:"duplicate_media_same_work"`
	HardlinkSupported        bool          `json:"hardlink_supported"`
	SymlinkSupported         bool          `json:"symlink_supported"`
	CrossVolumeActuallyRun   bool          `json:"cross_volume_actually_run"`
	FileIdentityProviderName string        `json:"file_identity_provider"`
}

type identity struct{ path, file, quick, full string }

func main() {
	dir := flag.String("dir", "results/identity-fixtures", "fixture directory")
	out := flag.String("out", "results/identity.json", "result JSON")
	flag.Parse()
	must(os.RemoveAll(*dir))
	must(os.MkdirAll(filepath.Join(*dir, "work-a"), 0o755))
	must(os.MkdirAll(filepath.Join(*dir, "work-b"), 0o755))
	p := filepath.Join(*dir, "work-a", "media.bin")
	base := payload('A')
	must(os.WriteFile(p, base, 0o644))
	orig := inspect(p)
	r := report{SchemaVersion: 1, FileIdentityProviderName: providerName()}
	add := func(op, mediaID, path string) {
		now := inspect(path)
		r.Observations = append(r.Observations, observation{op, mediaID, now.path == orig.path, now.file == orig.file, now.quick == orig.quick, now.full == orig.full})
	}
	add("original", "media-a-1", p)
	renamed := filepath.Join(*dir, "work-a", "renamed.bin")
	must(os.Rename(p, renamed))
	add("same-volume-rename", "media-a-1", renamed)
	moved := filepath.Join(*dir, "work-b", "renamed.bin")
	must(os.Rename(renamed, moved))
	add("same-volume-move", "media-a-1", moved)
	copyPath := filepath.Join(*dir, "work-b", "copy.bin")
	must(os.WriteFile(copyPath, base, 0o644))
	add("copy-to-second-work", "media-b-1", copyPath)
	r.SameBlobTwoWorks = inspect(copyPath).full == inspect(moved).full
	duplicate := filepath.Join(*dir, "work-b", "duplicate.bin")
	must(os.WriteFile(duplicate, base, 0o644))
	add("duplicate-occurrence-same-work", "media-b-2", duplicate)
	r.DuplicateMediaSameWork = inspect(duplicate).full == inspect(copyPath).full
	hard := filepath.Join(*dir, "work-b", "hard.bin")
	if os.Link(moved, hard) == nil {
		r.HardlinkSupported = true
		add("hardlink", "media-b-3", hard)
	}
	symlink := filepath.Join(*dir, "work-b", "symlink.bin")
	movedAbs, _ := filepath.Abs(moved)
	if os.Symlink(movedAbs, symlink) == nil {
		r.SymlinkSupported = true
		add("symlink", "media-b-4", symlink)
	}

	// 首尾 64 KiB 和大小相同，中间不同：快速指纹必然相同，完整哈希不同。
	c1 := filepath.Join(*dir, "collision-a.bin")
	c2 := filepath.Join(*dir, "collision-b.bin")
	must(os.WriteFile(c1, payload('X'), 0o644))
	must(os.WriteFile(c2, payload('Y'), 0o644))
	i1, i2 := inspect(c1), inspect(c2)
	r.QuickCollisionProved = i1.quick == i2.quick && i1.full != i2.full

	b, err := json.MarshalIndent(r, "", "  ")
	must(err)
	must(os.MkdirAll(filepath.Dir(*out), 0o755))
	must(os.WriteFile(*out, b, 0o644))
	fmt.Printf("quick-collision=%v same-blob-two-works=%v duplicate-media-same-work=%v hardlink=%v symlink=%v provider=%s\n",
		r.QuickCollisionProved, r.SameBlobTwoWorks, r.DuplicateMediaSameWork, r.HardlinkSupported, r.SymlinkSupported, r.FileIdentityProviderName)
}

func payload(middle byte) []byte {
	b := make([]byte, 192<<10)
	for i := range b {
		b[i] = 'Q'
	}
	for i := 64 << 10; i < 128<<10; i++ {
		b[i] = middle
	}
	return b
}

func inspect(path string) identity {
	abs, _ := filepath.Abs(path)
	f, err := os.Open(path)
	must(err)
	defer f.Close()
	info, err := f.Stat()
	must(err)
	full := sha256.New()
	_, err = io.Copy(full, f)
	must(err)
	first, last := make([]byte, min(int(info.Size()), 64<<10)), make([]byte, min(int(info.Size()), 64<<10))
	_, _ = f.ReadAt(first, 0)
	_, _ = f.ReadAt(last, max64(0, info.Size()-int64(len(last))))
	quick := sha256.New()
	fmt.Fprintf(quick, "%d\x00", info.Size())
	quick.Write(first)
	quick.Write(last)
	return identity{filepath.Clean(abs), fileIDByPath(path), hex.EncodeToString(quick.Sum(nil)), hex.EncodeToString(full.Sum(nil))}
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
func must(err error) {
	if err != nil {
		panic(err)
	}
}
