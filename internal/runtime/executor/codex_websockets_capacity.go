package executor

import (
	"context"
	"errors"
	"net/http"
	"strings"

	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v7/sdk/translator"
	"github.com/tidwall/gjson"
)

var codexResponsesWebsocketHandshakeEvents = map[string]struct{}{
	"response.created":        {},
	"response.in_progress":    {},
	"codex.rate_limits":       {},
	"codex.response.metadata": {},
}

func codexResponsesWebsocketCapacitySidebandAllowed(ctx context.Context, responseFormat sdktranslator.Format) bool {
	return cliproxyexecutor.DownstreamWebsocket(ctx) && responseFormat == sdktranslator.FormatOpenAIResponse
}

// codexResponsesWebsocketPayloadHasOutput keeps the sideband fail-safe: only
// known handshake metadata with empty application fields is pre-output. New
// event types are treated as output so a future protocol addition cannot
// accidentally enable replay after an unknown application event.
func codexResponsesWebsocketPayloadHasOutput(payload []byte) bool {
	eventType := strings.TrimSpace(gjson.GetBytes(payload, "type").String())
	if _, ok := codexResponsesWebsocketHandshakeEvents[eventType]; !ok {
		return true
	}
	return codexResponsesWebsocketPayloadHasApplicationData(payload, false)
}

// codexResponsesWebsocketCapacityPayloadHasOutput validates the complete
// terminal event, rather than only its extracted error object. A typed
// capacity error may be marked only when its envelope has no output, tool,
// reasoning, usage, or stateful application fields.
func codexResponsesWebsocketCapacityPayloadHasOutput(payload []byte) bool {
	eventType := strings.TrimSpace(gjson.GetBytes(payload, "type").String())
	switch eventType {
	case "error", "response.error", "response.failed":
		return codexResponsesWebsocketPayloadHasApplicationData(payload, true)
	default:
		return true
	}
}

func codexResponsesWebsocketPayloadHasApplicationData(payload []byte, terminal bool) bool {
	if len(payload) == 0 || !gjson.ValidBytes(payload) {
		return true
	}
	root := gjson.ParseBytes(payload)
	if !root.IsObject() {
		return true
	}
	return codexResponsesWebsocketScanPayloadValue(root, "", terminal)
}

func codexResponsesWebsocketScanPayloadValue(value gjson.Result, key string, terminal bool) bool {
	if !value.Exists() || value.Type == gjson.Null {
		return false
	}
	normalizedKey := strings.ToLower(strings.TrimSpace(key))
	switch normalizedKey {
	case "output", "output_text", "reasoning", "reasoning_content", "tool", "tools", "tool_calls", "tool_call", "function", "function_call", "custom_tool_call", "content", "item", "delta", "audio", "arguments", "recipient", "annotations":
		return codexResponsesWebsocketJSONValueNonEmpty(value)
	case "usage":
		return codexResponsesWebsocketJSONValueNonZero(value)
	case "status", "status_code", "state":
		if terminal && (normalizedKey == "status" || normalizedKey == "status_code") && value.Type == gjson.Number {
			status := value.Int()
			return status < http.StatusBadRequest || status > 599
		}
		if terminal && value.Type == gjson.String {
			state := strings.ToLower(strings.TrimSpace(value.String()))
			return state != "failed" && state != "error"
		}
		if !terminal && value.Type == gjson.String && strings.EqualFold(strings.TrimSpace(value.String()), "in_progress") {
			return false
		}
		return codexResponsesWebsocketJSONValueNonEmpty(value)
	}

	if value.IsObject() {
		found := false
		value.ForEach(func(childKey, childValue gjson.Result) bool {
			if codexResponsesWebsocketScanPayloadValue(childValue, childKey.String(), terminal) {
				found = true
				return false
			}
			return true
		})
		return found
	}
	if value.IsArray() {
		for _, childValue := range value.Array() {
			if codexResponsesWebsocketScanPayloadValue(childValue, normalizedKey, terminal) {
				return true
			}
		}
	}
	return false
}

func codexResponsesWebsocketJSONValueNonEmpty(value gjson.Result) bool {
	if !value.Exists() || value.Type == gjson.Null {
		return false
	}
	switch value.Type {
	case gjson.String:
		return strings.TrimSpace(value.String()) != ""
	case gjson.Number:
		return value.Num != 0
	case gjson.True:
		return true
	case gjson.False:
		return false
	case gjson.JSON:
		if value.IsArray() {
			return len(value.Array()) > 0
		}
		return strings.TrimSpace(value.Raw) != "{}"
	default:
		return true
	}
}

func codexResponsesWebsocketJSONValueNonZero(value gjson.Result) bool {
	if !value.Exists() || value.Type == gjson.Null {
		return false
	}
	switch value.Type {
	case gjson.Number:
		return value.Num != 0
	case gjson.String:
		return strings.TrimSpace(value.String()) != ""
	case gjson.True:
		return true
	case gjson.False:
		return false
	case gjson.JSON:
		if value.IsArray() {
			for _, childValue := range value.Array() {
				if codexResponsesWebsocketJSONValueNonZero(childValue) {
					return true
				}
			}
			return false
		}
		found := false
		value.ForEach(func(_, childValue gjson.Result) bool {
			if codexResponsesWebsocketJSONValueNonZero(childValue) {
				found = true
				return false
			}
			return true
		})
		return found
	default:
		return false
	}
}

func codexResponsesWebsocketCapacityCode(payload []byte) (string, bool) {
	for _, path := range []string{
		"error.code",
		"response.error.code",
		"body.error.code",
		"code",
	} {
		switch strings.ToLower(strings.TrimSpace(gjson.GetBytes(payload, path).String())) {
		case "server_is_overloaded", "slow_down":
			return strings.ToLower(strings.TrimSpace(gjson.GetBytes(payload, path).String())), true
		}
	}
	for _, path := range []string{
		"error.message",
		"response.error.message",
		"body.error.message",
		"message",
	} {
		message := strings.ToLower(strings.TrimSpace(gjson.GetBytes(payload, path).String()))
		if strings.Contains(message, "servers are currently overloaded") || strings.Contains(message, "server is overloaded") {
			return "server_is_overloaded", true
		}
	}
	if isCodexModelCapacityError(payload) {
		return "model_capacity", true
	}
	return "", false
}

// maybeCodexResponsesWebsocketCapacityError attaches the typed sideband only
// while no application output has been observed. Ordinary EOF, quota errors,
// and post-output failures remain unchanged.
func maybeCodexResponsesWebsocketCapacityError(err error, payload []byte, preOutput bool) error {
	if !preOutput || codexResponsesWebsocketCapacityPayloadHasOutput(payload) {
		return err
	}
	var statusErr cliproxyexecutor.StatusError
	if !errors.As(err, &statusErr) || statusErr == nil {
		return err
	}
	code, ok := codexResponsesWebsocketCapacityCode(payload)
	if !ok {
		return err
	}
	return cliproxyexecutor.NewResponsesWebsocketCapacityRejectedError(statusErr, code)
}
