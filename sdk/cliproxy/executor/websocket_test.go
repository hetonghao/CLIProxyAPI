package executor

import (
	"errors"
	"fmt"
	"net/http"
	"testing"
)

func TestUpstreamWebsocketReplayRequiredError(t *testing.T) {
	err := NewUpstreamWebsocketReplayRequiredError()
	if !IsUpstreamWebsocketReplayRequired(err) {
		t.Fatal("replay error was not recognized")
	}
	if !IsUpstreamWebsocketReplayRequired(fmt.Errorf("wrapped: %w", err)) {
		t.Fatal("wrapped replay error was not recognized")
	}
	statusErr, ok := err.(interface{ StatusCode() int })
	if !ok || statusErr.StatusCode() != http.StatusUpgradeRequired {
		t.Fatalf("replay error = %T %v, want status 426", err, err)
	}
	requestErr, ok := err.(RequestScopedError)
	if !ok || !requestErr.IsRequestScoped() {
		t.Fatalf("replay error = %T, want request scoped", err)
	}
}

type capacityStatusError struct{}

func (capacityStatusError) Error() string { return "capacity" }

func (capacityStatusError) StatusCode() int { return http.StatusServiceUnavailable }

func TestResponsesWebsocketCapacityRejectedError(t *testing.T) {
	err := NewResponsesWebsocketCapacityRejectedError(capacityStatusError{}, "server_is_overloaded")
	if !IsResponsesWebsocketCapacityRejected(err) {
		t.Fatal("capacity rejection was not recognized")
	}
	if !IsResponsesWebsocketCapacityRejected(fmt.Errorf("wrapped: %w", err)) {
		t.Fatal("wrapped capacity rejection was not recognized")
	}
	code, ok := ResponsesWebsocketCapacityRejectedCode(err)
	if !ok || code != "server_is_overloaded" {
		t.Fatalf("capacity code = %q, %t; want server_is_overloaded", code, ok)
	}
	wantReason := "ai-cove-capacity/v1;state=rejected;phase=pre_output;code=server_is_overloaded"
	if got := ResponsesWebsocketCapacityCloseReason(code); got != wantReason {
		t.Fatalf("capacity close reason = %q, want %q", got, wantReason)
	}
	if got, ok := ResponsesWebsocketCapacityCloseReasonCode(wantReason); !ok || got != code {
		t.Fatalf("capacity close reason parse = %q, %t; want %q, true", got, ok, code)
	}
	for _, reason := range []string{
		"ai-cove-capacity/v2;state=rejected;phase=pre_output;code=server_is_overloaded",
		"ai-cove-capacity/v1;state=unknown;phase=pre_output;code=server_is_overloaded",
		"ai-cove-capacity/v1;state=rejected;phase=post_output;code=server_is_overloaded",
		"ai-cove-capacity/v1;state=rejected;phase=pre_output;code=usage_limit_reached",
		"ai-cove-capacity/v1;state=rejected;phase=pre_output;code=server_is_overloaded;secret=token",
	} {
		if got, ok := ResponsesWebsocketCapacityCloseReasonCode(reason); ok || got != "" {
			t.Fatalf("malformed capacity reason parsed as %q, %t: %q", got, ok, reason)
		}
	}
	if IsResponsesWebsocketCapacityRejected(errors.New("usage_limit_reached")) {
		t.Fatal("untyped quota error was recognized as capacity rejection")
	}
}
