package httpapi

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/RecRivenVI/gallery/internal/contract/fault"
)

func gateServer(capacity int, wait time.Duration) *Server {
	return &Server{mediaGate: make(chan struct{}, capacity), mediaGateWait: wait}
}

func gateFaultCode(t *testing.T, err error) fault.Code {
	t.Helper()
	var structured *fault.Error
	if !errors.As(err, &structured) {
		t.Fatalf("非结构化错误: %v", err)
	}
	return structured.Code
}

// TestMediaReadGateBoundsConcurrentBodyReads 覆盖 MED-1 的资源上限部分：正文读取名额
// 有界，超额请求在有限等待后得到可重试的 MEDIA_READ_BUSY，而不是无限排队占用连接与
// Source 句柄；名额释放后立即可以继续服务。
func TestMediaReadGateBoundsConcurrentBodyReads(t *testing.T) {
	server := gateServer(2, 20*time.Millisecond)
	first, err := server.acquireMediaRead(context.Background())
	if err != nil {
		t.Fatalf("首个名额获取失败: %v", err)
	}
	second, err := server.acquireMediaRead(context.Background())
	if err != nil {
		t.Fatalf("第二个名额获取失败: %v", err)
	}
	start := time.Now()
	if _, err := server.acquireMediaRead(context.Background()); gateFaultCode(t, err) != fault.CodeMediaReadBusy {
		t.Fatalf("名额耗尽错误 = %v", err)
	}
	if elapsed := time.Since(start); elapsed < 20*time.Millisecond {
		t.Fatalf("超额请求没有经过有界等待就被拒绝: %v", elapsed)
	}
	first()
	third, err := server.acquireMediaRead(context.Background())
	if err != nil {
		t.Fatalf("名额释放后仍无法获取: %v", err)
	}
	third()
	second()
	// 全部释放后闸门必须回到空闲状态，不泄漏名额。
	if len(server.mediaGate) != 0 {
		t.Fatalf("名额泄漏: 仍占用 %d", len(server.mediaGate))
	}
}

// TestMediaReadGateHonorsRequestCancellation 证明客户端断开后等待名额的请求立即退出，
// 不继续占用等待队列。
func TestMediaReadGateHonorsRequestCancellation(t *testing.T) {
	server := gateServer(1, time.Minute)
	release, err := server.acquireMediaRead(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := server.acquireMediaRead(ctx); gateFaultCode(t, err) != fault.CodeProcessInterrupted {
		t.Fatalf("请求取消错误 = %v", err)
	}
}
