package executor

import (
	"context"
	"net/http"
	"testing"

	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

func TestApplyCodexWebsocketHeadersRemovesResponseOnlyTrace(t *testing.T) {
	tests := []struct {
		name          string
		headers       http.Header
		auth          *cliproxyauth.Auth
		clientHeaders http.Header
	}{
		{
			name:    "initial headers",
			headers: http.Header{http.CanonicalHeaderKey(websocketTraceHeader): []string{"initial-trace"}},
		},
		{
			name: "client magic header",
			auth: &cliproxyauth.Auth{Attributes: map[string]string{
				"header:" + websocketTraceHeader: "$" + websocketTraceHeader,
			}},
			clientHeaders: http.Header{websocketTraceHeader: []string{"client-trace"}},
		},
		{
			name: "auth static header",
			auth: &cliproxyauth.Auth{Attributes: map[string]string{
				"header:" + websocketTraceHeader: "auth-trace",
			}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := applyCodexWebsocketHeaders(context.Background(), tt.headers, tt.auth, "", nil, tt.clientHeaders)

			if trace := got.Get(websocketTraceHeader); trace != "" {
				t.Fatalf("response-only trace = %q, want absent", trace)
			}
		})
	}
}
