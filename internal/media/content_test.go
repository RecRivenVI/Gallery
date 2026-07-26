package media_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/RecRivenVI/gallery/internal/contract/fault"
	"github.com/RecRivenVI/gallery/internal/media"
)

// contentFixture 建立一个只读 Source 根与一个媒体文件，并返回该文件的 publication 身份
// 证据。它模拟扫描完成后 publication 冻结下来的 size/mtime/digest。
func contentFixture(t *testing.T, body []byte) (string, media.PublishedIdentity) {
	t.Helper()
	root := t.TempDir()
	path := filepath.Join(root, "media.bin")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(body)
	return root, media.PublishedIdentity{
		Algorithm: "sha256-v1", Digest: hex.EncodeToString(sum[:]),
		Size: info.Size(), MTimeNanos: info.ModTime().UnixNano(),
	}
}

func faultCode(t *testing.T, err error) fault.Code {
	t.Helper()
	var structured *fault.Error
	if !errors.As(err, &structured) {
		t.Fatalf("非结构化错误: %v", err)
	}
	return structured.Code
}

// TestContentHandleServesRangesWithoutCopyingWholeFile 是 MED-1 的核心回归：正文读取
// 只读请求区间，不产生任何副本，并且整文件读取顺带复算 digest。
func TestContentHandleServesRangesWithoutCopyingWholeFile(t *testing.T) {
	body := []byte("0123456789abcdefghij")
	root, published := contentFixture(t, body)
	handle, err := media.OpenContent(root, "media.bin", published)
	if err != nil {
		t.Fatalf("打开正文句柄失败: %v", err)
	}
	defer handle.Close()
	if handle.Size != int64(len(body)) {
		t.Fatalf("Size = %d want %d", handle.Size, len(body))
	}
	// 打开阶段不读取任何正文字节：Source 根下不得出现任何新条目，AppDirs 也没有参与。
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 1 {
		t.Fatalf("打开正文句柄改变了 Source 根: %v entries=%d", err, len(entries))
	}
	for _, item := range []struct {
		name         string
		start, count int64
		want         string
	}{
		{"整文件", 0, int64(len(body)), string(body)},
		{"前缀", 0, 4, "0123"},
		{"中段", 8, 5, "89abc"},
		{"末尾", 15, 5, "fghij"},
		{"单字节", 19, 1, "j"},
	} {
		t.Run(item.name, func(t *testing.T) {
			var buffer bytes.Buffer
			written, err := handle.CopyRange(&buffer, item.start, item.count, item.start == 0 && item.count == handle.Size)
			if err != nil {
				t.Fatalf("区间读取失败: %v", err)
			}
			if written != item.count || buffer.String() != item.want {
				t.Fatalf("区间结果 = %q (%d 字节) want %q", buffer.String(), written, item.want)
			}
		})
	}
	// SectionReader 使用定位读，区间读取之间互不影响，重复读取同一区间结果稳定。
	var first, second bytes.Buffer
	if _, err := handle.CopyRange(&first, 3, 4, false); err != nil {
		t.Fatal(err)
	}
	if _, err := handle.CopyRange(&second, 3, 4, false); err != nil {
		t.Fatal(err)
	}
	if first.String() != second.String() || first.String() != "3456" {
		t.Fatalf("重复区间读取不稳定: %q %q", first.String(), second.String())
	}
	if _, err := handle.CopyRange(io.Discard, 0, handle.Size+1, false); faultCode(t, err) != fault.CodeRangeInvalid {
		t.Fatalf("越界区间未拒绝")
	}
}

// TestOpenContentRejectsStaleIdentityBeforeSendingBytes 证明 publication 冻结的 size 与
// mtime 证据让内容变化在发送任何字节之前就被判定，因此常规内容替换仍得到干净的
// CONTENT_CHANGED 而不是中途中断的响应。
func TestOpenContentRejectsStaleIdentityBeforeSendingBytes(t *testing.T) {
	t.Run("大小变化", func(t *testing.T) {
		root, published := contentFixture(t, []byte("original"))
		if err := os.WriteFile(filepath.Join(root, "media.bin"), []byte("original-longer"), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := media.OpenContent(root, "media.bin", published)
		if faultCode(t, err) != fault.CodeContentChanged {
			t.Fatalf("大小变化错误 = %v", err)
		}
	})
	t.Run("mtime 变化", func(t *testing.T) {
		root, published := contentFixture(t, []byte("original"))
		path := filepath.Join(root, "media.bin")
		if err := os.WriteFile(path, []byte("replaced"), 0o600); err != nil {
			t.Fatal(err)
		}
		// 显式改写 mtime 而不是依赖「重写文件必然改变 mtime」：文件时间戳的时钟粒度在
		// Windows 上约 15 毫秒，同一刻内的两次写入会得到相同 mtime，使断言变成对时钟
		// 粒度的测试。这里要断言的是「发布 mtime 与当前 mtime 不一致即拒绝」。
		changed := time.Unix(0, published.MTimeNanos).Add(time.Hour)
		if err := os.Chtimes(path, changed, changed); err != nil {
			t.Fatal(err)
		}
		handle, err := media.OpenContent(root, "media.bin", published)
		if handle != nil {
			handle.Close()
		}
		if faultCode(t, err) != fault.CodeContentChanged {
			t.Fatalf("mtime 变化错误 = %v", err)
		}
	})
	t.Run("算法或 digest 非法", func(t *testing.T) {
		root, published := contentFixture(t, []byte("original"))
		published.Algorithm = "sha1-v1"
		if _, err := media.OpenContent(root, "media.bin", published); faultCode(t, err) != fault.CodeContentChanged {
			t.Fatalf("非法算法错误 = %v", err)
		}
	})
	t.Run("路径越界", func(t *testing.T) {
		root, published := contentFixture(t, []byte("original"))
		if _, err := media.OpenContent(root, "../outside.bin", published); faultCode(t, err) != fault.CodePathEscape {
			t.Fatalf("越界路径错误 = %v", err)
		}
	})
	t.Run("文件消失", func(t *testing.T) {
		root, published := contentFixture(t, []byte("original"))
		if err := os.Remove(filepath.Join(root, "media.bin")); err != nil {
			t.Fatal(err)
		}
		if _, err := media.OpenContent(root, "media.bin", published); faultCode(t, err) != fault.CodeContentDisappeared {
			t.Fatalf("文件消失错误 = %v", err)
		}
	})
}

// TestWholeFileReadDetectsIdentityPreservingSubstitution 覆盖身份证据无法发现的攻击面：
// 同大小、恢复 mtime、只改中间字节。整文件读取的顺带 digest 复算必须拒绝这种替换；
// 区间读取无法复算完整 digest，这一已知边界由 verify 扫描档案兜底。
func TestWholeFileReadDetectsIdentityPreservingSubstitution(t *testing.T) {
	body := []byte("same-size-original")
	root, published := contentFixture(t, body)
	path := filepath.Join(root, "media.bin")
	original, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	substituted := []byte("same-size-REPLACE!")
	if len(substituted) != len(body) {
		t.Fatalf("测试数据长度不一致: %d %d", len(substituted), len(body))
	}
	if err := os.WriteFile(path, substituted, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, original.ModTime(), original.ModTime()); err != nil {
		t.Fatal(err)
	}
	handle, err := media.OpenContent(root, "media.bin", published)
	if err != nil {
		t.Fatalf("恢复时间戳的替换必须能打开（身份证据全部成立）: %v", err)
	}
	defer handle.Close()
	if _, err := handle.CopyRange(io.Discard, 0, handle.Size, true); faultCode(t, err) != fault.CodeContentChanged {
		t.Fatalf("整文件复算未发现内容替换: %v", err)
	}
	// 区间读取只能依赖身份证据，因此这里不报错；这是被明确接受的边界，不是缺陷。
	if _, err := handle.CopyRange(io.Discard, 0, 4, false); err != nil {
		t.Fatalf("区间读取不应因无法复算完整 digest 而失败: %v", err)
	}
}

// TestCopyRangeRejectsTruncationDuringRead 证明读取期间的截断不会被当作正常结束：短读
// 必须变成 CONTENT_CHANGED，让调用方中断响应而不是返回不完整正文。
func TestCopyRangeRejectsTruncationDuringRead(t *testing.T) {
	body := bytes.Repeat([]byte("a"), 4096)
	root, published := contentFixture(t, body)
	handle, err := media.OpenContent(root, "media.bin", published)
	if err != nil {
		t.Fatal(err)
	}
	defer handle.Close()
	if err := os.Truncate(filepath.Join(root, "media.bin"), 16); err != nil {
		t.Fatal(err)
	}
	if _, err := handle.CopyRange(io.Discard, 0, handle.Size, true); faultCode(t, err) != fault.CodeContentChanged {
		t.Fatalf("读取期间截断错误 = %v", err)
	}
}

// TestOpenContentAcceptsMissingPublishedMTimeEvidence 覆盖 catalog v13 之前发布的历史
// 快照：mtime 证据为 0 时只比对 size 与整文件 digest，既不伪造证据，也不因缺证据而
// 拒绝服务。
func TestOpenContentAcceptsMissingPublishedMTimeEvidence(t *testing.T) {
	body := []byte("legacy-publication")
	root, published := contentFixture(t, body)
	published.MTimeNanos = 0
	path := filepath.Join(root, "media.bin")
	// 重写同样的内容只改变 mtime；没有 mtime 证据时这不构成内容变化。
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	handle, err := media.OpenContent(root, "media.bin", published)
	if err != nil {
		t.Fatalf("缺少 mtime 证据的历史快照不应被拒绝: %v", err)
	}
	defer handle.Close()
	var buffer bytes.Buffer
	if _, err := handle.CopyRange(&buffer, 0, handle.Size, true); err != nil {
		t.Fatalf("整文件读取失败: %v", err)
	}
	if !bytes.Equal(buffer.Bytes(), body) {
		t.Fatalf("正文不一致")
	}
	// 但内容真的变了（同大小）仍必须由 digest 复算拒绝。
	if err := os.WriteFile(path, []byte("legacy-REPLACEMENT"), 0o600); err != nil {
		t.Fatal(err)
	}
	stale, err := media.OpenContent(root, "media.bin", published)
	if err != nil {
		t.Fatalf("同大小替换在缺少 mtime 证据时应可打开: %v", err)
	}
	defer stale.Close()
	if _, err := stale.CopyRange(io.Discard, 0, stale.Size, true); faultCode(t, err) != fault.CodeContentChanged {
		t.Fatalf("缺少 mtime 证据时整文件复算未发现替换: %v", err)
	}
}
