// Package ports 固定 gallery 核心与操作系统适配器之间的可替换边界。
package ports

import (
	"context"
	"io"
	"io/fs"
	"time"
)

type FileIdentity struct {
	Provider, Volume, StableID string
	Reliable                   bool
}
type FileIdentityProvider interface {
	Identify(context.Context, string) (FileIdentity, error)
}
type FileEvent struct {
	Kind, Path string
	At         time.Time
}
type FileWatcher interface {
	Watch(context.Context, []string) (<-chan FileEvent, error)
	Close() error
}
type PathCanonicalizer interface {
	Canonicalize(string) (string, error)
	Equal(string, string) bool
}
type FileReader interface {
	Open(string) (io.ReadSeekCloser, error)
	Stat(string) (fs.FileInfo, error)
}
type ProcessSpec struct {
	Executable string
	Args       []string
	WorkDir    string
	Env        []string
}
type ProcessHandle interface {
	PID() int
	Wait(context.Context) error
	Terminate(context.Context) error
	Kill() error
}
type ProcessController interface {
	Start(context.Context, ProcessSpec) (ProcessHandle, error)
}
type ToolDiscovery interface {
	Find(context.Context, string) (string, error)
	Version(context.Context, string) (string, error)
}
type AppDirs struct{ Config, Data, Cache, Logs, Runtime string }
type AppDirsProvider interface{ Resolve(string) (AppDirs, error) }
type OpenExternal interface {
	URL(context.Context, string) error
	Reveal(context.Context, string) error
}
type Autostart interface {
	Available(context.Context) bool
	Enable(context.Context, string) error
	Disable(context.Context, string) error
}
type PowerEvents interface {
	Events(context.Context) (<-chan string, error)
}
type KeyStore interface {
	Get(context.Context, string) ([]byte, error)
	Put(context.Context, string, []byte) error
	Delete(context.Context, string) error
}
