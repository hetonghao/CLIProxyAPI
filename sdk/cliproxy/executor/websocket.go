package executor

import (
	"errors"
	"net/http"
	"strings"
	"time"
)

// UpstreamWebsocketReplayRequiredError indicates that an incremental request
// cannot safely continue because its upstream websocket is no longer reusable.
type UpstreamWebsocketReplayRequiredError struct{}

func (*UpstreamWebsocketReplayRequiredError) Error() string {
	return `{"error":{"message":"upstream transport requires full HTTP replay","type":"server_error","code":"upstream_http_replay_required","status":426}}`
}

func (*UpstreamWebsocketReplayRequiredError) StatusCode() int { return http.StatusUpgradeRequired }

func (*UpstreamWebsocketReplayRequiredError) IsRequestScoped() bool { return true }

// NewUpstreamWebsocketReplayRequiredError creates a request-scoped replay signal.
func NewUpstreamWebsocketReplayRequiredError() error {
	return &UpstreamWebsocketReplayRequiredError{}
}

// IsUpstreamWebsocketReplayRequired reports whether err is the internal replay signal.
func IsUpstreamWebsocketReplayRequired(err error) bool {
	var replayErr *UpstreamWebsocketReplayRequiredError
	return errors.As(err, &replayErr)
}

const (
	// ResponsesWebsocketCapacityCloseCode is the private close code used for an
	// explicit pre-output Codex capacity rejection. Unknown close codes remain
	// fail-safe transport failures to callers.
	ResponsesWebsocketCapacityCloseCode = 4409
	// ResponsesWebsocketCapacityCloseReasonPrefix is intentionally compact and
	// contains no request, credential, or upstream response data.
	ResponsesWebsocketCapacityCloseReasonPrefix = "ai-cove-capacity/v1;state=rejected;phase=pre_output;code="
)

// ResponsesWebsocketCapacityRejectedError carries an explicit, pre-output
// capacity rejection through the executor stack without exposing its body on
// the WebSocket close sideband.
type ResponsesWebsocketCapacityRejectedError struct {
	cause StatusError
	code  string
}

func (e *ResponsesWebsocketCapacityRejectedError) Error() string {
	if e == nil || e.cause == nil {
		return "responses websocket capacity rejected"
	}
	return e.cause.Error()
}

func (e *ResponsesWebsocketCapacityRejectedError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

func (e *ResponsesWebsocketCapacityRejectedError) StatusCode() int {
	if e == nil || e.cause == nil {
		return 0
	}
	return e.cause.StatusCode()
}

func (e *ResponsesWebsocketCapacityRejectedError) RetryAfter() *time.Duration {
	if e == nil || e.cause == nil {
		return nil
	}
	if retry, ok := e.cause.(interface{ RetryAfter() *time.Duration }); ok {
		return retry.RetryAfter()
	}
	return nil
}

func (e *ResponsesWebsocketCapacityRejectedError) IsRequestScoped() bool {
	if e == nil || e.cause == nil {
		return false
	}
	if requestScoped, ok := e.cause.(RequestScopedError); ok {
		return requestScoped.IsRequestScoped()
	}
	return false
}

func (e *ResponsesWebsocketCapacityRejectedError) Headers() http.Header {
	if e == nil || e.cause == nil {
		return nil
	}
	if headered, ok := e.cause.(interface{ Headers() http.Header }); ok {
		return headered.Headers()
	}
	return nil
}

// NewResponsesWebsocketCapacityRejectedError wraps a status error only when
// code is one of the explicitly supported, normalized capacity classes.
func NewResponsesWebsocketCapacityRejectedError(cause StatusError, code string) StatusError {
	if cause == nil || ResponsesWebsocketCapacityCloseReason(code) == "" {
		return cause
	}
	return &ResponsesWebsocketCapacityRejectedError{cause: cause, code: strings.TrimSpace(code)}
}

// IsResponsesWebsocketCapacityRejected reports the typed sideband marker,
// including when it is wrapped by another error.
func IsResponsesWebsocketCapacityRejected(err error) bool {
	var capacityErr *ResponsesWebsocketCapacityRejectedError
	return errors.As(err, &capacityErr) && capacityErr != nil
}

// ResponsesWebsocketCapacityRejectedCode returns the normalized sideband code.
func ResponsesWebsocketCapacityRejectedCode(err error) (string, bool) {
	var capacityErr *ResponsesWebsocketCapacityRejectedError
	if !errors.As(err, &capacityErr) || capacityErr == nil {
		return "", false
	}
	if ResponsesWebsocketCapacityCloseReason(capacityErr.code) == "" {
		return "", false
	}
	return strings.TrimSpace(capacityErr.code), true
}

// ResponsesWebsocketCapacityCloseReason serializes only the allow-listed code.
func ResponsesWebsocketCapacityCloseReason(code string) string {
	switch strings.TrimSpace(code) {
	case "server_is_overloaded", "model_capacity", "slow_down":
		return ResponsesWebsocketCapacityCloseReasonPrefix + strings.TrimSpace(code)
	default:
		return ""
	}
}

// ResponsesWebsocketCapacityCloseReasonCode parses the exact versioned,
// sanitized reason. It rejects unknown versions, states, phases, codes, and
// appended fields so callers fail closed when a proxy truncates or mutates it.
func ResponsesWebsocketCapacityCloseReasonCode(reason string) (string, bool) {
	if !strings.HasPrefix(reason, ResponsesWebsocketCapacityCloseReasonPrefix) {
		return "", false
	}
	code := strings.TrimPrefix(reason, ResponsesWebsocketCapacityCloseReasonPrefix)
	if code == "" || strings.ContainsAny(code, ";\r\n\t ") || ResponsesWebsocketCapacityCloseReason(code) != reason {
		return "", false
	}
	return code, true
}
