package helps

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"runtime"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	log "github.com/sirupsen/logrus"
)

func ValidWebsocketTrace(value string) bool {
	if len(value) != 32 {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && hex.EncodeToString(decoded) == value
}

func NewWebsocketTrace() (string, error) {
	var raw [16]byte
	if _, err := io.ReadFull(rand.Reader, raw[:]); err != nil {
		return "", fmt.Errorf("generate websocket trace: %w", err)
	}
	return hex.EncodeToString(raw[:]), nil
}

func WebsocketTraceFromOptions(opts cliproxyexecutor.Options) string {
	if value, ok := opts.Metadata[cliproxyexecutor.WebsocketTraceMetadataKey].(string); ok && ValidWebsocketTrace(value) {
		return value
	}
	return ""
}

func WebsocketTraceFromHandshake(value string, generate func() (string, error)) (string, error) {
	if ValidWebsocketTrace(value) {
		return value, nil
	}
	if generate == nil {
		generate = NewWebsocketTrace
	}
	return generate()
}

func ObservationDigestForDomain(key, domain, value string) string {
	if value == "" || key == "" {
		return ""
	}
	mac := hmac.New(sha256.New, []byte(key))
	mac.Write([]byte("cli-proxy-api:codex-websocket:" + domain + ":v1\x00" + value))
	return hex.EncodeToString(mac.Sum(nil)[:8])
}

func ObservationReason(value string) string {
	switch strings.TrimSpace(value) {
	case "target_changed", "send_error", "terminal_failure", "upstream_error", "upstream_disconnected", "session_closed", "executor_shutdown", "unexpected_binary":
		return strings.TrimSpace(value)
	default:
		return "other"
	}
}

func CloseReason(code int) string {
	switch code {
	case websocket.CloseNormalClosure:
		return "normal"
	case websocket.CloseGoingAway:
		return "going_away"
	case websocket.CloseProtocolError:
		return "protocol_error"
	case websocket.CloseMessageTooBig:
		return "message_too_big"
	default:
		return "other"
	}
}

func NetworkErrorKind(err error) string {
	if err == nil {
		return ""
	}
	var closeErr *websocket.CloseError
	if errors.As(err, &closeErr) {
		return "websocket_close"
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return "timeout"
	}
	if errors.Is(err, io.EOF) {
		return "eof"
	}
	if errors.Is(err, net.ErrClosed) {
		return "closed"
	}
	return "network"
}

func CaptureCodexWebsocketRuntimeSnapshot(opened, closed, cleanup uint64) log.Fields {
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	return log.Fields{
		"heap_alloc": mem.HeapAlloc, "heap_idle": mem.HeapIdle, "heap_released": mem.HeapReleased,
		"goroutines": runtime.NumGoroutine(), "opened": opened, "closed": closed, "cleanup": cleanup,
	}
}

func ObservationAgeMS(now, instant time.Time) int64 {
	if instant.IsZero() {
		return -1
	}
	if age := now.Sub(instant).Milliseconds(); age >= 0 {
		return age
	}
	return 0
}

func ObservationRemainingMS(now, deadline time.Time) int64 {
	if deadline.IsZero() {
		return -1
	}
	return deadline.Sub(now).Milliseconds()
}
