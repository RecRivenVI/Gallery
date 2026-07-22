// Package process 管理一次真实、独立编译的 galleryd 子进程的完整生命周期：编译、
// 以 Personal 模式启动并等待 runtime descriptor、请求正常停止并在超时后回退强杀。
// stage3/stage4/未来阶段的 orchestrator 共用同一套生命周期管理，不各自重新实现。
package process

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// GracefulStopTimeout 是等待 galleryd 响应正常停止信号的上限；超时后回退到强制
// 终止，并把这次回退记录在返回结果里，不得把回退悄悄当成正常路径。
const GracefulStopTimeout = 15 * time.Second

// descriptor 镜像 internal/platform/descriptor.Descriptor 的 JSON 形状；本工具不
// 导入 internal/* 包，因此在这里独立声明公开可见的字段子集。
type descriptor struct {
	Address string `json:"address"`
	PID     int    `json:"pid"`
}

// Process 是一次真实、独立编译的 galleryd 子进程句柄。
type Process struct {
	cmd        *exec.Cmd
	BaseURL    string
	descriptor descriptor
	AppRoot    string
	logFile    *os.File
	exited     chan struct{}
	waitErr    error
}

// BuildGalleryd 用当前固定 Go 工具链编译一份独立的 galleryd 可执行文件，供本轮全部
// 场景复用，避免每次启动都重新编译。
func BuildGalleryd(goBin, repoRoot, outPath string) error {
	cmd := exec.Command(goBin, "build", "-o", outPath, "./cmd/galleryd")
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(), "GOTOOLCHAIN=local", "CGO_ENABLED=0")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("build galleryd: %w: %s", err, string(output))
	}
	return nil
}

// StartGalleryd 以 Personal 模式、loopback 自动端口启动真实 galleryd 进程，指向给定
// AppDirs 根，并等待 runtime descriptor 出现后才返回——descriptor 存在即等价于
// "数据库、迁移、恢复、reconciliation 与全部服务装配完成、监听已开始服务"。logPath
// 由调用者指定，必须位于授权测试根的 logs/ 目录内；本函数只负责创建并在返回的
// Process 生命周期内持有该文件句柄，Stop() 会正确关闭它。
func StartGalleryd(binPath, appRoot, logPath string, timeout time.Duration) (*Process, error) {
	cmd := exec.Command(binPath, "-mode=personal", "-listen=127.0.0.1:0", "-app-root="+appRoot)
	configureProcessGroup(cmd)
	logFile, err := os.Create(logPath)
	if err != nil {
		return nil, err
	}
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		logFile.Close()
		return nil, err
	}

	proc := &Process{cmd: cmd, AppRoot: appRoot, logFile: logFile, exited: make(chan struct{})}
	go func() {
		proc.waitErr = cmd.Wait()
		close(proc.exited)
	}()

	descriptorPath := filepath.Join(appRoot, "run", "galleryd.json")
	deadline := time.Now().Add(timeout)
	var desc descriptor
	for time.Now().Before(deadline) {
		select {
		case <-proc.exited:
			logFile.Close()
			return nil, fmt.Errorf("galleryd 在建立 descriptor 前提前退出: %v", proc.waitErr)
		default:
		}
		content, readErr := os.ReadFile(descriptorPath)
		if readErr == nil {
			if err := json.Unmarshal(content, &desc); err == nil && desc.Address != "" {
				proc.BaseURL = "http://" + desc.Address
				proc.descriptor = desc
				return proc, nil
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	proc.forceKill()
	<-proc.exited
	logFile.Close()
	return nil, fmt.Errorf("等待 galleryd runtime descriptor 超时（%s）", timeout)
}

// StopOutcome 描述一次 Stop() 调用实际采用的路径，供调用方在最终报告中如实记录
// "本轮 galleryd 是否正常停止"，不得把强制终止的回退路径悄悄当成正常关闭。
type StopOutcome struct {
	RequestedGraceful bool
	ExitedGracefully  bool
	ForcedKill        bool
	Err               error
}

// Stop 结束本轮场景的 galleryd 子进程：优先请求正常停止（向进程组投递
// CTRL_BREAK_EVENT/SIGTERM，与 bootstrap.Run 的 signal.NotifyContext(os.Interrupt,
// syscall.SIGTERM) 关闭路径一致），等待其在 GracefulStopTimeout 内自行退出；
// 只有请求失败或超时才回退到强制终止，并在返回值中如实标记这次回退。
func (p *Process) Stop() StopOutcome {
	defer func() {
		if p.logFile != nil {
			p.logFile.Close()
		}
	}()
	if p == nil || p.cmd == nil || p.cmd.Process == nil {
		return StopOutcome{}
	}

	select {
	case <-p.exited:
		return StopOutcome{ExitedGracefully: true}
	default:
	}

	outcome := StopOutcome{RequestedGraceful: true}
	if err := requestGracefulStop(p.cmd); err != nil {
		outcome.Err = err
		outcome.ForcedKill = true
		p.forceKill()
		<-p.exited
		return outcome
	}

	select {
	case <-p.exited:
		outcome.ExitedGracefully = true
		return outcome
	case <-time.After(GracefulStopTimeout):
		outcome.ForcedKill = true
		p.forceKill()
		<-p.exited
		return outcome
	}
}

func (p *Process) forceKill() {
	if p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
	}
}
