package ports

import (
	"errors"
	"os"
)

var (
	// ErrFileIdentityInvalidHandle 表示调用方没有提供仍然打开的文件句柄。
	ErrFileIdentityInvalidHandle = errors.New("文件身份句柄无效")
	// ErrFileIdentityNotRegular 表示句柄目标不是可建立内容候选身份的普通文件。
	ErrFileIdentityNotRegular = errors.New("文件身份仅支持普通文件")
	// ErrFileIdentityUnavailable 表示平台或底层文件系统不能提供要求强度的稳定候选身份。
	ErrFileIdentityUnavailable = errors.New("稳定文件身份不可用")
)

// FileIdentityCandidate 是从已打开文件句柄读取的平台候选身份。Encoded 是版本化、
// 不含路径的 opaque 值，只能用于同一受控扫描范围内比较候选位置；它不是内容哈希，
// 不表示文件内容代次未变，也不能代替 ContentBlob 的完整 SHA-256 身份。
type FileIdentityCandidate struct {
	Encoded string
}

// FileIdentityProvider 从调用方已经打开的只读文件句柄读取候选身份。实现不得重新按
// 路径打开文件，避免路径在检查期间被替换后混淆身份。该查询不取得锁，也不保证读取
// 期间排除并发写入；需要内容代次证明的调用方必须使用独立的强校验能力。
type FileIdentityProvider interface {
	Identify(file *os.File) (FileIdentityCandidate, error)
}
