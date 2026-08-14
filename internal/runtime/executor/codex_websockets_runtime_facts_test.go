package executor

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v7/sdk/translator"
	log "github.com/sirupsen/logrus"
	logtest "github.com/sirupsen/logrus/hooks/test"
)

func TestCodexWebsocketsExecuteStreamRecordsTraceLifecycleAndRuntimeFacts(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	requestTrace := "0123456789abcdef0123456789abcdef"
	upstreamTrace := "abcdef0123456789abcdef0123456789"
	requestHeaders := make(chan http.Header, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestHeaders <- r.Header.Clone()
		conn, errUpgrade := upgrader.Upgrade(w, r, http.Header{websocketTraceHeader: []string{upstreamTrace}})
		if errUpgrade != nil {
			t.Errorf("upgrade websocket: %v", errUpgrade)
			return
		}
		if _, _, errRead := conn.ReadMessage(); errRead != nil {
			t.Errorf("read upstream websocket message: %v", errRead)
			return
		}
		if errWrite := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.completed","response":{"id":"resp-1","output":[],"usage":{"input_tokens":0,"output_tokens":0,"total_tokens":0}}}`)); errWrite != nil {
			t.Errorf("write completed websocket message: %v", errWrite)
		}
		_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		_, _, _ = conn.ReadMessage()
		_ = conn.Close()
	}))
	defer server.Close()

	previousLevel := log.GetLevel()
	log.SetLevel(log.InfoLevel)
	hook := logtest.NewLocal(log.StandardLogger())
	t.Cleanup(func() {
		hook.Reset()
		log.SetLevel(previousLevel)
	})

	exec := NewCodexWebsocketsExecutor(&config.Config{SDKConfig: config.SDKConfig{DisableImageGeneration: config.DisableImageGenerationAll}})
	const executionSessionID = "observability-session"
	t.Cleanup(func() { exec.CloseExecutionSession(executionSessionID) })
	auth := &cliproxyauth.Auth{ID: "observability-auth", Attributes: map[string]string{"api_key": "sk-test", "base_url": server.URL}}
	result, errExecute := exec.ExecuteStream(context.Background(), auth, cliproxyexecutor.Request{
		Model:   "gpt-5-codex",
		Payload: []byte(`{"model":"gpt-5-codex","input":[{"type":"message","role":"user","content":"hello"}]}`),
	}, cliproxyexecutor.Options{
		SourceFormat: sdktranslator.FromString("openai-response"),
		Headers:      http.Header{websocketTraceHeader: []string{requestTrace}},
		Metadata: map[string]any{
			cliproxyexecutor.ExecutionSessionMetadataKey: executionSessionID,
			cliproxyexecutor.WebsocketTraceMetadataKey:   "fedcba9876543210fedcba9876543210",
		},
	})
	if errExecute != nil {
		t.Fatalf("ExecuteStream() error = %v", errExecute)
	}
	for chunk := range result.Chunks {
		if chunk.Err != nil {
			t.Fatalf("stream chunk error = %v", chunk.Err)
		}
	}

	select {
	case headers := <-requestHeaders:
		if got := headers.Get(websocketTraceHeader); got != "" {
			t.Fatalf("response-only trace was forwarded upstream: %q", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for upstream websocket request")
	}

	exec.CloseExecutionSession(executionSessionID)
	var accepted, cleanup *log.Entry
	for _, entry := range hook.AllEntries() {
		if entry.Message != "codex websockets: observation" {
			continue
		}
		switch entry.Data["event"] {
		case "accepted":
			accepted = entry
		case "cleanup":
			cleanup = entry
		}
	}
	if accepted == nil {
		t.Fatal("missing structured websocket accepted diagnostic")
	}
	if got := accepted.Data["active_channel_capacity"]; got != int64(4096) {
		t.Fatalf("active_channel_capacity = %#v, want 4096", got)
	}
	if cleanup == nil {
		t.Fatal("missing structured websocket cleanup diagnostic")
	}
	if got := cleanup.Data["downstream_trace"]; got != "fedcba9876543210fedcba9876543210" {
		t.Fatalf("downstream_trace = %#v, want fedcba9876543210fedcba9876543210", got)
	}
	if got := cleanup.Data["upstream_trace"]; got != upstreamTrace {
		t.Fatalf("upstream_trace = %#v, want %s", got, upstreamTrace)
	}
	if got := cleanup.Data["upstream_generation"]; got != uint64(1) {
		t.Fatalf("upstream_generation = %#v, want 1", got)
	}
	if got := cleanup.Data["downstream_ordinal"]; got != uint64(1) {
		t.Fatalf("downstream_ordinal = %#v, want 1", got)
	}
	if got := cleanup.Data["upstream_ordinal"]; got != uint64(1) {
		t.Fatalf("upstream_ordinal = %#v, want 1", got)
	}
	if got := cleanup.Data["terminal_seen"]; got != true {
		t.Fatalf("terminal_seen = %#v, want true", got)
	}
	if got := cleanup.Data["request_state"]; got != "idle" {
		t.Fatalf("request_state = %#v, want idle", got)
	}
	if got := cleanup.Data["active_channel_bytes"]; got != int64(0) {
		t.Fatalf("active_channel_bytes = %#v, want 0", got)
	}
	if got := cleanup.Data["last_ping_tx_age_ms"]; got != int64(-1) {
		t.Fatalf("last_ping_tx_age_ms = %#v, want -1 when CPA never autonomously sends Ping", got)
	}
	authDigest, ok := cleanup.Data["auth_digest"].(string)
	if !ok || authDigest == "" || authDigest == auth.ID {
		t.Fatalf("auth_digest = %#v, want non-empty opaque digest", cleanup.Data["auth_digest"])
	}
	mac := hmac.New(sha256.New, []byte(websocketObservationKey))
	mac.Write([]byte("cli-proxy-api:codex-websocket:auth:v1\x00" + auth.ID))
	wantAuthDigest := hex.EncodeToString(mac.Sum(nil)[:8])
	if authDigest != wantAuthDigest {
		t.Fatalf("auth_digest = %q, want HMAC digest %q", authDigest, wantAuthDigest)
	}
	if strings.Contains(fmt.Sprint(cleanup.Data), auth.ID) {
		t.Fatalf("cleanup data leaked raw auth ID %q: %#v", auth.ID, cleanup.Data)
	}
	if _, ok := cleanup.Data["heap_alloc"]; !ok {
		t.Fatal("cleanup diagnostic missing heap_alloc")
	}
	if _, ok := cleanup.Data["heap_idle"]; !ok {
		t.Fatal("cleanup diagnostic missing heap_idle")
	}
	if _, ok := cleanup.Data["heap_released"]; !ok {
		t.Fatal("cleanup diagnostic missing heap_released")
	}
	if _, ok := cleanup.Data["goroutines"]; !ok {
		t.Fatal("cleanup diagnostic missing goroutines")
	}
	for _, forbidden := range []string{executionSessionID, auth.ID, requestTrace, "hello"} {
		if strings.Contains(cleanup.Message, forbidden) {
			t.Fatalf("cleanup message leaked %q: %q", forbidden, cleanup.Message)
		}
	}
}
