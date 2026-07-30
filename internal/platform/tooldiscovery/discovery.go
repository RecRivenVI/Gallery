// Package tooldiscovery implements the production external-tool adapter.
//
// Discovery is deliberately explicit: it never searches PATH. A configured executable must match both an exact
// version token reported by `<tool> -version` and the configured SHA-256 before it becomes available.
package tooldiscovery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/RecRivenVI/gallery/internal/contract/fault"
	"github.com/RecRivenVI/gallery/internal/ports"
)

const (
	probeTimeout     = 5 * time.Second
	probeOutputLimit = 64 << 10
)

// Declaration is the already syntax-validated bootstrap declaration for one supported executable.
type Declaration struct {
	ID      string
	Path    string
	Version string
	SHA256  string
}

// Capability is a path-free report suitable for logs and diagnostics.
type Capability struct {
	ID      string
	Version string
	SHA256  string
}

type entry struct {
	path       string
	version    string
	sha256     string
	digest     [sha256.Size]byte
	capability Capability
}

// Discovery implements toolrunner.Resolver. It is immutable after construction and safe for concurrent use.
type Discovery struct {
	entries map[string]entry
}

// New verifies every explicit declaration before returning a resolver. Empty declarations preserve the production
// fail-closed default by returning nil. A partially valid declaration set is rejected atomically.
func New(ctx context.Context, declarations []Declaration, controller ports.ProcessController) (*Discovery, error) {
	if len(declarations) == 0 {
		return nil, nil
	}
	if controller == nil {
		return nil, fmt.Errorf("ToolDiscovery 缺少 ProcessController")
	}
	entries := make(map[string]entry, len(declarations))
	for _, declaration := range declarations {
		if declaration.ID != "ffprobe" && declaration.ID != "ffmpeg" {
			return nil, unavailable(declaration.ID, "工具 ID 不受支持", nil)
		}
		if _, exists := entries[declaration.ID]; exists {
			return nil, unavailable(declaration.ID, "工具重复声明", nil)
		}
		if !filepath.IsAbs(declaration.Path) {
			return nil, unavailable(declaration.ID, "工具路径不是绝对路径", nil)
		}
		expected, err := decodeDigest(declaration.SHA256)
		if err != nil {
			return nil, unavailable(declaration.ID, "工具摘要声明无效", err)
		}
		path := filepath.Clean(declaration.Path)
		actual, err := executableDigest(path)
		if err != nil {
			return nil, unavailable(declaration.ID, "工具不可读", err)
		}
		if actual != expected {
			return nil, unavailable(declaration.ID, "工具摘要不在允许列表", nil)
		}
		version, err := probeVersion(ctx, controller, declaration.ID, path)
		if err != nil {
			return nil, unavailable(declaration.ID, "版本探测失败", err)
		}
		if version != declaration.Version {
			return nil, unavailable(declaration.ID, "工具版本不在允许列表", nil)
		}
		digest := hex.EncodeToString(actual[:])
		entries[declaration.ID] = entry{
			path: path, version: version, sha256: digest, digest: actual,
			capability: Capability{ID: declaration.ID, Version: version, SHA256: digest},
		}
	}
	return &Discovery{entries: entries}, nil
}

// Available reports whether a specific tool ID passed startup verification.
func (d *Discovery) Available(toolID string) bool {
	if d == nil {
		return false
	}
	_, ok := d.entries[toolID]
	return ok
}

// Capabilities returns a stable, path-free snapshot ordered by tool ID.
func (d *Discovery) Capabilities() []Capability {
	if d == nil {
		return nil
	}
	capabilities := make([]Capability, 0, len(d.entries))
	for _, item := range d.entries {
		capabilities = append(capabilities, item.capability)
	}
	sort.Slice(capabilities, func(i, j int) bool { return capabilities[i].ID < capabilities[j].ID })
	return capabilities
}

// Resolve rechecks the executable digest immediately before each execution. This catches ordinary replacement or
// partial upgrades without silently running a binary that differs from the startup allowlist. The same-OS-user
// replacement race after this check remains outside Gallery's stated threat model.
func (d *Discovery) Resolve(ctx context.Context, toolID string, args []string, workingDir string) (ports.Command, error) {
	if err := ctx.Err(); err != nil {
		return ports.Command{}, err
	}
	item, ok := d.entries[toolID]
	if !ok {
		return ports.Command{}, unavailable(toolID, "工具未配置", nil)
	}
	actual, err := executableDigest(item.path)
	if err != nil {
		return ports.Command{}, unavailable(toolID, "工具不可读", err)
	}
	if actual != item.digest {
		return ports.Command{}, unavailable(toolID, "工具摘要已变化", nil)
	}
	return ports.Command{Path: item.path, Args: append([]string(nil), args...), Dir: workingDir}, nil
}

func decodeDigest(value string) ([sha256.Size]byte, error) {
	var result [sha256.Size]byte
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != sha256.Size {
		return result, fmt.Errorf("SHA-256 必须是 64 位十六进制")
	}
	copy(result[:], decoded)
	return result, nil
}

func executableDigest(path string) ([sha256.Size]byte, error) {
	var result [sha256.Size]byte
	file, err := os.Open(path)
	if err != nil {
		return result, sanitizePathError(err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return result, sanitizePathError(err)
	}
	if !info.Mode().IsRegular() {
		return result, fmt.Errorf("不是普通文件")
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return result, sanitizePathError(err)
	}
	copy(result[:], hash.Sum(nil))
	return result, nil
}

func probeVersion(parent context.Context, controller ports.ProcessController, toolID, path string) (string, error) {
	ctx, cancel := context.WithTimeout(parent, probeTimeout)
	defer cancel()
	stdout := newProbeBuffer(cancel)
	stderr := newProbeBuffer(cancel)
	process, err := controller.Start(ctx, ports.Command{Path: path, Args: []string{"-version"}, Stdout: stdout, Stderr: stderr})
	if err != nil {
		return "", sanitizePathError(err)
	}
	waitErr := process.Wait()
	if stdout.overflowed() || stderr.overflowed() {
		return "", fmt.Errorf("版本输出超过 %d bytes", probeOutputLimit)
	}
	if waitErr != nil {
		return "", sanitizePathError(waitErr)
	}
	output := stdout.String()
	if strings.TrimSpace(output) == "" {
		output = stderr.String()
	}
	line, _, _ := strings.Cut(strings.ReplaceAll(output, "\r\n", "\n"), "\n")
	prefix := toolID + " version "
	if !strings.HasPrefix(line, prefix) {
		return "", fmt.Errorf("版本首行不符合 %q 契约", prefix)
	}
	fields := strings.Fields(strings.TrimPrefix(line, prefix))
	if len(fields) == 0 {
		return "", fmt.Errorf("版本首行缺少 version token")
	}
	return fields[0], nil
}

type probeBuffer struct {
	mu       sync.Mutex
	content  []byte
	overflow bool
	cancel   context.CancelFunc
}

func newProbeBuffer(cancel context.CancelFunc) *probeBuffer {
	return &probeBuffer{content: make([]byte, 0, 1024), cancel: cancel}
}

func (b *probeBuffer) Write(value []byte) (int, error) {
	b.mu.Lock()
	remaining := probeOutputLimit - len(b.content)
	if len(value) <= remaining {
		b.content = append(b.content, value...)
		b.mu.Unlock()
		return len(value), nil
	}
	if remaining > 0 {
		b.content = append(b.content, value[:remaining]...)
	}
	first := !b.overflow
	b.overflow = true
	b.mu.Unlock()
	if first {
		b.cancel()
	}
	return remaining, io.ErrShortWrite
}

func (b *probeBuffer) overflowed() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.overflow
}

func (b *probeBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.content)
}

func sanitizePathError(err error) error {
	var pathErr *os.PathError
	if errors.As(err, &pathErr) {
		return pathErr.Err
	}
	return err
}

func unavailable(toolID, reason string, cause error) error {
	message := fmt.Sprintf("工具 %q：%s", toolID, reason)
	if cause != nil {
		message += ": " + cause.Error()
	}
	return fault.New(fault.CodeExternalToolUnavailable, false, errors.New(message))
}
