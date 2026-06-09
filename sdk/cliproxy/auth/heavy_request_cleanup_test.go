package auth

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/registry"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
)

func TestManagerExecuteStream_CleansHeavyRequestBeforeCredentialRetry(t *testing.T) {
	m := NewManager(nil, nil, nil)
	m.SetRetryConfig(0, 0, 0)

	executor := &authFallbackExecutor{
		id: "codex",
		streamFirstErrors: map[string]error{
			"aa-fails": &Error{HTTPStatus: http.StatusBadGateway, Message: "upstream failed"},
		},
	}
	m.RegisterExecutor(executor)

	model := "gpt-5.4-heavy-cleanup"
	badAuth := &Auth{ID: "aa-fails", Provider: "codex"}
	goodAuth := &Auth{ID: "bb-succeeds", Provider: "codex"}

	reg := registry.GetGlobalRegistry()
	reg.RegisterClient(badAuth.ID, "codex", []*registry.ModelInfo{{ID: model}})
	reg.RegisterClient(goodAuth.ID, "codex", []*registry.ModelInfo{{ID: model}})
	t.Cleanup(func() {
		reg.UnregisterClient(badAuth.ID)
		reg.UnregisterClient(goodAuth.ID)
	})

	if _, errRegister := m.Register(context.Background(), badAuth); errRegister != nil {
		t.Fatalf("register bad auth: %v", errRegister)
	}
	if _, errRegister := m.Register(context.Background(), goodAuth); errRegister != nil {
		t.Fatalf("register good auth: %v", errRegister)
	}

	cleanupCalls := 0
	prevCleanup := heavyRequestRetryCleanup
	heavyRequestRetryCleanup = func() { cleanupCalls++ }
	t.Cleanup(func() { heavyRequestRetryCleanup = prevCleanup })

	result, errExecute := m.ExecuteStream(context.Background(), []string{"codex"}, cliproxyexecutor.Request{Model: model}, cliproxyexecutor.Options{
		Stream: true,
		Metadata: map[string]any{
			cliproxyexecutor.HeavyRequestMetadataKey: true,
		},
	})
	if errExecute != nil {
		t.Fatalf("execute stream error = %v, want success after retry", errExecute)
	}
	for chunk := range result.Chunks {
		if chunk.Err != nil {
			t.Fatalf("unexpected stream chunk error: %v", chunk.Err)
		}
	}

	if cleanupCalls != 1 {
		t.Fatalf("cleanup calls = %d, want 1", cleanupCalls)
	}
	if got := executor.StreamCalls(); len(got) != 2 || got[0] != badAuth.ID || got[1] != goodAuth.ID {
		t.Fatalf("stream calls = %v, want [%s %s]", got, badAuth.ID, goodAuth.ID)
	}
}

func TestManagerExecuteStream_DoesNotCleanNormalRequestBeforeCredentialRetry(t *testing.T) {
	m := NewManager(nil, nil, nil)
	m.SetRetryConfig(0, 0, 0)

	executor := &authFallbackExecutor{
		id: "codex",
		streamFirstErrors: map[string]error{
			"aa-fails-normal": &Error{HTTPStatus: http.StatusBadGateway, Message: "upstream failed"},
		},
	}
	m.RegisterExecutor(executor)

	model := "gpt-5.4-normal-cleanup"
	badAuth := &Auth{ID: "aa-fails-normal", Provider: "codex"}
	goodAuth := &Auth{ID: "bb-succeeds-normal", Provider: "codex"}

	reg := registry.GetGlobalRegistry()
	reg.RegisterClient(badAuth.ID, "codex", []*registry.ModelInfo{{ID: model}})
	reg.RegisterClient(goodAuth.ID, "codex", []*registry.ModelInfo{{ID: model}})
	t.Cleanup(func() {
		reg.UnregisterClient(badAuth.ID)
		reg.UnregisterClient(goodAuth.ID)
	})

	if _, errRegister := m.Register(context.Background(), badAuth); errRegister != nil {
		t.Fatalf("register bad auth: %v", errRegister)
	}
	if _, errRegister := m.Register(context.Background(), goodAuth); errRegister != nil {
		t.Fatalf("register good auth: %v", errRegister)
	}

	cleanupCalls := 0
	prevCleanup := heavyRequestRetryCleanup
	heavyRequestRetryCleanup = func() { cleanupCalls++ }
	t.Cleanup(func() { heavyRequestRetryCleanup = prevCleanup })

	result, errExecute := m.ExecuteStream(context.Background(), []string{"codex"}, cliproxyexecutor.Request{Model: model}, cliproxyexecutor.Options{Stream: true})
	if errExecute != nil {
		t.Fatalf("execute stream error = %v, want success after retry", errExecute)
	}
	for chunk := range result.Chunks {
		if chunk.Err != nil {
			t.Fatalf("unexpected stream chunk error: %v", chunk.Err)
		}
	}

	if cleanupCalls != 0 {
		t.Fatalf("cleanup calls = %d, want 0", cleanupCalls)
	}
}

func TestDefaultHeavyRequestRetryCleanupThrottlesAllCleanup(t *testing.T) {
	previousFree := heavyRequestFreeOSMemory
	previousLast := lastHeavyRequestFreeOS.Load()
	calls := 0
	heavyRequestFreeOSMemory = func() { calls++ }
	lastHeavyRequestFreeOS.Store(0)
	t.Cleanup(func() {
		heavyRequestFreeOSMemory = previousFree
		lastHeavyRequestFreeOS.Store(previousLast)
	})

	defaultHeavyRequestRetryCleanup()
	defaultHeavyRequestRetryCleanup()
	defaultHeavyRequestRetryCleanup()

	if calls != 1 {
		t.Fatalf("cleanup calls inside throttle window = %d, want 1", calls)
	}

	lastHeavyRequestFreeOS.Store(time.Now().Add(-heavyRequestFreeOSMemoryMinInterval - time.Millisecond).UnixNano())
	defaultHeavyRequestRetryCleanup()
	if calls != 2 {
		t.Fatalf("cleanup calls after throttle window = %d, want 2", calls)
	}
}

type contextAwareStreamExecutor struct {
	id      string
	ctxDone <-chan struct{}
}

func (e *contextAwareStreamExecutor) Identifier() string { return e.id }

func (e *contextAwareStreamExecutor) Execute(context.Context, *Auth, cliproxyexecutor.Request, cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
	return cliproxyexecutor.Response{}, &Error{HTTPStatus: http.StatusNotImplemented, Message: "not implemented"}
}

func (e *contextAwareStreamExecutor) ExecuteStream(ctx context.Context, _ *Auth, _ cliproxyexecutor.Request, _ cliproxyexecutor.Options) (*cliproxyexecutor.StreamResult, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	e.ctxDone = ctx.Done()
	ch := make(chan cliproxyexecutor.StreamChunk, 1)
	ch <- cliproxyexecutor.StreamChunk{Payload: []byte("ok")}
	close(ch)
	return &cliproxyexecutor.StreamResult{Chunks: ch}, nil
}

func (e *contextAwareStreamExecutor) Refresh(_ context.Context, auth *Auth) (*Auth, error) {
	return auth, nil
}

func (e *contextAwareStreamExecutor) CountTokens(context.Context, *Auth, cliproxyexecutor.Request, cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
	return cliproxyexecutor.Response{}, &Error{HTTPStatus: http.StatusNotImplemented, Message: "not implemented"}
}

func (e *contextAwareStreamExecutor) HttpRequest(context.Context, *Auth, *http.Request) (*http.Response, error) {
	return nil, nil
}

func TestManagerExecuteStream_KeepsHeavyAttemptContextUntilSuccessfulStreamDrained(t *testing.T) {
	m := NewManager(nil, nil, nil)
	executor := &contextAwareStreamExecutor{id: "codex"}
	m.RegisterExecutor(executor)

	model := "gpt-5.4-heavy-success-context"
	auth := &Auth{ID: "aa-success", Provider: "codex"}
	reg := registry.GetGlobalRegistry()
	reg.RegisterClient(auth.ID, "codex", []*registry.ModelInfo{{ID: model}})
	t.Cleanup(func() { reg.UnregisterClient(auth.ID) })
	if _, errRegister := m.Register(context.Background(), auth); errRegister != nil {
		t.Fatalf("register auth: %v", errRegister)
	}

	result, errExecute := m.ExecuteStream(context.Background(), []string{"codex"}, cliproxyexecutor.Request{Model: model}, cliproxyexecutor.Options{
		Stream: true,
		Metadata: map[string]any{
			cliproxyexecutor.HeavyRequestMetadataKey: true,
		},
	})
	if errExecute != nil {
		t.Fatalf("execute stream error: %v", errExecute)
	}
	if executor.ctxDone == nil {
		t.Fatal("executor did not capture attempt context")
	}
	select {
	case <-executor.ctxDone:
		t.Fatal("attempt context was canceled before successful stream was consumed")
	default:
	}

	for chunk := range result.Chunks {
		if chunk.Err != nil {
			t.Fatalf("unexpected stream chunk error: %v", chunk.Err)
		}
	}
	select {
	case <-executor.ctxDone:
	case <-time.After(time.Second):
		t.Fatal("attempt context was not canceled after successful stream drained")
	}
}
