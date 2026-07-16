package media

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"

	"github.com/RecRivenVI/gallery/internal/contract/fault"
)

type Snapshot struct {
	File *os.File
	Size int64
	path string
}

func (s *Snapshot) Close() error {
	if s == nil {
		return nil
	}
	var closeErr error
	if s.File != nil {
		closeErr = s.File.Close()
	}
	removeErr := os.Remove(s.path)
	if os.IsNotExist(removeErr) {
		removeErr = nil
	}
	if closeErr != nil {
		return closeErr
	}
	return removeErr
}

func PrepareSnapshot(root, relative, expectedAlgorithm, expectedDigest string, expectedSize int64, tempRoot string) (*Snapshot, error) {
	if expectedAlgorithm != "sha256-v1" || len(expectedDigest) != sha256.Size*2 {
		return nil, fault.New(fault.CodeContentChangedDuringHash, false, nil)
	}
	source, resolved, _, err := OpenSourceFile(root, relative)
	if err != nil {
		return nil, err
	}
	defer source.Close()
	before, err := source.Stat()
	if err != nil {
		return nil, readFault(err)
	}
	if before.Size() != expectedSize {
		return nil, fault.New(fault.CodeContentChangedDuringHash, true, nil)
	}
	if err := os.MkdirAll(tempRoot, 0o700); err != nil {
		return nil, fault.New(fault.CodeInternal, true, err)
	}
	temporary, err := os.CreateTemp(tempRoot, "media-snapshot-*")
	if err != nil {
		return nil, fault.New(fault.CodeInternal, true, err)
	}
	cleanup := func() { temporary.Close(); _ = os.Remove(temporary.Name()) }
	if err := temporary.Chmod(0o600); err != nil {
		cleanup()
		return nil, fault.New(fault.CodeInternal, true, err)
	}
	hasher := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(temporary, hasher), source)
	if copyErr != nil {
		cleanup()
		return nil, readFault(copyErr)
	}
	afterHandle, handleErr := source.Stat()
	afterPath, pathErr := os.Stat(resolved)
	if handleErr != nil || pathErr != nil || written != expectedSize || before.Size() != afterHandle.Size() ||
		before.ModTime() != afterHandle.ModTime() || !os.SameFile(before, afterPath) || before.Size() != afterPath.Size() || before.ModTime() != afterPath.ModTime() ||
		hex.EncodeToString(hasher.Sum(nil)) != expectedDigest {
		cleanup()
		return nil, fault.New(fault.CodeContentChangedDuringHash, true, nil)
	}
	if err := temporary.Sync(); err != nil {
		cleanup()
		return nil, fault.New(fault.CodeInternal, true, err)
	}
	if _, err := temporary.Seek(0, io.SeekStart); err != nil {
		cleanup()
		return nil, fault.New(fault.CodeInternal, true, err)
	}
	return &Snapshot{File: temporary, Size: written, path: filepath.Clean(temporary.Name())}, nil
}
