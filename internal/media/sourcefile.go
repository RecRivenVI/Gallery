package media

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/RecRivenVI/gallery/internal/contract/fault"
	"github.com/RecRivenVI/gallery/internal/domain"
)

type HashResult struct {
	Blob         domain.ContentBlobRef
	Size         int64
	LocationKey  string
	RelativePath string
}

func HashSourceFile(root, relative string, afterRead func()) (HashResult, error) {
	file, resolved, normalized, err := OpenSourceFile(root, relative)
	if err != nil {
		return HashResult{}, err
	}
	defer file.Close()
	before, err := file.Stat()
	if err != nil {
		return HashResult{}, readFault(err)
	}
	hasher := sha256.New()
	written, err := io.Copy(hasher, file)
	if err != nil {
		return HashResult{}, readFault(err)
	}
	if afterRead != nil {
		afterRead()
	}
	afterHandle, handleErr := file.Stat()
	afterPath, pathErr := os.Stat(resolved)
	if handleErr != nil || pathErr != nil || written != before.Size() || before.Size() != afterHandle.Size() ||
		before.ModTime() != afterHandle.ModTime() || !os.SameFile(before, afterPath) || before.Size() != afterPath.Size() || before.ModTime() != afterPath.ModTime() {
		return HashResult{}, fault.New(fault.CodeContentChangedDuringHash, true, nil)
	}
	var sum [sha256.Size]byte
	copy(sum[:], hasher.Sum(nil))
	return HashResult{
		Blob: domain.NewSHA256BlobRef(sum), Size: before.Size(),
		LocationKey: locationKey(normalized), RelativePath: normalized,
	}, nil
}

func OpenSourceFile(root, relative string) (*os.File, string, string, error) {
	normalized, err := ValidateRelativePath(relative)
	if err != nil {
		return nil, "", "", err
	}
	realRoot, err := filepath.EvalSymlinks(filepath.Clean(root))
	if err != nil {
		return nil, "", "", readFault(err)
	}
	target := filepath.Join(realRoot, filepath.FromSlash(normalized))
	resolved, err := filepath.EvalSymlinks(target)
	if err != nil {
		return nil, "", "", readFault(err)
	}
	if !within(realRoot, resolved) {
		return nil, "", "", fault.New(fault.CodePathEscape, false, nil)
	}
	file, err := os.Open(resolved)
	if err != nil {
		return nil, "", "", readFault(err)
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, "", "", readFault(err)
	}
	if !info.Mode().IsRegular() {
		file.Close()
		return nil, "", "", fault.New(fault.CodeSourceReadFailed, false, nil)
	}
	return file, resolved, normalized, nil
}

func ValidateRelativePath(relative string) (string, error) {
	if relative == "" || strings.ContainsRune(relative, '\x00') || strings.Contains(relative, "\\") || path.IsAbs(relative) || filepath.VolumeName(relative) != "" {
		return "", fault.New(fault.CodePathEscape, false, nil)
	}
	clean := path.Clean(relative)
	if clean != relative || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fault.New(fault.CodePathEscape, false, nil)
	}
	for _, segment := range strings.Split(clean, "/") {
		if segment == "" || segment == "." || segment == ".." || strings.HasSuffix(segment, ".") || strings.HasSuffix(segment, " ") || reservedWindowsName(segment) {
			return "", fault.New(fault.CodePathEscape, false, nil)
		}
	}
	return clean, nil
}

func within(root, target string) bool {
	relative, err := filepath.Rel(root, target)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)
}

func readFault(err error) error {
	if errors.Is(err, fs.ErrNotExist) {
		return fault.New(fault.CodeContentDisappeared, true, nil)
	}
	if errors.Is(err, fs.ErrPermission) {
		return fault.New(fault.CodeSourceReadFailed, true, nil)
	}
	return fault.New(fault.CodeSourceUnavailable, true, err)
}

func locationKey(relative string) string {
	sum := sha256.Sum256([]byte("gallery-file-location\x00v1\x00" + relative))
	return hex.EncodeToString(sum[:])
}

func reservedWindowsName(segment string) bool {
	base := strings.ToUpper(strings.SplitN(segment, ".", 2)[0])
	switch base {
	case "CON", "PRN", "AUX", "NUL", "COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9", "LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9":
		return true
	default:
		return false
	}
}
