package executor

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"testing"

	"github.com/MoeForge/moego/ojson"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/runtime/executor/helps"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v8/sdk/translator"
)

func TestDevinGPTFastModes(t *testing.T) {
	for _, family := range []string{"astra", "sol"} {
		for _, source := range []sdktranslator.Format{sdktranslator.FormatOpenAI, sdktranslator.FormatOpenAIResponse} {
			for _, stream := range []bool{false, true} {
				for _, tt := range []struct{ name, suffix, effort, tier, want string }{
					{"low priority", "", "low", "priority", "low-priority"},
					{"medium priority", "", "medium", "priority", "medium-priority"},
					{"high priority", "", "high", "priority", "high-priority"},
					{"xhigh priority", "", "xhigh", "priority", "xhigh-priority"},
					{"max priority", "", "max", "priority", "max-priority"},
					{"priority default effort", "", "", "priority", "medium-priority"},
					{"ordinary request", "", "high", "", "high"},
					{"explicit default", "", "high", "default", "high"},
					{"automatic tier", "", "high", "auto", "high"},
					{"fast suffix keeps body effort", "(fast)", "high", "", "high-priority"},
					{"fast suffix default effort", "(fast)", "", "", "medium-priority"},
					{"compound suffix overrides effort", "(high-fast)", "low", "", "high-priority"},
					{"effort suffix keeps priority", "(high)", "low", "priority", "high-priority"},
					{"body fast", "", "fast", "", "medium-priority"},
					{"body compound fast", "", "high-fast", "", "high-priority"},
					{"suffix changes only effort", "(low)", "high-fast", "", "low-priority"},
					{"explicit fast beats default tier", "(high-fast)", "low", "default", "high-priority"},
					{"none uses default", "", "none", "priority", "medium-priority"},
					{"native priority UID", "-high-priority", "low", "", "high-priority"},
					{"native fast alias", "-high-fast", "low", "", "high-priority"},
					{"native effort with priority", "-xhigh", "low", "priority", "xhigh-priority"},
					{"native none uses supported default", "-none-fast", "high", "", "medium-priority"},
					{"native minimal clamps to low", "-minimal-priority", "high", "", "low-priority"},
				} {
					t.Run(fmt.Sprintf("%s/%s/stream=%t/%s", family, source, stream, tt.name), func(t *testing.T) {
						model := "devin/gpt-6-2-" + family + tt.suffix
						payload := devinFastRequest(t, source, model, tt.effort, tt.tier)
						wire, errExecute := executeDevinFastRequest(t, source, stream, &config.Config{}, model, payload)
						if errExecute != nil {
							t.Fatal(errExecute)
						}
						want := "gpt-6-2-" + family + "-" + tt.want
						if got := ojson.GetBytes(wire, "model").String(); got != want {
							t.Fatalf("upstream model = %q, want %q", got, want)
						}
					})
				}
			}
		}
	}
}

func TestDevinGPTFastRejectsUnsupportedRequests(t *testing.T) {
	for _, source := range []sdktranslator.Format{sdktranslator.FormatOpenAI, sdktranslator.FormatOpenAIResponse} {
		for _, stream := range []bool{false, true} {
			for _, tt := range []struct{ model, tier string }{
				{"devin/gpt-6-1-sol", "priority"},
				{"devin/gpt-6-1-sol(fast)", ""},
				{"devin/gpt-6-2-astra", "flex"},
			} {
				t.Run(fmt.Sprintf("%s/stream=%t/%s/%s", source, stream, tt.model, tt.tier), func(t *testing.T) {
					payload := devinFastRequest(t, source, tt.model, "high", tt.tier)
					wire, errExecute := executeDevinFastRequest(t, source, stream, &config.Config{}, tt.model, payload)
					var status cliproxyexecutor.StatusError
					var scoped cliproxyexecutor.RequestScopedError
					if !errors.As(errExecute, &status) || status.StatusCode() != http.StatusBadRequest ||
						!errors.As(errExecute, &scoped) || !scoped.IsRequestScoped() {
						t.Fatalf("error = %v, want request-scoped 400", errExecute)
					}
					if len(wire) != 0 {
						t.Fatal("unsupported Fast request reached the upstream")
					}
				})
			}
		}
	}
}

func TestDevinGPTFastPayloadRulesRemainFinal(t *testing.T) {
	const model = "devin/gpt-6-2-astra"
	for _, source := range []sdktranslator.Format{sdktranslator.FormatOpenAI, sdktranslator.FormatOpenAIResponse} {
		for _, stream := range []bool{false, true} {
			for _, tt := range []struct{ name, configured, want string }{
				{"operator selects ordinary UID", "gpt-6-2-astra-low", "gpt-6-2-astra-low"},
				{"operator selects priority UID", "gpt-6-2-sol-max-priority", "gpt-6-2-sol-max-priority"},
				{"operator filters model", "", ""},
			} {
				t.Run(fmt.Sprintf("%s/stream=%t/%s", source, stream, tt.name), func(t *testing.T) {
					models := []config.PayloadModelRule{{Name: model, Protocol: "devin"}}
					cfg := &config.Config{}
					if tt.configured == "" {
						cfg.Payload.Filter = []config.PayloadFilterRule{{Models: models, Params: []string{"model"}}}
					} else {
						cfg.Payload.Override = []config.PayloadRule{{Models: models, Params: map[string]any{"model": tt.configured}}}
					}
					payload := devinFastRequest(t, source, model, "high", "priority")
					wire, errExecute := executeDevinFastRequest(t, source, stream, cfg, model, payload)
					if errExecute != nil {
						t.Fatal(errExecute)
					}
					if got := ojson.GetBytes(wire, "model").String(); got != tt.want {
						t.Fatalf("final model = %q, want %q", got, tt.want)
					}
				})
			}
		}
	}
}

func TestDevinGPTFastDiagnosticsFollowPayloadOverride(t *testing.T) {
	const model = "devin/gpt-6-2-astra"
	const configured = "gpt-6-2-astra-low"
	cfg := &config.Config{Payload: config.PayloadConfig{Override: []config.PayloadRule{{
		Models: []config.PayloadModelRule{{Name: model, Protocol: "devin"}}, Params: map[string]any{"model": configured},
	}}}}
	executor := NewDevinExecutor(cfg)
	payload := devinFastRequest(t, sdktranslator.FormatOpenAIResponse, model, "high", "priority")
	auth := &cliproxyauth.Auth{Provider: "devin", Attributes: map[string]string{"api_key": "test-key"}}
	req, selected, logged, errPrepare := executor.prepareDevinHTTPRequest(context.Background(), auth,
		cliproxyexecutor.Request{Model: model, Payload: payload}, cliproxyexecutor.Options{SourceFormat: sdktranslator.FormatOpenAIResponse})
	if errPrepare != nil {
		t.Fatal(errPrepare)
	}
	defer func() {
		if errClose := req.Body.Close(); errClose != nil {
			t.Fatal(errClose)
		}
	}()
	if selected != configured || ojson.GetBytes(logged, "model").String() != configured {
		t.Fatalf("reported model %q and log %s must reflect final model %q", selected, logged, configured)
	}
}

func devinFastRequest(t *testing.T, source sdktranslator.Format, model, effort, tier string) []byte {
	t.Helper()
	doc := ojson.NewDocString(`{"input":"OK"}`)
	effortPath := "reasoning.effort"
	if source == sdktranslator.FormatOpenAI {
		doc = ojson.NewDocString(`{"messages":[{"role":"user","content":"OK"}]}`)
		effortPath = "reasoning_effort"
	}
	for key, value := range map[string]string{"model": model, effortPath: effort, "service_tier": tier} {
		if value != "" {
			if errSet := doc.Set(key, value); errSet != nil {
				t.Fatal(errSet)
			}
		}
	}
	return doc.Bytes()
}

func executeDevinFastRequest(t *testing.T, source sdktranslator.Format, stream bool, cfg *config.Config, model string, payload []byte) ([]byte, error) {
	t.Helper()
	var captured []byte
	transport := roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		_, proto, errRead := helps.ReadConnectFrame(req.Body)
		if errRead != nil {
			return nil, errRead
		}
		_, view, errDecode := helps.FinalizeDevinPayload(proto, func(body []byte) []byte { return body })
		if errDecode != nil {
			return nil, errDecode
		}
		captured = view
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/connect+proto"}}, Body: io.NopCloser(bytes.NewReader(devinSchemaCompletionFrames()))}, nil
	})
	ctx := context.WithValue(context.Background(), "cliproxy.roundtripper", transport)
	executor := NewDevinExecutor(cfg)
	auth := &cliproxyauth.Auth{ID: t.Name(), Provider: "devin", Attributes: map[string]string{"api_key": "test-key"}}
	req := cliproxyexecutor.Request{Model: model, Payload: payload}
	opts := cliproxyexecutor.Options{SourceFormat: source, OriginalRequest: payload, Stream: stream}
	original := bytes.Clone(payload)
	var errExecute error
	if stream {
		result, errStream := executor.ExecuteStream(ctx, auth, req, opts)
		errExecute = errStream
		if errStream == nil {
			for chunk := range result.Chunks {
				if chunk.Err != nil {
					errExecute = chunk.Err
				}
			}
		}
	} else {
		_, errExecute = executor.Execute(ctx, auth, req, opts)
	}
	if !bytes.Equal(payload, original) {
		t.Fatal("caller payload was modified")
	}
	if errExecute == nil && len(captured) == 0 {
		t.Fatal("no request reached the upstream transport")
	}
	return captured, errExecute
}
