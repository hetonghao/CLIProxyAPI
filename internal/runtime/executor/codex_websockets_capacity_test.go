package executor

import (
	"errors"
	"net/http"
	"testing"

	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
)

func TestMaybeCodexResponsesWebsocketCapacityError_marksOnlyPreOutput(t *testing.T) {
	base := statusErr{code: http.StatusServiceUnavailable, msg: `{"error":{"code":"server_is_overloaded","message":"Authorization secret must not cross the sideband"}}`}
	event := []byte(`{"type":"response.failed","response":{"error":{"type":"service_unavailable_error","code":"server_is_overloaded","message":"Authorization secret must not cross the sideband"}}}`)

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

	postOutput := maybeCodexResponsesWebsocketCapacityError(base, event, false)
	if cliproxyexecutor.IsResponsesWebsocketCapacityRejected(postOutput) {
		t.Fatal("post-output capacity error was marked for replay")
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

func TestCodexResponsesWebsocketPayloadHasOutput_isConservative(t *testing.T) {
	for _, event := range []string{
		`{"type":"response.created"}`,
		`{"type":"response.in_progress"}`,
		`{"type":"codex.rate_limits"}`,
		`{"type":"codex.response.metadata"}`,
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
	if !codexResponsesWebsocketPayloadHasOutput([]byte(`{"type":"future.event"}`)) {
		t.Fatal("unknown event was not failed closed")
	}
}
