package ports

import (
	"context"
	"io"
	"io/fs"
	"time"

	"github.com/RecRivenVI/gallery/internal/domain"
)

type Clock interface {
	Now() time.Time
}

type IDGenerator interface {
	New(kind domain.IDKind) (domain.ID, error)
}

// FileSystem 是 AppDirs 与路径守卫实际需要的最小可写文件系统端口。
type FileSystem interface {
	Abs(path string) (string, error)
	EvalSymlinks(path string) (string, error)
	MkdirAll(path string, perm fs.FileMode) error
	Stat(path string) (fs.FileInfo, error)
}

type Command struct {
	Path   string
	Args   []string
	Dir    string
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

type Process interface {
	Wait() error
	Kill() error
}

// ProcessController 只接受参数数组，不提供 shell 字符串入口。
type ProcessController interface {
	Start(ctx context.Context, command Command) (Process, error)
}
