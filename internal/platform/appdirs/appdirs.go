package appdirs

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/RecRivenVI/gallery/internal/contract/fault"
	"github.com/RecRivenVI/gallery/internal/ports"
)

type Dirs struct {
	Config  string
	Data    string
	State   string
	Cache   string
	Logs    string
	Temp    string
	Runtime string
}

func UnderRoot(root string) Dirs {
	return Dirs{
		Config: filepath.Join(root, "config"), Data: filepath.Join(root, "data"),
		State: filepath.Join(root, "state"), Cache: filepath.Join(root, "cache"),
		Logs: filepath.Join(root, "logs"), Temp: filepath.Join(root, "tmp"),
		Runtime: filepath.Join(root, "run"),
	}
}

func Defaults() (Dirs, error) {
	switch runtime.GOOS {
	case "windows":
		roaming := os.Getenv("APPDATA")
		local := os.Getenv("LOCALAPPDATA")
		if roaming == "" || local == "" {
			return Dirs{}, fmt.Errorf("APPDATA/LOCALAPPDATA 不可用")
		}
		dirs := UnderRoot(filepath.Join(local, "Gallery"))
		dirs.Config = filepath.Join(roaming, "Gallery")
		return dirs, nil
	case "darwin":
		home, err := os.UserHomeDir()
		if err != nil {
			return Dirs{}, err
		}
		root := filepath.Join(home, "Library", "Application Support", "Gallery")
		dirs := UnderRoot(root)
		dirs.Cache = filepath.Join(home, "Library", "Caches", "Gallery")
		dirs.Logs = filepath.Join(home, "Library", "Logs", "Gallery")
		return dirs, nil
	default:
		home, err := os.UserHomeDir()
		if err != nil {
			return Dirs{}, err
		}
		config := envOr("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
		data := envOr("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
		state := envOr("XDG_STATE_HOME", filepath.Join(home, ".local", "state"))
		cache := envOr("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
		return Dirs{
			Config: filepath.Join(config, "gallery"), Data: filepath.Join(data, "gallery"),
			State: filepath.Join(state, "gallery"), Cache: filepath.Join(cache, "gallery"),
			Logs: filepath.Join(state, "gallery", "logs"), Temp: filepath.Join(cache, "gallery", "tmp"),
			Runtime: filepath.Join(state, "gallery", "run"),
		}, nil
	}
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func (d Dirs) WriteRoots() []string {
	return []string{d.Config, d.Data, d.State, d.Cache, d.Logs, d.Temp, d.Runtime}
}

// ValidateDisjoint 必须在创建目录和初始化数据库之前调用。
func (d Dirs) ValidateDisjoint(fileSystem ports.FileSystem, sourceRoots []string) error {
	writes, err := canonicalizeAll(fileSystem, d.WriteRoots())
	if err != nil {
		return fault.New(fault.CodeConfigInvalid, false, err)
	}
	sources, err := canonicalizeAll(fileSystem, sourceRoots)
	if err != nil {
		return fault.New(fault.CodeConfigInvalid, false, err)
	}

	for _, source := range sources {
		for _, write := range writes {
			if overlaps(source, write) {
				return fault.New(fault.CodeAppDirsOverlap, false, nil)
			}
		}
	}
	for i := range sources {
		for j := i + 1; j < len(sources); j++ {
			if overlaps(sources[i], sources[j]) {
				return fault.New(fault.CodeSourceRootsOverlap, false, nil)
			}
		}
	}
	return nil
}

func (d Dirs) Ensure(fileSystem ports.FileSystem) error {
	for _, root := range d.WriteRoots() {
		if root == "" {
			return fault.New(fault.CodeConfigInvalid, false, fmt.Errorf("AppDirs 路径为空"))
		}
		if err := fileSystem.MkdirAll(root, 0o700); err != nil {
			return fault.New(fault.CodeConfigInvalid, false, err)
		}
	}
	return nil
}

func canonicalizeAll(fileSystem ports.FileSystem, paths []string) ([]string, error) {
	result := make([]string, 0, len(paths))
	for _, path := range paths {
		if path == "" {
			return nil, fmt.Errorf("路径为空")
		}
		canonical, err := canonicalize(fileSystem, path)
		if err != nil {
			return nil, err
		}
		result = append(result, canonical)
	}
	return result, nil
}

func canonicalize(fileSystem ports.FileSystem, path string) (string, error) {
	abs, err := fileSystem.Abs(path)
	if err != nil {
		return "", err
	}
	abs = filepath.Clean(abs)
	current := abs
	var suffix []string
	for {
		if _, statErr := fileSystem.Stat(current); statErr == nil {
			real, evalErr := fileSystem.EvalSymlinks(current)
			if evalErr != nil {
				return "", evalErr
			}
			for i := len(suffix) - 1; i >= 0; i-- {
				real = filepath.Join(real, suffix[i])
			}
			return compareForm(filepath.Clean(real)), nil
		} else if !errors.Is(statErr, fs.ErrNotExist) {
			return "", statErr
		}
		parent := filepath.Dir(current)
		if parent == current {
			return compareForm(abs), nil
		}
		suffix = append(suffix, filepath.Base(current))
		current = parent
	}
}

func compareForm(path string) string {
	if runtime.GOOS == "windows" {
		return strings.ToLower(path)
	}
	return path
}

func overlaps(left, right string) bool {
	return contains(left, right) || contains(right, left)
}

func contains(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel))
}
