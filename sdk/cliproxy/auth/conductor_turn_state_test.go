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
	opts := cliproxyexecutor.Options{Headers: headers}

	_, got, err := applyRequestAfterAuthInterceptor(context.Background(), nil, "codex", &Auth{ID: "auth-b"}, cliproxyexecutor.Request{}, opts, "")
	if err != nil {
		t.Fatalf("intercept error: %v", err)
	}
	if got.Headers.Get(turnstate.Header) != "" {
		t.Fatalf("foreign value must be stripped, got %q", got.Headers.Get(turnstate.Header))
	}
	if headers.Get(turnstate.Header) == "" {
		t.Fatalf("caller headers must stay untouched")
	}

	turnstate.Record(http.Header{turnstate.Header: []string{"value-c"}}, "auth-c")
	sameCredential := cliproxyexecutor.Options{Headers: http.Header{turnstate.Header: []string{"value-c"}}}
	_, got, err = applyRequestAfterAuthInterceptor(context.Background(), nil, "codex", &Auth{ID: "auth-c"}, cliproxyexecutor.Request{}, sameCredential, "")
	if err != nil {
		t.Fatalf("intercept error: %v", err)
	}
	if got.Headers.Get(turnstate.Header) != "value-c" {
		t.Fatalf("same credential must keep the header, got %q", got.Headers.Get(turnstate.Header))
	}

	noAuth := cliproxyexecutor.Options{Headers: http.Header{turnstate.Header: []string{"value-d"}}}
	_, got, err = applyRequestAfterAuthInterceptor(context.Background(), nil, "codex", nil, cliproxyexecutor.Request{}, noAuth, "")
	if err != nil {
		t.Fatalf("intercept error: %v", err)
	}
	if got.Headers.Get(turnstate.Header) != "value-d" {
		t.Fatalf("missing auth must leave the header untouched, got %q", got.Headers.Get(turnstate.Header))
	}
}
