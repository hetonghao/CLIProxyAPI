package executor

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v7/sdk/translator"
	"github.com/tidwall/gjson"
)

func setCodexWebsocketLivenessTestTimeouts(t *testing.T, idle, firstApplication, probe time.Duration) {
	t.Helper()
	previousIdle := codexResponsesWebsocketIdleTimeout
	previousFirstApplication := codexResponsesWebsocketFirstApplicationTimeout
	previousProbe := codexResponsesWebsocketProbeTimeout
	codexResponsesWebsocketIdleTimeout = idle
	codexResponsesWebsocketFirstApplicationTimeout = firstApplication
	codexResponsesWebsocketProbeTimeout = probe
	t.Cleanup(func() {
		codexResponsesWebsocketIdleTimeout = previousIdle
		codexResponsesWebsocketFirstApplicationTimeout = previousFirstApplication
		codexResponsesWebsocketProbeTimeout = previousProbe
	})
}

func codexWebsocketLivenessTestAuth(serverURL string) *cliproxyauth.Auth {
	return &cliproxyauth.Auth{
		ID:       "auth-liveness",
		Provider: "codex",
		Attributes: map[string]string{
			"api_key":  "sk-test",
			"base_url": serverURL,
		},
	}
}

func codexWebsocketLivenessTestRequest() cliproxyexecutor.Request {
	return cliproxyexecutor.Request{
		Model:   "gpt-5-codex",
		Payload: []byte(`{"model":"gpt-5-codex","input":[{"type":"message","role":"user","content":"hello"}]}`),
	}
}

func codexWebsocketLivenessTestOptions(sessionID string) cliproxyexecutor.Options {
	return cliproxyexecutor.Options{
		SourceFormat:   sdktranslator.FromString("openai-response"),
		ResponseFormat: sdktranslator.FromString("openai-response"),
		Metadata: map[string]any{
			cliproxyexecutor.ExecutionSessionMetadataKey: sessionID,
		},
	}
}

func consumeCodexWebsocketLivenessStream(result *cliproxyexecutor.StreamResult) error {
	var streamErr error
	for chunk := range result.Chunks {
		if chunk.Err != nil {
			streamErr = chunk.Err
		}
	}
	return streamErr
}

func TestCodexWebsocketKeepsActiveConnectionAliveWhenUpstreamSendsPing(t *testing.T) {
	setCodexWebsocketLivenessTestTimeouts(t, 100*time.Millisecond, 200*time.Millisecond, time.Second)
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	heartbeatReached := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, errUpgrade := upgrader.Upgrade(w, r, nil)
		if errUpgrade != nil {
			t.Errorf("upgrade websocket: %v", errUpgrade)
			return
		}
		defer func() { _ = conn.Close() }()
		if _, _, errRead := conn.ReadMessage(); errRead != nil {
			return
		}

		ticker := time.NewTicker(5 * time.Millisecond)
		defer ticker.Stop()
		for count := 0; ; count++ {
			select {
			case <-ticker.C:
				if errWrite := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(time.Second)); errWrite != nil {
					return
				}
				if count >= 10 {
					select {
					case heartbeatReached <- struct{}{}:
					default:
					}
				}
			}
		}
	}))
	defer server.Close()

	exec := NewCodexWebsocketsExecutor(&config.Config{SDKConfig: config.SDKConfig{DisableImageGeneration: config.DisableImageGenerationAll}})
	defer exec.CloseExecutionSession("ping-liveness")
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	started := time.Now()
	result, errExecute := exec.ExecuteStream(ctx, codexWebsocketLivenessTestAuth(server.URL), codexWebsocketLivenessTestRequest(), codexWebsocketLivenessTestOptions("ping-liveness"))
	if errExecute != nil {
		t.Fatalf("ExecuteStream() error = %v", errExecute)
	}
	streamErrCh := make(chan error, 1)
	go func() { streamErrCh <- consumeCodexWebsocketLivenessStream(result) }()

	select {
	case <-heartbeatReached:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("upstream Ping heartbeat did not outlive the idle read deadline")
	}

	select {
	case streamErr := <-streamErrCh:
		if !errors.Is(streamErr, context.DeadlineExceeded) {
			t.Fatalf("stream error = %v, want first-application timeout", streamErr)
		}
		if elapsed := time.Since(started); elapsed >= 450*time.Millisecond {
			t.Fatalf("stream waited %v, want independent first-application timeout", elapsed)
		}
	case <-time.After(300 * time.Millisecond):
		t.Fatal("stream did not stop at the first-application deadline")
	}
}

func TestCodexWebsocketExecuteHonorsFirstApplicationTimeout(t *testing.T) {
	setCodexWebsocketLivenessTestTimeouts(t, time.Second, 40*time.Millisecond, time.Second)
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, errUpgrade := upgrader.Upgrade(w, r, nil)
		if errUpgrade != nil {
			t.Errorf("upgrade websocket: %v", errUpgrade)
			return
		}
		defer func() { _ = conn.Close() }()
		if _, _, errRead := conn.ReadMessage(); errRead != nil {
			return
		}
		for {
			if _, _, errRead := conn.ReadMessage(); errRead != nil {
				return
			}
		}
	}))
	defer server.Close()

	exec := NewCodexWebsocketsExecutor(&config.Config{SDKConfig: config.SDKConfig{DisableImageGeneration: config.DisableImageGenerationAll}})
	started := time.Now()
	_, errExecute := exec.Execute(context.Background(), codexWebsocketLivenessTestAuth(server.URL), codexWebsocketLivenessTestRequest(), codexWebsocketLivenessTestOptions(""))
	if !errors.Is(errExecute, context.DeadlineExceeded) {
		t.Fatalf("Execute() error = %v, want first-application timeout", errExecute)
	}
	if elapsed := time.Since(started); elapsed >= 200*time.Millisecond {
		t.Fatalf("Execute() waited %v, want independent first-application timeout", elapsed)
	}
}

func TestCodexWebsocketProbesIdleConnectionBeforeSubmittingRequest(t *testing.T) {
	setCodexWebsocketLivenessTestTimeouts(t, time.Second, time.Second, 30*time.Millisecond)
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	var connections atomic.Int32
	var requests atomic.Int32
	completed := []byte(`{"type":"response.completed","response":{"id":"resp-1","output":[],"usage":{"input_tokens":0,"output_tokens":0,"total_tokens":0}}}`)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, errUpgrade := upgrader.Upgrade(w, r, nil)
		if errUpgrade != nil {
			t.Errorf("upgrade websocket: %v", errUpgrade)
			return
		}
		defer func() { _ = conn.Close() }()
		connection := connections.Add(1)
		if connection == 1 {
			conn.SetPingHandler(func(string) error {
				return conn.WriteControl(websocket.PongMessage, []byte("stale-probe"), time.Now().Add(time.Second))
			})
		}
		for {
			_, payload, errRead := conn.ReadMessage()
			if errRead != nil {
				return
			}
			if gjson.GetBytes(payload, "type").String() != "response.create" {
				continue
			}
			requests.Add(1)
			if errWrite := conn.WriteMessage(websocket.TextMessage, completed); errWrite != nil {
				return
			}
		}
	}))
	defer server.Close()

	exec := NewCodexWebsocketsExecutor(&config.Config{SDKConfig: config.SDKConfig{DisableImageGeneration: config.DisableImageGenerationAll}})
	defer exec.CloseExecutionSession("probe-liveness")
	auth := codexWebsocketLivenessTestAuth(server.URL)
	request := codexWebsocketLivenessTestRequest()
	opts := codexWebsocketLivenessTestOptions("probe-liveness")
	result, errExecute := exec.ExecuteStream(context.Background(), auth, request, opts)
	if errExecute != nil {
		t.Fatalf("first ExecuteStream() error = %v", errExecute)
	}
	if errStream := consumeCodexWebsocketLivenessStream(result); errStream != nil {
		t.Fatalf("first stream error = %v", errStream)
	}

	result, errExecute = exec.ExecuteStream(context.Background(), auth, request, opts)
	if errExecute != nil {
		t.Fatalf("second ExecuteStream() error = %v", errExecute)
	}
	if errStream := consumeCodexWebsocketLivenessStream(result); errStream != nil {
		t.Fatalf("second stream error = %v", errStream)
	}
	if got := connections.Load(); got != 2 {
		t.Fatalf("websocket connections = %d, want 2 after missing Pong probe", got)
	}
	if got := requests.Load(); got != 2 {
		t.Fatalf("upstream response.create requests = %d, want 2", got)
	}

	result, errExecute = exec.ExecuteStream(context.Background(), auth, request, opts)
	if errExecute != nil {
		t.Fatalf("third ExecuteStream() error = %v", errExecute)
	}
	if errStream := consumeCodexWebsocketLivenessStream(result); errStream != nil {
		t.Fatalf("third stream error = %v", errStream)
	}
	if got := connections.Load(); got != 2 {
		t.Fatalf("websocket connections = %d, want healthy connection reuse", got)
	}
	if got := requests.Load(); got != 3 {
		t.Fatalf("upstream response.create requests = %d, want 3", got)
	}
}
