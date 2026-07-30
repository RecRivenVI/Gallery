package config

import (
	"encoding/hex"
	"flag"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/RecRivenVI/gallery/internal/contract/fault"
	"github.com/RecRivenVI/gallery/internal/platform/appdirs"
)

type Mode string

const (
	ModePersonal Mode = "personal"
	ModeLAN      Mode = "lan"
)

type Config struct {
	Mode        Mode
	Listen      string
	AppDirs     appdirs.Dirs
	SourceRoots []string
	// FileRoots 是只读的文件根声明，形如 `id=path`。文件根与 Source 是不同概念：它不产生
	// Catalog 事实、不绑定规则、不被扫描，且**可以**是 Source 的祖先——真实配置正是这个形状。
	FileRoots     []FileRootDeclaration
	ExternalTools []ExternalToolDeclaration
}

// FileRootDeclaration 是一条命令行文件根声明。
type FileRootDeclaration struct {
	ID   string
	Path string
}

// ExternalToolDeclaration 把一个受支持的工具 ID 绑定到显式绝对路径、工具自行报告的精确
// version token 与可执行文件 SHA-256。三者缺一不可；Gallery 不从 PATH 静默发现工具。
type ExternalToolDeclaration struct {
	ID      string
	Path    string
	Version string
	SHA256  string
}

type stringList []string

func (s *stringList) String() string { return strings.Join(*s, ",") }
func (s *stringList) Set(value string) error {
	*s = append(*s, value)
	return nil
}

func Parse(args []string) (Config, error) {
	defaults, err := appdirs.Defaults()
	if err != nil {
		return Config{}, fault.New(fault.CodeConfigInvalid, false, err)
	}
	flags := flag.NewFlagSet("galleryd", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	mode := flags.String("mode", string(ModePersonal), "部署模式：personal 或 lan")
	listen := flags.String("listen", "127.0.0.1:0", "HTTP 监听地址")
	appRoot := flags.String("app-root", "", "开发/测试用 AppDirs 统一父目录")
	var sourceRoots stringList
	flags.Var(&sourceRoots, "source-root", "只读 Source 根；可重复指定，仅用于启动重叠守卫")
	var fileRoots stringList
	flags.Var(&fileRoots, "file-root", "只读文件根，形如 id=path；可重复指定")
	var externalToolPaths stringList
	flags.Var(&externalToolPaths, "external-tool-path", "外部工具绝对路径，形如 ffprobe=C:\\path\\ffprobe.exe；可重复指定")
	var externalToolVersions stringList
	flags.Var(&externalToolVersions, "external-tool-version", "外部工具精确 version token，形如 ffprobe=7.1.1；可重复指定")
	var externalToolSHA256 stringList
	flags.Var(&externalToolSHA256, "external-tool-sha256", "外部工具可执行文件 SHA-256，形如 ffprobe=<64 hex>；可重复指定")
	if err := flags.Parse(args); err != nil {
		return Config{}, err
	}
	if flags.NArg() != 0 {
		return Config{}, fault.New(fault.CodeConfigInvalid, false, fmt.Errorf("存在未知位置参数"))
	}
	if *appRoot != "" {
		defaults = appdirs.UnderRoot(*appRoot)
	}
	declarations := make([]FileRootDeclaration, 0, len(fileRoots))
	for _, item := range fileRoots {
		id, path, ok := strings.Cut(item, "=")
		if !ok || id == "" || path == "" {
			return Config{}, fault.WithField(fault.CodeConfigInvalid, "file-root",
				fmt.Errorf("文件根声明必须形如 id=path"))
		}
		declarations = append(declarations, FileRootDeclaration{ID: id, Path: path})
	}
	externalTools, err := parseExternalToolDeclarations(externalToolPaths, externalToolVersions, externalToolSHA256)
	if err != nil {
		return Config{}, err
	}
	cfg := Config{Mode: Mode(*mode), Listen: *listen, AppDirs: defaults, SourceRoots: sourceRoots,
		FileRoots: declarations, ExternalTools: externalTools}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) Validate() error {
	if c.Mode != ModePersonal && c.Mode != ModeLAN {
		return fault.New(fault.CodeConfigInvalid, false, fmt.Errorf("未知部署模式"))
	}
	host, _, err := net.SplitHostPort(c.Listen)
	if err != nil {
		return fault.New(fault.CodeConfigInvalid, false, fmt.Errorf("listen 地址无效: %w", err))
	}
	if c.Mode == ModePersonal && !isLoopbackHost(host) {
		return fault.New(fault.CodeConfigInvalid, false, fmt.Errorf("Personal 模式只允许 loopback"))
	}
	if c.Mode == ModeLAN && !isTrustedLANHost(host) {
		return fault.New(fault.CodeConfigInvalid, false, fmt.Errorf("LAN 模式只允许 loopback 或私有地址"))
	}
	seenTools := make(map[string]struct{}, len(c.ExternalTools))
	for _, declaration := range c.ExternalTools {
		if err := validateExternalToolDeclaration(declaration); err != nil {
			return err
		}
		if _, exists := seenTools[declaration.ID]; exists {
			return fault.WithField(fault.CodeConfigInvalid, "external-tool-path",
				fmt.Errorf("外部工具 %q 重复声明", declaration.ID))
		}
		seenTools[declaration.ID] = struct{}{}
	}
	return nil
}

func parseExternalToolDeclarations(paths, versions, digests []string) ([]ExternalToolDeclaration, error) {
	pathMap, err := parseExternalToolValues("external-tool-path", paths)
	if err != nil {
		return nil, err
	}
	versionMap, err := parseExternalToolValues("external-tool-version", versions)
	if err != nil {
		return nil, err
	}
	digestMap, err := parseExternalToolValues("external-tool-sha256", digests)
	if err != nil {
		return nil, err
	}
	ids := make(map[string]struct{}, len(pathMap)+len(versionMap)+len(digestMap))
	for id := range pathMap {
		ids[id] = struct{}{}
	}
	for id := range versionMap {
		ids[id] = struct{}{}
	}
	for id := range digestMap {
		ids[id] = struct{}{}
	}
	ordered := make([]string, 0, len(ids))
	for id := range ids {
		ordered = append(ordered, id)
	}
	sort.Strings(ordered)
	declarations := make([]ExternalToolDeclaration, 0, len(ordered))
	for _, id := range ordered {
		path, hasPath := pathMap[id]
		version, hasVersion := versionMap[id]
		digest, hasDigest := digestMap[id]
		if !hasPath || !hasVersion || !hasDigest {
			return nil, fault.WithField(fault.CodeConfigInvalid, "external-tool-path",
				fmt.Errorf("外部工具 %q 必须同时声明 path、version 与 sha256", id))
		}
		declarations = append(declarations, ExternalToolDeclaration{ID: id, Path: path, Version: version, SHA256: strings.ToLower(digest)})
	}
	return declarations, nil
}

func parseExternalToolValues(field string, values []string) (map[string]string, error) {
	parsed := make(map[string]string, len(values))
	for _, item := range values {
		id, value, ok := strings.Cut(item, "=")
		if !ok || id == "" || value == "" {
			return nil, fault.WithField(fault.CodeConfigInvalid, field,
				fmt.Errorf("外部工具声明必须形如 id=value"))
		}
		if _, exists := parsed[id]; exists {
			return nil, fault.WithField(fault.CodeConfigInvalid, field,
				fmt.Errorf("外部工具 %q 在同一字段重复声明", id))
		}
		parsed[id] = value
	}
	return parsed, nil
}

func validateExternalToolDeclaration(declaration ExternalToolDeclaration) error {
	if declaration.ID != "ffprobe" && declaration.ID != "ffmpeg" {
		return fault.WithField(fault.CodeConfigInvalid, "external-tool-path",
			fmt.Errorf("不支持的外部工具 ID %q", declaration.ID))
	}
	if !filepath.IsAbs(declaration.Path) {
		return fault.WithField(fault.CodeConfigInvalid, "external-tool-path",
			fmt.Errorf("外部工具 %q 必须使用绝对路径", declaration.ID))
	}
	if !validExternalToolVersion(declaration.Version) {
		return fault.WithField(fault.CodeConfigInvalid, "external-tool-version",
			fmt.Errorf("外部工具 %q 的 version token 无效", declaration.ID))
	}
	digest, err := hex.DecodeString(declaration.SHA256)
	if err != nil || len(digest) != sha256Size {
		return fault.WithField(fault.CodeConfigInvalid, "external-tool-sha256",
			fmt.Errorf("外部工具 %q 的 SHA-256 必须是 64 位十六进制", declaration.ID))
	}
	return nil
}

const sha256Size = 32

func validExternalToolVersion(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for index, r := range value {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' ||
			(index > 0 && (r == '.' || r == '-' || r == '_' || r == '+' || r == '~')) {
			continue
		}
		return false
	}
	return true
}

func IsLoopbackListen(listen string) bool {
	host, _, err := net.SplitHostPort(listen)
	return err == nil && isLoopbackHost(host)
}

func isTrustedLANHost(host string) bool {
	if isLoopbackHost(host) {
		return true
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsPrivate() && !ip.IsUnspecified()
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}
