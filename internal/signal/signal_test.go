package signal

import (
	"context"
	"os"
	"runtime"
	"syscall"
	"testing"
	"time"
)

func TestContextWithShutdown(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("signal delivery not supported on Windows")
	}

	ctx := ContextWithShutdown(context.Background())

	go func() {
		time.Sleep(50 * time.Millisecond)
		p, _ := os.FindProcess(os.Getpid())
		p.Signal(syscall.SIGTERM)
	}()

	select {
	case <-ctx.Done():
		// success: context was cancelled
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for shutdown signal")
	}
}
