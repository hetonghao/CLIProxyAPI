package executor

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v7/sdk/translator"
)

func TestMaybeCodexResponsesWebsocketCapacityError_marksOnlyPreOutput(t *testing.T) {
	base := statusErr{code: http.StatusServiceUnavailable, msg: `{"error":{"code":"server_is_overloaded","message":"Authorization secret must not cross the sideband"}}`}
	event := []byte(`{"type":"response.failed","response":{"status":"failed","error":{"type":"service_unavailable_error","code":"server_is_overloaded","message":"Authorization secret must not cross the sideband"}}}`)

	marked := maybeCodexResponsesWebsocketCapacityError(base, event, true)
	if !cliproxyexecutor.IsResponsesWebsocketCapacityRejected(marked) {
		t.Fatal("pre-output capacity error was not marked")
	}
	code, ok := cliproxyexecutor.ResponsesWebsocketCapacityRejectedCode(marked)
	if !ok || code != "server_is_overloaded" {
		t.Fatalf("marked capacity code = %q, %t; want server_is_overloaded, true", code, ok)
	}
	if got := cliproxyexecutor.ResponsesWebsocketCapacityCloseReason(code); got != "ai-cove-capacity/v1;state=rejected;phase=pre_output;code=server_is_overloaded" {
		t.Fatalf("marked capacity reason = %q", got)
	}
	if errors.Is(marked, base) {
		t.Log("marked error preserves the original status error")
	}

	statusEvent := []byte(`{"type":"error","status":503,"error":{"type":"service_unavailable_error","code":"server_is_overloaded"}}`)
	if !cliproxyexecutor.IsResponsesWebsocketCapacityRejected(maybeCodexResponsesWebsocketCapacityError(base, statusEvent, true)) {
		t.Fatal("plain HTTP-status capacity error was not marked")
	}

	postOutput := maybeCodexResponsesWebsocketCapacityError(base, event, false)
	if cliproxyexecutor.IsResponsesWebsocketCapacityRejected(postOutput) {
		t.Fatal("post-output capacity error was marked for replay")
	}
}

func TestCodexResponsesWebsocketCapacitySidebandRequiresResponsesFormat(t *testing.T) {
	ctx := cliproxyexecutor.WithDownstreamWebsocket(context.Background())
	if !codexResponsesWebsocketCapacitySidebandAllowed(ctx, sdktranslator.FormatOpenAIResponse) {
		t.Fatal("Responses WebSocket format was not allowed")
	}
	for _, format := range []sdktranslator.Format{sdktranslator.FormatOpenAI, sdktranslator.FormatClaude, sdktranslator.FormatCodex} {
		if codexResponsesWebsocketCapacitySidebandAllowed(ctx, format) {
			t.Fatalf("non-Responses format %q was allowed", format)
		}
	}
	if codexResponsesWebsocketCapacitySidebandAllowed(context.Background(), sdktranslator.FormatOpenAIResponse) {
		t.Fatal("non-WebSocket request was allowed")
	}
}

func TestMaybeCodexResponsesWebsocketCapacityError_rejectsQuotaAndUnknownEvents(t *testing.T) {
	base := statusErr{code: http.StatusTooManyRequests, msg: "quota"}
	cases := []struct {
		name  string
		event []byte
	}{
		{
			name:  "quota",
			event: []byte(`{"type":"response.failed","response":{"error":{"type":"usage_limit_reached","code":"usage_limit_reached"}}}`),
		},
		{
			name:  "unknown transport",
			event: []byte(`{"type":"error","error":{"message":"unexpected EOF"}}`),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			marked := maybeCodexResponsesWebsocketCapacityError(base, tc.event, true)
			if cliproxyexecutor.IsResponsesWebsocketCapacityRejected(marked) {
				t.Fatal("non-capacity error was marked")
			}
		})
	}
}

func TestMaybeCodexResponsesWebsocketCapacityError_rejectsOutputBearingErrorEnvelope(t *testing.T) {
	base := statusErr{code: http.StatusServiceUnavailable, msg: `{"error":{"code":"server_is_overloaded"}}`}
	cases := []struct {
		name  string
		event string
	}{
		{
			name:  "response failed output",
			event: `{"type":"response.failed","response":{"status":"failed","output":[{"type":"function_call"}],"error":{"code":"server_is_overloaded"}}}`,
		},
		{
			name:  "error reasoning",
			event: `{"type":"error","status":503,"reasoning":{"summary":"already started"},"error":{"code":"server_is_overloaded"}}`,
		},
		{
			name:  "error tool call",
			event: `{"type":"error","status":503,"tool_calls":[{"id":"call-1"}],"error":{"code":"server_is_overloaded"}}`,
		},
		{
			name:  "error single tool",
			event: `{"type":"error","status":503,"tool":{"name":"shell"},"error":{"code":"server_is_overloaded"}}`,
		},
		{
			name:  "nonterminal state",
			event: `{"type":"response.failed","response":{"status":"in_progress","error":{"code":"server_is_overloaded"}}}`,
		},
		{
			name:  "terminal unknown tool object",
			event: `{"type":"response.failed","response":{"status":"failed","server_tool_use":{"id":"call-1"},"error":{"code":"server_is_overloaded"}}}`,
		},
		{
			name:  "terminal unknown scalar",
			event: `{"type":"error","status":503,"computer_call":"call-1","error":{"code":"server_is_overloaded"}}`,
		},
		{
			name:  "terminal nested unknown metadata",
			event: `{"type":"response.failed","response":{"status":"failed","metadata":{"computer_call":"call-1"},"error":{"code":"server_is_overloaded"}}}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			marked := maybeCodexResponsesWebsocketCapacityError(base, []byte(tc.event), true)
			if cliproxyexecutor.IsResponsesWebsocketCapacityRejected(marked) {
				t.Fatal("output-bearing error envelope was marked for replay")
			}
		})
	}
}

func TestInvalidateResponsesWebsocketCapacityErrorDoesNotNotify(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade websocket: %v", err)
			return
		}
		defer func() { _ = conn.Close() }()
		_, _, _ = conn.ReadMessage()
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	defer func() { _ = conn.Close() }()

	exec := NewCodexWebsocketsExecutor(&config.Config{})
	exec.store = &codexWebsocketSessionStore{sessions: make(map[string]*codexWebsocketSession)}
	sessionID := "capacity-no-notify"
	disconnectCh := exec.UpstreamDisconnectChan(sessionID)
	sess := exec.getOrCreateSession(sessionID)
	sess.connMu.Lock()
	sess.conn = conn
	sess.authID = "auth-1"
	sess.wsURL = wsURL
	sess.readerConn = conn
	sess.connMu.Unlock()

	base := statusErr{code: http.StatusServiceUnavailable, msg: `{"error":{"code":"server_is_overloaded"}}`}
	marker := maybeCodexResponsesWebsocketCapacityError(base, []byte(`{"type":"error","status":503,"error":{"code":"server_is_overloaded"}}`), true)
	if !cliproxyexecutor.IsResponsesWebsocketCapacityRejected(marker) {
		t.Fatal("capacity error was not marked")
	}
	exec.invalidateUpstreamConnForResponsesWebsocketError(sess, conn, "terminal_failure", marker)

	select {
	case got := <-disconnectCh:
		t.Fatalf("capacity invalidation notified downstream: %v", got)
	default:
	}
}

func TestCodexResponsesWebsocketPayloadHasOutput_isConservative(t *testing.T) {
	for _, event := range []string{
		`{"type":"response.created"}`,
		`{"type":"response.in_progress"}`,
		`{"type":"codex.rate_limits","rate_limits":{"primary":{"used_percent":1}}}`,
		`{"type":"codex.response.metadata","metadata":{"conversation_id":"conv_1"}}`,
	} {
		if codexResponsesWebsocketPayloadHasOutput([]byte(event)) {
			t.Fatalf("handshake event treated as output: %s", event)
		}
	}
	if !codexResponsesWebsocketPayloadHasOutput([]byte(`{"type":"response.output_text.delta","delta":"x"}`)) {
		t.Fatal("output event was treated as pre-output")
	}
	if !codexResponsesWebsocketPayloadHasOutput([]byte(`{"type":"response.created","response":{"usage":{"total_tokens":1}}}`)) {
		t.Fatal("handshake usage event was treated as pre-output")
	}
	if !codexResponsesWebsocketPayloadHasOutput([]byte(`{"type":"response.created","response":{"usage":{"input_token_details":{"cached_tokens":1}}}}`)) {
		t.Fatal("nested usage detail was treated as pre-output")
	}
	for _, event := range []string{
		`{"type":"response.created","response":{"output":[{"type":"function_call"}]}}`,
		`{"type":"response.created","response":{"reasoning":{"summary":"started"}}}`,
		`{"type":"response.created","response":{"tool_calls":[{"id":"call-1"}]}}`,
		`{"type":"response.created","response":{"metadata":{"server_tool_use":{"id":"call-1"}}}}`,
		`{"type":"response.created","response":{"server_tool_use":{"id":"call-1"}}}`,
		`{"type":"response.created","response":{"computer_call":{"id":"call-1"}}}`,
		`{"type":"response.created","response":{"future_field":"unexpected"}}`,
		`{"type":"response.created","future_field":"unexpected"}`,
	} {
		if !codexResponsesWebsocketPayloadHasOutput([]byte(event)) {
			t.Fatalf("application-bearing handshake event treated as pre-output: %s", event)
		}
	}
	if codexResponsesWebsocketPayloadHasOutput([]byte(`{"type":"response.created","response":{"metadata":{"conversation_id":"conv-1"}}}`)) {
		t.Fatal("known response metadata was treated as output")
	}
	if !codexResponsesWebsocketPayloadHasOutput([]byte(`{"type":"future.event"}`)) {
		t.Fatal("unknown event was not failed closed")
	}
}

func TestCodexResponsesWebsocketCapacityCode_acceptsDirectModelCapacityCode(t *testing.T) {
	for _, payload := range []string{
		`{"code":"model_capacity"}`,
		`{"error":{"code":"model_capacity"}}`,
		`{"body":{"code":"model_capacity"}}`,
		`{"response":{"error":{"code":"model_capacity"}}}`,
		`{"response":{"code":"model_capacity"}}`,
	} {
		code, ok := codexResponsesWebsocketCapacityCode([]byte(payload))
		if !ok || code != "model_capacity" {
			t.Fatalf("capacity code for %s = %q, %t; want model_capacity, true", payload, code, ok)
		}
	}
}
