package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	version "github.com/RecRivenVI/gallery/pkg/galleryversion"
)

// cmd/galleryd 的可观察契约只有三样东西：**退出码**、**stdout/stderr 上的结构化日志**，
// 以及 **runtime descriptor 文件**。外部进程（桌面壳、testlab orchestrator、服务管理器、
// 未来的安装包）只能依赖这三样，因此本文件全部用真实子进程做黑盒断言，不去调用内部函数。
//
// 子进程的实现方式：测试二进制自身在看到 gallerydArgsEnv 时改扮 galleryd（替换 os.Args
// 后直接执行 run()）。这样断言的是 main.go 里真实的参数处理、退出码与信号路径，同时
// 不需要在测试中额外 go build 一份可执行文件——本机杀毒软件会误删刚链接好的二进制，
// 少一次链接就少一次不可复现的失败。
const gallerydArgsEnv = "GALLERY_TEST_GALLERYD_ARGS"

// startupBudget 是等待 runtime descriptor 出现的上限。启动包含两库迁移、恢复与全部服务
// 装配，在慢盘上可能需要数秒。
const startupBudget = 60 * time.Second

// shutdownBudget 与 bootstrap.Run 中 server.Shutdown 的 10 秒预算一致：收到 SIGINT 后
// galleryd 必须在这个预算内自行退出，而不是等待外部强杀。
const shutdownBudget = 10 * time.Second

func TestMain(m *testing.M) {
	if raw, ok := os.LookupEnv(gallerydArgsEnv); ok {
		var args []string
		if err := json.Unmarshal([]byte(raw), &args); err != nil {
			fmt.Fprintf(os.Stderr, "子进程参数解析失败: %v\n", err)
			os.Exit(97)
		}
		os.Args = append([]string{"galleryd"}, args...)
		os.Exit(run())
	}
	os.Exit(m.Run())
}

// runToCompletion 以给定参数运行一次 galleryd 并等待其退出，返回 stdout、stderr 与退出码。
func runToCompletion(t *testing.T, args ...string) (string, string, int) {
	t.Helper()
	encoded, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(os.Args[0])
	command.Env = append(os.Environ(), gallerydArgsEnv+"="+string(encoded))
	var stdout, stderr strings.Builder
	command.Stdout = &stdout
	command.Stderr = &stderr
	err = command.Run()
	if err == nil {
		return stdout.String(), stderr.String(), 0
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("启动 galleryd 子进程失败: %v", err)
	}
	return stdout.String(), stderr.String(), exitErr.ExitCode()
}

// gallerydProcess 是一个已启动、尚未退出的 galleryd 子进程。
type gallerydProcess struct {
	command    *exec.Cmd
	appRoot    string
	logPath    string
	exited     chan struct{}
	exitCode   int
	descriptor map[string]any
}

// startGalleryd 以 Personal 模式、loopback 自动端口启动 galleryd，并等待 runtime descriptor
// 出现后返回。
func startGalleryd(t *testing.T, appRoot string) *gallerydProcess {
	t.Helper()
	args := []string{"-mode=personal", "-listen=127.0.0.1:0", "-app-root=" + appRoot}
	encoded, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(os.Args[0])
	command.Env = append(os.Environ(), gallerydArgsEnv+"="+string(encoded))
	configureGracefulStopGroup(command)

	logPath := filepath.Join(t.TempDir(), "galleryd.log")
	logFile, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	command.Stdout = logFile
	command.Stderr = logFile
	if err := command.Start(); err != nil {
		logFile.Close()
		t.Fatal(err)
	}

	process := &gallerydProcess{command: command, appRoot: appRoot, logPath: logPath, exited: make(chan struct{})}
	go func() {
		waitErr := command.Wait()
		var exitErr *exec.ExitError
		switch {
		case waitErr == nil:
			process.exitCode = 0
		case errors.As(waitErr, &exitErr):
			process.exitCode = exitErr.ExitCode()
		default:
			process.exitCode = -1
		}
		logFile.Close()
		close(process.exited)
	}()
	t.Cleanup(func() {
		select {
		case <-process.exited:
			return
		default:
		}
		_ = command.Process.Kill()
		<-process.exited
	})
	return process
}

func (p *gallerydProcess) descriptorPath() string {
	return filepath.Join(p.appRoot, "run", "galleryd.json")
}

func (p *gallerydProcess) logs(t *testing.T) string {
	t.Helper()
	content, err := os.ReadFile(p.logPath)
	if err != nil {
		t.Fatalf("读取 galleryd 日志失败: %v", err)
	}
	return string(content)
}

// awaitDescriptor 轮询等待 runtime descriptor 出现并解析它；进程提前退出时立刻失败。
func (p *gallerydProcess) awaitDescriptor(t *testing.T) map[string]any {
	t.Helper()
	deadline := time.Now().Add(startupBudget)
	for time.Now().Before(deadline) {
		select {
		case <-p.exited:
			t.Fatalf("galleryd 在发布 descriptor 前退出（退出码 %d）：\n%s", p.exitCode, p.logs(t))
		default:
		}
		content, err := os.ReadFile(p.descriptorPath())
		if err == nil {
			var parsed map[string]any
			if json.Unmarshal(content, &parsed) == nil {
				if address, _ := parsed["address"].(string); address != "" {
					p.descriptor = parsed
					return parsed
				}
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("等待 runtime descriptor 超时（%s）：\n%s", startupBudget, p.logs(t))
	return nil
}

// TestConfigurationFailureExitsNonZero 断言配置失败以非零退出码结束，并在结构化日志里
// 留下可解释的原因。
//
// 这是服务管理器、桌面壳与 CI 唯一能自动判定"这次启动没成功"的信号。一旦某条配置错误
// 路径返回 0，外部就会认为服务已正常启动并停止重试或告警，而实际上什么都没在跑。
func TestConfigurationFailureExitsNonZero(t *testing.T) {
	for name, args := range map[string][]string{
		"未知 flag":          {"-definitely-not-a-flag"},
		"未知部署模式":           {"-mode=bogus"},
		"Personal 模式非环回监听": {"-mode=personal", "-listen=203.0.113.5:8080"},
		"LAN 模式公网监听":       {"-mode=lan", "-listen=203.0.113.5:8080"},
		"监听地址缺少端口":         {"-listen=127.0.0.1"},
		"多余位置参数":           {"unexpected-positional"},
		"文件根声明缺少等号":        {"-file-root=broken"},
		"文件根声明缺少路径":        {"-file-root=id="},
	} {
		t.Run(name, func(t *testing.T) {
			appRoot := t.TempDir()
			_, stderr, code := runToCompletion(t, append(args, "-app-root="+appRoot)...)
			if code == 0 {
				t.Fatalf("配置失败却以退出码 0 结束；stderr:\n%s", stderr)
			}
			if code != 2 {
				t.Fatalf("配置失败的退出码是 %d，期望 2（与运行时失败的 1 区分开）；stderr:\n%s", code, stderr)
			}
			if !strings.Contains(stderr, `"msg":"configuration_failed"`) {
				t.Fatalf("stderr 缺少结构化的 configuration_failed 记录：\n%s", stderr)
			}
			if _, err := os.Stat(filepath.Join(appRoot, "run", "galleryd.json")); !os.IsNotExist(err) {
				t.Fatal("配置失败的启动发布了 runtime descriptor")
			}
		})
	}
}

// TestVersionSubcommandExitsZero 断言 version 子命令以 0 退出并打印服务名与版本。
// 它必须在触碰 AppDirs 之前完成：安装器与诊断脚本会在服务未配置、目录不可写的机器上
// 调用它来确认可执行文件版本。
func TestVersionSubcommandExitsZero(t *testing.T) {
	stdout, stderr, code := runToCompletion(t, "version")
	if code != 0 {
		t.Fatalf("version 退出码为 %d；stderr:\n%s", code, stderr)
	}
	if !strings.Contains(stdout, version.ServiceName) || !strings.Contains(stdout, version.Version) {
		t.Fatalf("version 输出为 %q，未同时包含服务名与版本", stdout)
	}
	if stderr != "" {
		t.Fatalf("version 不应向 stderr 输出内容：%q", stderr)
	}
}

// TestHelpExitsZero 断言 -h 属于成功路径。flag 包把它作为 ErrHelp 返回，main 必须把它
// 与真正的配置错误区分开，否则 `galleryd -h` 会在任何调用它的脚本里表现为失败。
func TestHelpExitsZero(t *testing.T) {
	_, _, code := runToCompletion(t, "-h")
	if code != 0 {
		t.Fatalf("-h 的退出码为 %d，期望 0", code)
	}
}

// TestRuntimeDescriptorImpliesServiceIsServing 断言 descriptor 的存在等价于"已经在服务"。
//
// bootstrap.Run 有意把 descriptor 的发布放在监听器已经 Serve、且数据库、迁移、恢复、
// reconciliation 与全部服务装配都完成之后。这条不变量是外部发现机制的全部依据：
// testlab orchestrator、桌面壳与未来的安装包都是"看到 descriptor 就开始发请求"。若它被
// 提前发布，第一批请求会打在一个还没 Serve 的监听器上——TCP 连接会被内核 backlog 接受，
// 于是表现为请求挂起而不是干净的连接拒绝，极难诊断。
//
// 黑盒断言的形式：descriptor 一出现就立刻发一次完整的 API 往返，并要求它在很短的预算内
// 返回 200。这检验的是不变量的可观察后果，不是源码顺序本身。
func TestRuntimeDescriptorImpliesServiceIsServing(t *testing.T) {
	process := startGalleryd(t, t.TempDir())
	parsed := process.awaitDescriptor(t)

	address, _ := parsed["address"].(string)
	if !strings.HasPrefix(address, "127.0.0.1:") {
		t.Fatalf("Personal 模式 descriptor 的地址不是环回：%q", address)
	}
	if pid, _ := parsed["pid"].(float64); int(pid) != process.command.Process.Pid {
		t.Fatalf("descriptor 中的 PID %v 与子进程 %d 不符", parsed["pid"], process.command.Process.Pid)
	}
	if nonce, _ := parsed["startupNonce"].(string); nonce == "" {
		t.Fatal("descriptor 缺少 startupNonce：所有权判定会失效")
	}

	client := &http.Client{Timeout: 5 * time.Second}
	response, err := client.Get("http://" + address + "/api/v1/health")
	if err != nil {
		t.Fatalf("descriptor 已发布但服务尚未应答: %v\n%s", err, process.logs(t))
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("descriptor 已发布但 /api/v1/health 返回 %d：%s", response.StatusCode, string(body))
	}
	var health struct {
		Status    string `json:"status"`
		Databases struct {
			Control string `json:"control"`
			Catalog string `json:"catalog"`
		} `json:"databases"`
	}
	if err := json.Unmarshal(body, &health); err != nil {
		t.Fatalf("health 响应不是合法 JSON：%s", string(body))
	}
	if health.Status != "ok" || health.Databases.Control != "ok" || health.Databases.Catalog != "ok" {
		t.Fatalf("descriptor 已发布但两库尚未就绪：%s", string(body))
	}
	if !strings.Contains(process.logs(t), `"msg":"galleryd_started"`) {
		t.Fatalf("缺少 galleryd_started 记录：\n%s", process.logs(t))
	}
}

// TestGracefulShutdownWithinBudget 断言中断信号在 10 秒预算内触发优雅关闭：进程自行退出、
// 退出码为 0、descriptor 被清理、并留下 galleryd_stopped 记录。
//
// 预算不是随手取的：bootstrap.Run 给 server.Shutdown 的上下文正是 10 秒。超出预算意味着
// 关闭路径被某个不响应取消的操作挂住，外部只能强杀——而强杀会留下需要 recovery 收敛的
// running Job 租约和未清理的 descriptor，把一次正常的停止变成一次需要恢复的崩溃。
func TestGracefulShutdownWithinBudget(t *testing.T) {
	process := startGalleryd(t, t.TempDir())
	process.awaitDescriptor(t)

	if err := requestGracefulStop(process.command); err != nil {
		t.Skipf("当前环境无法向子进程投递中断信号，跳过优雅关闭断言: %v", err)
	}
	start := time.Now()
	select {
	case <-process.exited:
	case <-time.After(shutdownBudget):
		t.Fatalf("收到中断信号后 %s 内未退出：\n%s", shutdownBudget, process.logs(t))
	}
	elapsed := time.Since(start)

	if process.exitCode != 0 {
		t.Fatalf("优雅关闭的退出码为 %d，期望 0：\n%s", process.exitCode, process.logs(t))
	}
	logs := process.logs(t)
	if !strings.Contains(logs, `"msg":"galleryd_stopped"`) {
		t.Fatalf("缺少 galleryd_stopped 记录（%s 内退出）：\n%s", elapsed, logs)
	}
	if strings.Contains(logs, `"msg":"galleryd_failed"`) {
		t.Fatalf("优雅关闭被记录为失败：\n%s", logs)
	}
	if _, err := os.Stat(process.descriptorPath()); !os.IsNotExist(err) {
		t.Fatalf("优雅关闭后 runtime descriptor 未被清理（%v）：外部会继续把已停止的服务当成在跑", err)
	}
}

// TestSecondInstanceOnSameAppDirsExitsNonZero 断言同一份 AppDirs 上的第二个实例以非零
// 退出码失败，且**不破坏第一个实例**。
//
// 这条断言同时覆盖两件事：
//
//   - 运行时失败（而非配置失败）必须以退出码 1 结束，与配置失败的 2 区分开，使外部能分辨
//     "参数写错了，重试没用"与"环境冲突，可以重试"；
//   - 第二个实例在取得 AppDirs 独占锁之前不打开数据库、不建立监听，因此不得覆盖或删除
//     第一个实例的 runtime descriptor——descriptor.RemoveIfOwned 的 startupNonce 判定就是
//     为此存在。若这里失败，两个实例会互相抢夺同一份 control.db。
func TestSecondInstanceOnSameAppDirsExitsNonZero(t *testing.T) {
	appRoot := t.TempDir()
	first := startGalleryd(t, appRoot)
	original := first.awaitDescriptor(t)

	_, stderr, code := runToCompletion(t, "-mode=personal", "-listen=127.0.0.1:0", "-app-root="+appRoot)
	if code == 0 {
		t.Fatalf("第二个实例以退出码 0 结束；stderr:\n%s", stderr)
	}
	if code != 1 {
		t.Fatalf("运行时失败的退出码是 %d，期望 1（与配置失败的 2 区分开）；stderr:\n%s", code, stderr)
	}
	if !strings.Contains(stderr, `"msg":"galleryd_failed"`) {
		t.Fatalf("stderr 缺少结构化的 galleryd_failed 记录：\n%s", stderr)
	}

	content, err := os.ReadFile(first.descriptorPath())
	if err != nil {
		t.Fatalf("第一个实例的 descriptor 在第二个实例失败后不可读: %v", err)
	}
	var current map[string]any
	if err := json.Unmarshal(content, &current); err != nil {
		t.Fatal(err)
	}
	if current["startupNonce"] != original["startupNonce"] || current["address"] != original["address"] {
		t.Fatalf("第二个实例改写了第一个实例的 descriptor：%v -> %v", original, current)
	}
	select {
	case <-first.exited:
		t.Fatalf("第一个实例被第二个实例的失败启动带崩：\n%s", first.logs(t))
	default:
	}
}
