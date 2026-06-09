package executor

import (
	"bytes"
	"sync/atomic"
	"testing"

	sdktranslator "github.com/router-for-me/CLIProxyAPI/v7/sdk/translator"
)

func TestTranslateCodexRequestPairReusesEqualPayload(t *testing.T) {
	from := sdktranslator.Format("codex-test-from-equal")
	to := sdktranslator.Format("codex-test-to-equal")
	var calls int32
	sdktranslator.Register(from, to, func(model string, rawJSON []byte, stream bool) []byte {
		atomic.AddInt32(&calls, 1)
		if model != "test-model" {
			t.Errorf("model = %q, want test-model", model)
		}
		if !stream {
			t.Error("stream = false, want true")
		}
		return append([]byte(nil), rawJSON...)
	}, sdktranslator.ResponseTransform{})

	payload := []byte(`{"model":"test-model","input":[{"role":"user"}]}`)
	originalTranslated, body := translateCodexRequestPair(from, to, "test-model", payload, bytes.Clone(payload), true)

	if gotCalls := atomic.LoadInt32(&calls); gotCalls != 1 {
		t.Fatalf("TranslateRequest calls = %d, want 1", gotCalls)
	}
	if !bytes.Equal(originalTranslated, body) {
		t.Fatalf("translated payloads differ: original=%s body=%s", originalTranslated, body)
	}
}

func TestTranslateCodexRequestPairTranslatesDifferentPayloads(t *testing.T) {
	from := sdktranslator.Format("codex-test-from-different")
	to := sdktranslator.Format("codex-test-to-different")
	var calls int32
	sdktranslator.Register(from, to, func(_ string, rawJSON []byte, _ bool) []byte {
		atomic.AddInt32(&calls, 1)
		return append([]byte(nil), rawJSON...)
	}, sdktranslator.ResponseTransform{})

	originalPayload := []byte(`{"model":"test-model","input":[{"role":"system"}]}`)
	payload := []byte(`{"model":"test-model","input":[{"role":"user"}]}`)
	originalTranslated, body := translateCodexRequestPair(from, to, "test-model", originalPayload, payload, false)

	if gotCalls := atomic.LoadInt32(&calls); gotCalls != 2 {
		t.Fatalf("TranslateRequest calls = %d, want 2", gotCalls)
	}
	if !bytes.Equal(originalTranslated, originalPayload) {
		t.Fatalf("original translated = %s, want %s", originalTranslated, originalPayload)
	}
	if !bytes.Equal(body, payload) {
		t.Fatalf("body = %s, want %s", body, payload)
	}
}

func TestCodexResponseTranslationPayloadsOmitOpenAIResponsesRequestBodies(t *testing.T) {
	originalPayload := []byte(`{"model":"test-model","input":"large"}`)
	requestPayload := []byte(`{"model":"test-model","input":"translated"}`)

	original, request := codexResponseTranslationPayloads(sdktranslator.FormatOpenAIResponse, originalPayload, requestPayload)

	if original != nil {
		t.Fatalf("original payload = %q, want nil", string(original))
	}
	if request != nil {
		t.Fatalf("request payload = %q, want nil", string(request))
	}
}

func TestCodexResponseTranslationPayloadsKeepOpenAIChatCompletionsOriginalRequest(t *testing.T) {
	originalPayload := []byte(`{"model":"test-model","messages":[{"role":"user","content":"hi"}]}`)
	requestPayload := []byte(`{"model":"test-model","input":"translated"}`)

	original, request := codexResponseTranslationPayloads(sdktranslator.FromString("openai"), originalPayload, requestPayload)

	if !bytes.Equal(original, originalPayload) {
		t.Fatalf("original payload = %q, want %q", string(original), string(originalPayload))
	}
	if !bytes.Equal(request, requestPayload) {
		t.Fatalf("request payload = %q, want %q", string(request), string(requestPayload))
	}
}
