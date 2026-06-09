package auth

import (
	"context"
	"runtime/debug"
	"sync/atomic"
	"time"

	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
)

const heavyRequestFreeOSMemoryMinInterval = 2 * time.Second

var (
	heavyRequestRetryCleanup = defaultHeavyRequestRetryCleanup
	heavyRequestFreeOSMemory = debug.FreeOSMemory
	lastHeavyRequestFreeOS   atomic.Int64
)

func streamAttemptContext(parent context.Context, opts cliproxyexecutor.Options) (context.Context, context.CancelFunc) {
	if !cliproxyexecutor.IsHeavyRequest(opts) {
		return parent, nil
	}
	if parent == nil {
		parent = context.Background()
	}
	return context.WithCancel(parent)
}

func cleanupFailedHeavyRequestAttempt(cancel context.CancelFunc, opts cliproxyexecutor.Options) {
	if cancel != nil {
		cancel()
	}
	if cliproxyexecutor.IsHeavyRequest(opts) && heavyRequestRetryCleanup != nil {
		heavyRequestRetryCleanup()
	}
}

func streamResultWithCancel(ctx context.Context, result *cliproxyexecutor.StreamResult, cancel context.CancelFunc) *cliproxyexecutor.StreamResult {
	if result == nil || cancel == nil {
		return result
	}
	out := make(chan cliproxyexecutor.StreamChunk)
	go func() {
		defer cancel()
		defer close(out)
		for chunk := range result.Chunks {
			if ctx == nil {
				out <- chunk
				continue
			}
			select {
			case <-ctx.Done():
				return
			case out <- chunk:
			}
		}
	}()
	return &cliproxyexecutor.StreamResult{
		Headers: result.Headers,
		Chunks:  out,
	}
}

func defaultHeavyRequestRetryCleanup() {
	now := time.Now().UnixNano()
	last := lastHeavyRequestFreeOS.Load()
	if last != 0 && time.Duration(now-last) < heavyRequestFreeOSMemoryMinInterval {
		return
	}
	if lastHeavyRequestFreeOS.CompareAndSwap(last, now) {
		heavyRequestFreeOSMemory()
	}
}
