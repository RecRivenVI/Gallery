package media

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"hash"
	"io"
	"os"

	"github.com/RecRivenVI/gallery/internal/contract/fault"
)

// ContentHandle 是已确认媒体正文的只读流式句柄。
//
// 它与 PrepareSnapshot 的关键区别是**不复制文件、不预先计算完整 SHA-256**：打开时只做
// 安全路径解析、size 比对和一次文件身份复核，随后直接从 Source 句柄流式返回请求的字节
// 区间。这把每次正文请求的代价从「整文件读 + 整文件写 + 整文件哈希」降到「只读所需区间」，
// 单个请求的常驻资源也与文件大小无关，同时保留产品不变量——读到的字节要么与已发布
// ContentBlob 一致，要么调用方得到稳定错误：
//
//   - 打开时把当前文件与 publication 冻结的 size 与 mtime 证据逐项比对，任何不符都在
//     发送任何字节之前返回 CONTENT_CHANGED；
//   - 打开时与每次区间读取结束前，复核解析后路径与句柄仍指向同一文件（os.SameFile）
//     且 size 与 mtime 在读取期间未变；
//   - 整文件读取（非 Range）顺带复算 sha256-v1 并与已发布 digest 比对，不增加任何 I/O，
//     因此能发现「同大小、同 mtime、中间字节被替换」这类身份证据无法覆盖的内容变化。
//
// 身份证据在这里只用于**判定既有 ContentBlob 是否仍然成立**，不用于建立新的 ContentBlob；
// 建立新 Blob 仍然必须走 HashSourceFileWithOptions 的首次完整 SHA-256。
type ContentHandle struct {
	file      *os.File
	resolved  string
	before    os.FileInfo
	algorithm string
	digest    string

	// Size 是已发布 ContentBlob 记录的大小，也是打开时实测的文件大小。
	Size int64
}

// PublishedIdentity 是 publication 冻结的媒体身份证据。MTimeNanos 为 0 表示该 revision
// 发布时没有留下 mtime 证据（catalog v13 之前的历史快照），此时只比对 size 与整文件
// digest，不伪造证据，也不因此放行任何未经校验的字节。
type PublishedIdentity struct {
	Algorithm  string
	Digest     string
	Size       int64
	MTimeNanos int64
}

// OpenContent 打开一个已确认媒体的正文句柄。它不读取任何正文字节，因此 HEAD 请求与
// Range 请求都不必为整文件付出代价。
func OpenContent(root, relative string, published PublishedIdentity) (*ContentHandle, error) {
	if published.Algorithm != "sha256-v1" || len(published.Digest) != sha256.Size*2 {
		return nil, fault.New(fault.CodeContentChanged, false, nil)
	}
	file, resolved, _, err := OpenSourceFile(root, relative)
	if err != nil {
		return nil, err
	}
	before, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, readFault(err)
	}
	if before.Size() != published.Size {
		file.Close()
		return nil, fault.New(fault.CodeContentChanged, true, nil)
	}
	if published.MTimeNanos != 0 && before.ModTime().UnixNano() != published.MTimeNanos {
		file.Close()
		return nil, fault.New(fault.CodeContentChanged, true, nil)
	}
	handle := &ContentHandle{
		file: file, resolved: resolved, before: before,
		algorithm: published.Algorithm, digest: published.Digest, Size: before.Size(),
	}
	if err := handle.VerifyIdentity(); err != nil {
		file.Close()
		return nil, err
	}
	return handle, nil
}

func (h *ContentHandle) Close() error {
	if h == nil || h.file == nil {
		return nil
	}
	return h.file.Close()
}

// VerifyIdentity 复核打开时记录的身份证据仍然成立：句柄与解析后路径仍指向同一个文件，
// 且 size 与 mtime 均未变化。任一不成立都返回 CONTENT_CHANGED，绝不静默返回与已发布
// ContentBlob 不一致的字节。
func (h *ContentHandle) VerifyIdentity() error {
	afterHandle, handleErr := h.file.Stat()
	afterPath, pathErr := os.Stat(h.resolved)
	if handleErr != nil || pathErr != nil {
		return fault.New(fault.CodeContentChanged, true, nil)
	}
	if afterHandle.Size() != h.before.Size() || afterHandle.ModTime() != h.before.ModTime() {
		return fault.New(fault.CodeContentChanged, true, nil)
	}
	if !os.SameFile(h.before, afterPath) || afterPath.Size() != h.before.Size() || afterPath.ModTime() != h.before.ModTime() {
		return fault.New(fault.CodeContentChanged, true, nil)
	}
	return nil
}

// CopyRange 把 [start, start+length) 区间写入 dst，并返回实际写出的字节数。
//
// wholeFile 为真（即请求覆盖整个文件）时顺带复算 sha256-v1 并与已发布 digest 比对；
// 这不产生额外读 I/O，因为这些字节本来就要读，而且能发现「同大小、同 mtime、中间字节被
// 替换」这类身份证据无法覆盖的内容变化。Range 请求无法复算完整 digest，退回到身份证据
// 复核。无论哪条路径，返回错误时调用方都必须视为该响应不可信。
//
// 返回的字节数即使在出错时也必须被调用方检查：已经写出的字节无法收回，调用方需要中断
// 响应（而不是追加错误正文），让客户端无法把截断结果误判为成功。
func (h *ContentHandle) CopyRange(dst io.Writer, start, length int64, wholeFile bool) (int64, error) {
	if start < 0 || length < 0 || start+length > h.Size {
		return 0, fault.New(fault.CodeRangeInvalid, false, nil)
	}
	var hasher hash.Hash
	sink := dst
	if wholeFile {
		hasher = sha256.New()
		sink = io.MultiWriter(dst, hasher)
	}
	// SectionReader 使用定位读，不改变句柄自身偏移，因此同一 ContentHandle 上的区间读取
	// 彼此独立。io.CopyN 在源提前结束时返回 io.EOF：这说明文件在读取期间被截断，按内容
	// 变化处理，不允许把短读当作正常结束。
	written, err := io.CopyN(sink, io.NewSectionReader(h.file, start, length), length)
	if err != nil {
		if errors.Is(err, io.EOF) {
			return written, fault.New(fault.CodeContentChanged, true, nil)
		}
		return written, err
	}
	if hasher != nil && hex.EncodeToString(hasher.Sum(nil)) != h.digest {
		return written, fault.New(fault.CodeContentChanged, true, nil)
	}
	if err := h.VerifyIdentity(); err != nil {
		return written, err
	}
	return written, nil
}
