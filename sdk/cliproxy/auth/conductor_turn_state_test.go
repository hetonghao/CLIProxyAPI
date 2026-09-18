package auth

import (
	"context"
	"net/http"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/turnstate"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
)

func TestApplyRequestAfterAuthInterceptorStripsForeignTurnState(t *testing.T) {
	headers := http.Header{turnstate.Header: []string{"value-a"}}
	opts := cliproxyexecutor.Options{
		Headers:  headers,
		Metadata: map[string]any{cliproxyexecutor.SelectedAuthMetadataKey: "auth-b"},
	}

	_, got, err := applyRequestAfterAuthInterceptor(context.Background(), nil, "codex", cliproxyexecutor.Request{}, opts, "")
	if err != nil {
		t.Fatalf("intercept error: %v", err)
	}
	if got.Headers.Get(turnstate.Header) != "" {
		t.Fatalf("foreign value must be stripped, got %q", got.Headers.Get(turnstate.Header))
	}
	if headers.Get(turnstate.Header) == "" {
		t.Fatalf("caller headers must stay untouched")
	}

	sameCredential := cliproxyexecutor.Options{
		Headers:  http.Header{turnstate.Header: []string{"value-c"}},
		Metadata: map[string]any{cliproxyexecutor.SelectedAuthMetadataKey: "auth-c"},
	}
	turnstate.Record(http.Header{turnstate.Header: []string{"value-c"}}, "auth-c")
	_, got, err = applyRequestAfterAuthInterceptor(context.Background(), nil, "codex", cliproxyexecutor.Request{}, sameCredential, "")
	if err != nil {
		t.Fatalf("intercept error: %v", err)
	}
	if got.Headers.Get(turnstate.Header) != "value-c" {
		t.Fatalf("same credential must keep the header, got %q", got.Headers.Get(turnstate.Header))
	}
}
