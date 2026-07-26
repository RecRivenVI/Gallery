package config

import (
	"flag"
	"fmt"
	"net"
	"os"
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
	FileRoots []FileRootDeclaration
}

// FileRootDeclaration 是一条命令行文件根声明。
type FileRootDeclaration struct {
	ID   string
	Path string
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
	cfg := Config{Mode: Mode(*mode), Listen: *listen, AppDirs: defaults, SourceRoots: sourceRoots, FileRoots: declarations}
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
	return nil
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
