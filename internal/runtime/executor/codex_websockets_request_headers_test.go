package executor

import (
	"context"
	"net/http"
	"testing"

	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

func TestApplyCodexWebsocketHeadersRemovesResponseOnlyTraceAfterCustomHeaders(t *testing.T) {
	auth := &cliproxyauth.Auth{Attributes: map[string]string{
		"header:X-AI-Cove-WS-Trace": "client-injected-trace",
	}}
	headers := http.Header{websocketTraceHeader: []string{"client-trace"}}

	got := applyCodexWebsocketHeaders(context.Background(), headers, auth, "", nil)

	if trace := got.Get(websocketTraceHeader); trace != "" {
		t.Fatalf("response-only trace = %q, want absent after custom headers", trace)
	}
}
