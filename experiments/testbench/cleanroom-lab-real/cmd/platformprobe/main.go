// platformprobe 用同一二进制源验证路径、Unicode、链接、时间与工具发现的跨平台语义。
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"golang.org/x/text/unicode/norm"
)

type report struct {
	SchemaVersion                                            int `json:"schema_version"`
	OS, Arch                                                 string
	CaseDistinct, NFCNFDistinct, Hardlink, Symlink, LongPath bool
	TimestampDeltaNS                                         int64
	FFMPEGFound, FFprobeFound                                bool
	PathSeparator                                            string
	RuntimeExecuted                                          bool
}

func main() {
	dir := flag.String("dir", "results/platform-fixtures", "fixture directory")
	out := flag.String("out", "results/platform.json", "result JSON")
	flag.Parse()
	must(os.RemoveAll(*dir))
	must(os.MkdirAll(*dir, 0o755))
	a := filepath.Join(*dir, "Case.txt")
	b := filepath.Join(*dir, "case.txt")
	must(os.WriteFile(a, []byte("A"), 0o644))
	must(os.WriteFile(b, []byte("B"), 0o644))
	entries, _ := os.ReadDir(*dir)
	caseCount := 0
	for _, e := range entries {
		if strings.EqualFold(e.Name(), "case.txt") {
			caseCount++
		}
	}
	nfc := norm.NFC.String("é") + ".txt"
	nfd := norm.NFD.String("é") + ".txt"
	must(os.WriteFile(filepath.Join(*dir, nfc), []byte("nfc"), 0o644))
	must(os.WriteFile(filepath.Join(*dir, nfd), []byte("nfd"), 0o644))
	entries, _ = os.ReadDir(*dir)
	unicodeCount := 0
	for _, e := range entries {
		if norm.NFC.String(e.Name()) == nfc {
			unicodeCount++
		}
	}
	orig := filepath.Join(*dir, "original.bin")
	must(os.WriteFile(orig, []byte("payload"), 0o644))
	hard := os.Link(orig, filepath.Join(*dir, "hard.bin")) == nil
	abs, _ := filepath.Abs(orig)
	sym := os.Symlink(abs, filepath.Join(*dir, "sym.bin")) == nil
	long := *dir
	for i := 0; i < 8; i++ {
		long = filepath.Join(long, strings.Repeat("segment", 5))
	}
	longOK := os.MkdirAll(long, 0o755) == nil && os.WriteFile(filepath.Join(long, "x.bin"), []byte("x"), 0o644) == nil
	want := time.Unix(1_700_000_000, 123_456_789)
	must(os.Chtimes(orig, want, want))
	info, _ := os.Stat(orig)
	delta := info.ModTime().Sub(want).Nanoseconds()
	if delta < 0 {
		delta = -delta
	}
	_, ffmpeg := exec.LookPath("ffmpeg")
	_, ffprobe := exec.LookPath("ffprobe")
	r := report{1, runtime.GOOS, runtime.GOARCH, caseCount == 2, unicodeCount == 2, hard, sym, longOK, delta, ffmpeg == nil, ffprobe == nil, string(os.PathSeparator), true}
	data, e := json.MarshalIndent(r, "", "  ")
	must(e)
	must(os.MkdirAll(filepath.Dir(*out), 0o755))
	must(os.WriteFile(*out, data, 0o644))
	fmt.Printf("os=%s case-distinct=%v unicode-distinct=%v hardlink=%v symlink=%v long=%v ffmpeg=%v\n", r.OS, r.CaseDistinct, r.NFCNFDistinct, r.Hardlink, r.Symlink, r.LongPath, r.FFMPEGFound)
}
func must(e error) {
	if e != nil {
		panic(e)
	}
}
