// Package process 管理一次真实、独立编译的 galleryd 子进程的完整生命周期：编译、
// 以 Personal 模式启动并等待 runtime descriptor、请求正常停止并在超时后回退强杀。
// stage3/stage4/未来阶段的 orchestrator 共用同一套生命周期管理，不各自重新实现。
package process

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// GracefulStopTimeout 是等待 galleryd 响应正常停止信号的上限；超时后回退到强制
// 终止，并把这次回退记录在返回结果里，不得把回退悄悄当成正常路径。
const (
	GracefulStopTimeout = 15 * time.Second
	ForceKillTimeout    = 5 * time.Second
)

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
	return BuildGallerydContext(context.Background(), goBin, repoRoot, outPath)
}

// BuildGallerydContext 与 BuildGalleryd 相同，但允许验证运行器在超时或收到终止信号时
// 取消编译，避免 CI 或本地中断后遗留无界等待的 go 子进程。
func BuildGallerydContext(ctx context.Context, goBin, repoRoot, outPath string) error {
	cmd := exec.Command(goBin, "build", "-o", outPath, "./cmd/galleryd")
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(), "GOTOOLCHAIN=local", "CGO_ENABLED=0")
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	err := RunCommandContext(ctx, cmd)
	if err != nil {
		return fmt.Errorf("build galleryd: %w: %s", err, output.String())
	}
	return nil
}

// StartGalleryd 以 Personal 模式、loopback 自动端口启动真实 galleryd 进程，指向给定
// AppDirs 根，并等待 runtime descriptor 出现后才返回——descriptor 存在即等价于
// "数据库、迁移、恢复、reconciliation 与全部服务装配完成、监听已开始服务"。logPath
// 由调用者指定，必须位于授权测试根的 logs/ 目录内；本函数只负责创建并在返回的
// Process 生命周期内持有该文件句柄，Stop() 会正确关闭它。
func StartGalleryd(binPath, appRoot, logPath string, timeout time.Duration) (*Process, error) {
	return StartGallerydWithSourceRootsContext(context.Background(), binPath, appRoot, logPath, timeout)
}

// StartGallerydWithSourceRoots 与 StartGalleryd 相同，并把调用方拥有的临时合成 Source
// 加入启动重叠守卫。具名参数刻意只接受 Source 根，不能覆盖这里固定的 Personal、loopback
// 自动端口与临时 AppDirs。
func StartGallerydWithSourceRoots(binPath, appRoot, logPath string, timeout time.Duration, sourceRoots ...string) (*Process, error) {
	return StartGallerydWithSourceRootsContext(
		context.Background(), binPath, appRoot, logPath, timeout, sourceRoots...,
	)
}

// StartGallerydWithSourceRootsContext 与 StartGallerydWithSourceRoots 相同，但启动等待受 ctx
// 约束；取消或超时后会强制终止尚未完成启动的 galleryd，并以独立二级上限等待退出。
func StartGallerydWithSourceRootsContext(
	ctx context.Context,
	binPath, appRoot, logPath string,
	timeout time.Duration,
	sourceRoots ...string,
) (*Process, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("启动 galleryd 已取消: %w", err)
	}
	args := []string{"-mode=personal", "-listen=127.0.0.1:0", "-app-root=" + appRoot}
	for _, root := range sourceRoots {
		if root == "" {
			return nil, fmt.Errorf("source root 不能为空")
		}
		args = append(args, "-source-root="+root)
	}
	cmd := exec.Command(binPath, args...)
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
	startupCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	var desc descriptor
	for {
		select {
		case <-proc.exited:
			logFile.Close()
			return nil, fmt.Errorf("galleryd 在建立 descriptor 前提前退出: %v", proc.waitErr)
		case <-ticker.C:
			content, readErr := os.ReadFile(descriptorPath)
			if readErr == nil {
				if err := json.Unmarshal(content, &desc); err == nil && desc.Address != "" {
					proc.BaseURL = "http://" + desc.Address
					proc.descriptor = desc
					return proc, nil
				}
			}
		case <-startupCtx.Done():
			killErr := proc.forceKill()
			if !proc.waitForExit(ForceKillTimeout) {
				killErr = errors.Join(killErr, fmt.Errorf("强制终止 galleryd 后等待退出超时（%s）", ForceKillTimeout))
			}
			logFile.Close()
			if ctx.Err() != nil {
				return nil, errors.Join(fmt.Errorf("启动 galleryd 已取消: %w", ctx.Err()), killErr)
			}
			return nil, errors.Join(fmt.Errorf("等待 galleryd runtime descriptor 超时（%s）", timeout), killErr)
		}
	}
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
	if p == nil {
		return StopOutcome{}
	}
	defer func() {
		if p.logFile != nil {
			p.logFile.Close()
		}
	}()
	if p.cmd == nil || p.cmd.Process == nil {
		return StopOutcome{}
	}

	select {
	case <-p.exited:
		return p.finishOutcome(StopOutcome{})
	default:
	}

	outcome := StopOutcome{RequestedGraceful: true}
	if err := requestGracefulStop(p.cmd); err != nil {
		outcome.Err = err
		outcome.ForcedKill = true
		outcome.Err = errors.Join(outcome.Err, p.forceKill())
		if !p.waitForExit(ForceKillTimeout) {
			outcome.Err = errors.Join(outcome.Err, fmt.Errorf("强杀后等待退出超时（%s）", ForceKillTimeout))
			return outcome
		}
		return p.finishOutcome(outcome)
	}

	select {
	case <-p.exited:
		return p.finishOutcome(outcome)
	case <-time.After(GracefulStopTimeout):
		outcome.ForcedKill = true
		outcome.Err = errors.Join(outcome.Err, p.forceKill())
		if !p.waitForExit(ForceKillTimeout) {
			outcome.Err = errors.Join(outcome.Err, fmt.Errorf("强杀后等待退出超时（%s）", ForceKillTimeout))
			return outcome
		}
		return p.finishOutcome(outcome)
	}
}

func (p *Process) finishOutcome(outcome StopOutcome) StopOutcome {
	if p.waitErr != nil {
		outcome.Err = errors.Join(outcome.Err, p.waitErr)
	}
	outcome.ExitedGracefully = !outcome.ForcedKill && p.waitErr == nil
	return outcome
}

func (p *Process) waitForExit(timeout time.Duration) bool {
	select {
	case <-p.exited:
		return true
	case <-time.After(timeout):
		return false
	}
}

func (p *Process) forceKill() error {
	if p.cmd == nil || p.cmd.Process == nil {
		return nil
	}
	select {
	case <-p.exited:
		return nil
	default:
	}
	err := p.cmd.Process.Kill()
	if errors.Is(err, os.ErrProcessDone) {
		return nil
	}
	return err
}
