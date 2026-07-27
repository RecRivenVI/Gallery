// Command web-e2e 在完全隔离的真实 galleryd 上执行浏览器业务 E2E。
//
// 它只复制仓库合成 fixture 到系统临时目录，不接触真实 Source；运行前后比较文件清单、
// 大小、mtime 与 SHA-256，证明 Gallery 没有写入 Source。galleryd 使用动态 loopback 端口和
// 临时 AppDirs，结束时复用 testlab 的跨平台优雅停止路径。
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"syscall"
	"time"

	testprocess "github.com/RecRivenVI/gallery/tools/testlab/internal/process"
)

type fileFact struct {
	Size    int64
	Mode    fs.FileMode
	ModTime int64
	SHA256  string
}

func main() {
	os.Exit(run())
}

func run() (exitCode int) {
	var repoRoot string
	var keep bool
	flag.StringVar(&repoRoot, "repo-root", ".", "Gallery 仓库根目录")
	flag.BoolVar(&keep, "keep", false, "保留临时验证目录")
	flag.Parse()
	runCtx, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()

	root, err := filepath.Abs(repoRoot)
	if err != nil {
		return fail("解析仓库根目录", err)
	}
	goBin, err := fixedGo()
	if err != nil {
		return fail("解析固定 Go 工具链", err)
	}
	if err := verifyGo(goBin); err != nil {
		return fail("验证固定 Go 工具链", err)
	}
	nodeBin, err := exec.LookPath("node")
	if err != nil {
		return fail("查找 Node.js", err)
	}
	npmBin, err := exec.LookPath("npm")
	if err != nil {
		return fail("查找 npm", err)
	}

	testRoot, err := os.MkdirTemp("", "gallery-web-real-")
	if err != nil {
		return fail("创建临时验证目录", err)
	}
	defer func() {
		if keep {
			fmt.Printf("已按 -keep 保留隔离验证目录：%s\n", testRoot)
			return
		}
		if err := os.RemoveAll(testRoot); err != nil {
			fmt.Fprintf(os.Stderr, "清理隔离验证目录失败: %v\n", err)
			exitCode = 1
		}
	}()

	appRoot := filepath.Join(testRoot, "app")
	sourceRoot := filepath.Join(testRoot, "source")
	logsRoot := filepath.Join(testRoot, "logs")
	for _, dir := range []string{appRoot, sourceRoot, logsRoot} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fail("创建隔离目录", err)
		}
	}
	fixture := filepath.Join(root, "tests", "fixtures", "walking-skeleton")
	if err := os.CopyFS(sourceRoot, os.DirFS(fixture)); err != nil {
		return fail("复制合成 Source", err)
	}
	if err := os.Remove(filepath.Join(sourceRoot, "work-one", "media.bin")); err != nil {
		return fail("移除占位合成媒体", err)
	}
	if err := writeSyntheticPNG(filepath.Join(sourceRoot, "work-one", "01-rule.png"), 2, 2, 8); err != nil {
		return fail("写入规则封面合成媒体", err)
	}
	if err := writeSyntheticPNG(filepath.Join(sourceRoot, "work-one", "02-custom.png"), 3, 2, 97); err != nil {
		return fail("写入自定义封面合成媒体", err)
	}
	metadata := []byte("{\"creator\":{\"name\":\"Synthetic Creator\"}}\n")
	if err := os.WriteFile(filepath.Join(sourceRoot, "work-one", "metadata.json"), metadata, 0o600); err != nil {
		return fail("写入合成 metadata", err)
	}
	if err := normalizeSyntheticTimes(sourceRoot); err != nil {
		return fail("稳定合成 Source 时间戳", err)
	}
	before, err := snapshot(sourceRoot)
	if err != nil {
		return fail("记录 Source 只读基线", err)
	}

	webRoot := filepath.Join(root, "web")
	diagnosticsRoot := filepath.Join(webRoot, "test-results", "real-backend")
	if err := os.RemoveAll(diagnosticsRoot); err != nil {
		return fail("清理旧真实后端诊断", err)
	}
	if err := command(runCtx, 5*time.Minute, webRoot, nil, npmBin, "run", "build"); err != nil {
		return fail("构建 Web 生产资产", err)
	}
	galleryd := filepath.Join(testRoot, "galleryd")
	if runtime.GOOS == "windows" {
		galleryd += ".exe"
	}
	buildCtx, cancelBuild := context.WithTimeout(runCtx, 2*time.Minute)
	err = testprocess.BuildGallerydContext(buildCtx, goBin, root, galleryd)
	cancelBuild()
	if err != nil {
		return fail("构建隔离 galleryd", err)
	}

	gallerydLog := filepath.Join(logsRoot, "galleryd.log")
	server, err := testprocess.StartGallerydWithSourceRootsContext(
		runCtx,
		galleryd,
		appRoot,
		gallerydLog,
		60*time.Second,
		sourceRoot,
	)
	if err != nil {
		return fail("启动隔离 galleryd", errors.Join(err, retainDiagnostics(gallerydLog, diagnosticsRoot)))
	}
	serverStopped := false
	defer func() {
		if serverStopped {
			return
		}
		if err := stopError(server.Stop()); err != nil {
			fmt.Fprintf(os.Stderr, "回收隔离 galleryd 失败: %v\n", err)
			exitCode = 1
		}
	}()

	rulePackage := filepath.Join(testRoot, "web-e2e-rule-package.json")
	if err := prepareRulePackage(
		filepath.Join(root, "internal", "rules", "testdata", "minimal-rule-package.json"),
		rulePackage,
	); err != nil {
		return fail("准备 Web E2E 规则包", err)
	}
	env := []string{
		"GALLERY_REAL_BASE_URL=" + server.BaseURL,
		"GALLERY_REAL_SOURCE_ROOT=" + sourceRoot,
		"GALLERY_REAL_RULE_PACKAGE=" + rulePackage,
	}
	playwright := filepath.Join(webRoot, "node_modules", "@playwright", "test", "cli.js")
	testErr := waitHealthy(runCtx, server.BaseURL, 30*time.Second)
	if testErr == nil {
		testErr = command(runCtx, 2*time.Minute, webRoot, env, nodeBin, playwright, "test",
			"e2e/real-bootstrap.spec.ts", "--project=chromium", "--workers=1", "--retries=0")
	}
	// publication E2E 以 bootstrap 留下的首次 index/J1 未确认媒体为前置；规则生命周期则要在
	// media/custom-cover/gallery 完成后再用新 Personal Session 继续同一隔离实例。分开调用把这些
	// 状态依赖和每段独立超时预算写进运行器契约，不依赖 Playwright 对多 spec 的偶然排序。
	if testErr == nil {
		testErr = command(runCtx, 2*time.Minute, webRoot, env, nodeBin, playwright, "test",
			"e2e/real-media.spec.ts",
			"--project=chromium", "--workers=1", "--retries=0")
	}
	if testErr == nil {
		testErr = command(runCtx, 2*time.Minute, webRoot, env, nodeBin, playwright, "test",
			"e2e/real-custom-cover.spec.ts",
			"--project=chromium", "--workers=1", "--retries=0")
	}
	if testErr == nil {
		testErr = command(runCtx, 2*time.Minute, webRoot, env, nodeBin, playwright, "test",
			"e2e/real-gallery.spec.ts",
			"--project=chromium", "--workers=1", "--retries=0")
	}
	if testErr == nil {
		testErr = command(runCtx, 2*time.Minute, webRoot, env, nodeBin, playwright, "test",
			"e2e/real-rule-lifecycle.spec.ts",
			"--project=chromium", "--workers=1", "--retries=0")
	}
	stop := server.Stop()
	serverStopped = true
	after, guardErr := snapshot(sourceRoot)
	if guardErr == nil && !reflect.DeepEqual(before, after) {
		guardErr = describeGuardDifference(before, after)
	}
	testErr = errors.Join(testErr, stopError(stop))
	if testErr != nil || guardErr != nil {
		diagnosticErr := retainDiagnostics(gallerydLog, diagnosticsRoot)
		if diagnosticErr == nil {
			fmt.Printf("失败诊断已保存到：%s\n", diagnosticsRoot)
		}
		return fail("真实浏览器 E2E", errors.Join(testErr, guardErr, diagnosticErr))
	}

	fmt.Println("真实 galleryd 浏览器 E2E 通过；合成 Source 只读 guard 通过；galleryd 已优雅停止")
	return 0
}

func writeSyntheticPNG(path string, width, height int, seed uint8) error {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	imageData := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := range height {
		for x := range width {
			imageData.Set(x, y, color.NRGBA{
				R: uint8((int(seed) + x*61 + y*29) % 256),
				G: uint8((int(seed) + x*17 + y*83) % 256),
				B: uint8((int(seed) + x*97 + y*11) % 256),
				A: 255,
			})
		}
	}
	encodeErr := png.Encode(file, imageData)
	closeErr := file.Close()
	return errors.Join(encodeErr, closeErr)
}

func prepareRulePackage(sourcePath, targetPath string) error {
	content, err := os.ReadFile(sourcePath)
	if err != nil {
		return err
	}
	const oldMedia = `"glob": "*.bin", "kind": "image", "mime": "application/octet-stream"`
	const newMedia = `"glob": "*.png", "kind": "image", "mime": "image/png"`
	if strings.Count(string(content), oldMedia) != 1 {
		return fmt.Errorf("最小规则包媒体原语形状不符合预期")
	}
	adapted := strings.Replace(string(content), oldMedia, newMedia, 1)
	return os.WriteFile(targetPath, []byte(adapted), 0o600)
}

func retainDiagnostics(logPath, diagnosticsRoot string) error {
	content, err := os.ReadFile(logPath)
	if err != nil {
		return fmt.Errorf("读取 galleryd 诊断日志: %w", err)
	}
	if err := os.MkdirAll(diagnosticsRoot, 0o700); err != nil {
		return fmt.Errorf("创建诊断目录: %w", err)
	}
	if err := os.WriteFile(filepath.Join(diagnosticsRoot, "galleryd.log"), content, 0o600); err != nil {
		return fmt.Errorf("保存 galleryd 诊断日志: %w", err)
	}
	return nil
}

func fixedGo() (string, error) {
	if configured := os.Getenv("GALLERY_GO"); configured != "" {
		if info, err := os.Stat(configured); err == nil && !info.IsDir() {
			return configured, nil
		}
		return "", fmt.Errorf("GALLERY_GO 指向的文件不存在")
	}
	if runtime.GOOS == "windows" {
		return "", fmt.Errorf("Windows 必须显式设置 GALLERY_GO")
	}
	return exec.LookPath("go")
}

func verifyGo(goBin string) error {
	cmd := exec.Command(goBin, "version")
	cmd.Env = append(os.Environ(), "GOTOOLCHAIN=local")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("go version: %w: %s", err, strings.TrimSpace(string(output)))
	}
	version := strings.TrimSpace(string(output))
	fmt.Println(version)
	fields := strings.Fields(version)
	if len(fields) < 3 || fields[0] != "go" || fields[1] != "version" || fields[2] != "go1.26.5" {
		return fmt.Errorf("需要 Go 1.26.5，实际为 %s", version)
	}
	return nil
}

func command(
	parent context.Context,
	timeout time.Duration,
	dir string,
	extraEnv []string,
	name string,
	args ...string,
) error {
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), extraEnv...)
	cmd.Env = append(cmd.Env, "GOTOOLCHAIN=local")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := testprocess.RunCommandContext(ctx, cmd); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("%s %s: %w", filepath.Base(name), strings.Join(args, " "), errors.Join(ctx.Err(), err))
		}
		return fmt.Errorf("%s %s: %w", filepath.Base(name), strings.Join(args, " "), err)
	}
	return nil
}

func waitHealthy(parent context.Context, baseURL string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	client := &http.Client{Timeout: 2 * time.Second}
	for {
		request, _ := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/api/v1/health", nil)
		response, err := client.Do(request)
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("等待健康检查失败: %w", ctx.Err())
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func stopError(outcome testprocess.StopOutcome) error {
	if outcome.ForcedKill || !outcome.ExitedGracefully || outcome.Err != nil {
		return fmt.Errorf("galleryd 未优雅停止: %+v", outcome)
	}
	return nil
}

func snapshot(root string) (map[string]fileFact, error) {
	result := make(map[string]fileFact)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		key := filepath.ToSlash(relative)
		if entry.IsDir() {
			result[key+"/"] = fileFact{Mode: info.Mode(), ModTime: info.ModTime().UnixNano()}
			return nil
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("合成 Source 包含非普通文件: %s", entry.Name())
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		digest := sha256.Sum256(content)
		result[key] = fileFact{
			Size: info.Size(), Mode: info.Mode(), ModTime: info.ModTime().UnixNano(), SHA256: hex.EncodeToString(digest[:]),
		}
		return nil
	})
	return result, err
}

// normalizeSyntheticTimes 在只读 guard 建立之前把运行器自己刚复制/写入的 fixture 时间戳
// 固定下来。Windows 可能延迟提交新建子项带来的父目录 mtime 更新；若立即取基线，会把这次
// 测试准备阶段的迟到更新误判成 galleryd 写 Source。这里在全部准备写入完成后由深到浅设置
// 固定时间，随后 guard 仍会检测 galleryd 造成的目录增删、mode/mtime 与文件内容变化。
func normalizeSyntheticTimes(root string) error {
	paths := make([]string, 0)
	if err := filepath.WalkDir(root, func(path string, _ fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		paths = append(paths, path)
		return nil
	}); err != nil {
		return err
	}
	sort.Slice(paths, func(i, j int) bool { return len(paths[i]) > len(paths[j]) })
	fixed := time.Date(2020, time.January, 2, 3, 4, 5, 0, time.UTC)
	for _, path := range paths {
		if err := os.Chtimes(path, fixed, fixed); err != nil {
			return fmt.Errorf("设置 %s 时间戳: %w", filepath.Base(path), err)
		}
	}
	return nil
}

func describeGuardDifference(before, after map[string]fileFact) error {
	changed := make([]string, 0)
	for path, expected := range before {
		if actual, ok := after[path]; !ok || actual != expected {
			changed = append(changed, path)
		}
	}
	for path := range after {
		if _, ok := before[path]; !ok {
			changed = append(changed, path)
		}
	}
	sort.Strings(changed)
	return fmt.Errorf("Source 只读 guard 失败，变化的相对项: %s", strings.Join(changed, ", "))
}

func fail(stage string, err error) int {
	fmt.Fprintf(os.Stderr, "%s失败: %v\n", stage, err)
	return 1
}
