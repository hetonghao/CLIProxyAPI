package pluginhost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/registry"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	coreexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

type staticEnvelopePluginClient struct {
	raw []byte
}

func (c staticEnvelopePluginClient) Call(context.Context, string, []byte) ([]byte, error) {
	return c.raw, nil
}

func (c staticEnvelopePluginClient) Shutdown() {}

func TestDecodeEnvelopeResultPreservesPluginHTTPStatus(t *testing.T) {
	_, errDecode := decodeEnvelopeResult[rpcEmptyResponse](pluginabi.Envelope{
		OK: false,
		Error: &pluginabi.Error{
			Code:       "plugin_error",
			Message:    "license required",
			HTTPStatus: http.StatusForbidden,
		},
	})
	if errDecode == nil {
		t.Fatal("decodeEnvelopeResult returned nil error")
	}
	if got := errDecode.Error(); got != "license required" {
		t.Fatalf("error = %q, want license required", got)
	}
	statusProvider, ok := errDecode.(interface{ StatusCode() int })
	if !ok {
		t.Fatalf("error %T does not expose StatusCode", errDecode)
	}
	if got := statusProvider.StatusCode(); got != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", got, http.StatusForbidden)
	}
}

func TestCallPluginReturnsPluginErrorWithoutMethodWrapper(t *testing.T) {
	raw, errMarshal := json.Marshal(pluginabi.Envelope{
		OK: false,
		Error: &pluginabi.Error{
			Code:       "plugin_error",
			Message:    "license required",
			HTTPStatus: http.StatusForbidden,
		},
	})
	if errMarshal != nil {
		t.Fatalf("marshal envelope: %v", errMarshal)
	}
	_, errCall := callPlugin[rpcEmptyResponse](context.Background(), staticEnvelopePluginClient{raw: raw}, pluginabi.MethodExecutorExecuteStream, rpcEmptyResponse{})
	if errCall == nil {
		t.Fatal("callPlugin returned nil error")
	}
	if got := errCall.Error(); got != "license required" {
		t.Fatalf("error = %q, want license required", got)
	}
	statusProvider, ok := errCall.(interface{ StatusCode() int })
	if !ok {
		t.Fatalf("error %T does not expose StatusCode", errCall)
	}
	if got := statusProvider.StatusCode(); got != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", got, http.StatusForbidden)
	}
}

func TestIsPluginErrorEnvelopeAcceptsNonzeroReturnEnvelope(t *testing.T) {
	raw := marshalRPCError("plugin_error", "upstream failed")
	if !isPluginErrorEnvelope(raw) {
		t.Fatalf("isPluginErrorEnvelope(%s) = false, want true", raw)
	}
	if isPluginErrorEnvelope([]byte(`not json`)) {
		t.Fatal("isPluginErrorEnvelope accepted invalid JSON")
	}
}

func TestCallPluginPreservesStatusFromNewErrorEnvelope(t *testing.T) {
	raw, errMarshal := pluginabi.NewErrorEnvelope("insufficient_quota", "plan limit reached", http.StatusForbidden)
	if errMarshal != nil {
		t.Fatalf("NewErrorEnvelope() error = %v", errMarshal)
	}
	_, errCall := callPlugin[rpcEmptyResponse](context.Background(), staticEnvelopePluginClient{raw: raw}, pluginabi.MethodExecutorExecute, rpcEmptyResponse{})
	if errCall == nil {
		t.Fatal("callPlugin returned nil error")
	}
	if got := errCall.Error(); got != "plan limit reached" {
		t.Fatalf("error = %q, want plan limit reached", got)
	}
	statusProvider, ok := errCall.(interface{ StatusCode() int })
	if !ok {
		t.Fatalf("error %T does not expose StatusCode", errCall)
	}
	if got := statusProvider.StatusCode(); got != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", got, http.StatusForbidden)
	}
}

type countingEnvelopePluginClient struct {
	staticEnvelopePluginClient
	calls int
}

func (c *countingEnvelopePluginClient) Call(ctx context.Context, method string, payload []byte) ([]byte, error) {
	c.calls++
	return c.staticEnvelopePluginClient.Call(ctx, method, payload)
}

func newSchedulerStopRetryFixture(t *testing.T, raw []byte, provider, model, authID string) (*coreauth.Manager, *countingEnvelopePluginClient, *coreauth.Auth) {
	t.Helper()
	client := &countingEnvelopePluginClient{staticEnvelopePluginClient: staticEnvelopePluginClient{raw: raw}}
	adapter := &rpcPluginAdapter{id: "admission-test", client: client}
	host := newHostWithRecords(capabilityRecord{
		id:     "admission-test",
		plugin: pluginapi.Plugin{Capabilities: pluginapi.Capabilities{Scheduler: adapter}},
	})
	manager := coreauth.NewManager(nil, nil, nil)
	manager.RegisterExecutor(&fakeProviderExecutor{provider: provider})
	manager.SetPluginScheduler(host)
	manager.SetRetryConfig(2, 0, 1)
	registered, errRegister := manager.Register(context.Background(), &coreauth.Auth{ID: authID, Provider: provider, Status: coreauth.StatusActive})
	if errRegister != nil {
		t.Fatalf("Register(%s) error = %v", authID, errRegister)
	}
	reg := registry.GetGlobalRegistry()
	reg.RegisterClient(authID, provider, []*registry.ModelInfo{{ID: model}})
	t.Cleanup(func() { reg.UnregisterClient(authID) })
	return manager, client, registered
}

func TestPluginSchedulerStopRetryEndsRequestRetries(t *testing.T) {
	cases := []struct {
		name       string
		stream     bool
		raw        string
		wantCalls  int
		wantStatus int
	}{
		{name: "429-stop-retry", raw: `{"ok":false,"error":{"code":"provider_rate_limit_exceeded","message":"local admission exhausted","http_status":429,"stop_retry":true}}`, wantCalls: 1, wantStatus: http.StatusTooManyRequests},
		{name: "429-omitted", raw: `{"ok":false,"error":{"code":"provider_rate_limit_exceeded","message":"local admission exhausted","http_status":429}}`, wantCalls: 3, wantStatus: http.StatusTooManyRequests},
		{name: "429-false", raw: `{"ok":false,"error":{"code":"provider_rate_limit_exceeded","message":"local admission exhausted","http_status":429,"stop_retry":false}}`, wantCalls: 3, wantStatus: http.StatusTooManyRequests},
		{name: "503-stop-retry", raw: `{"ok":false,"error":{"code":"provider_rate_limiter_stopped","message":"local admission stopped","http_status":503,"stop_retry":true}}`, wantCalls: 1, wantStatus: http.StatusServiceUnavailable},
		{name: "503-omitted", raw: `{"ok":false,"error":{"code":"provider_rate_limiter_stopped","message":"local admission stopped","http_status":503}}`, wantCalls: 3, wantStatus: http.StatusServiceUnavailable},
		{name: "429-stop-retry-stream", stream: true, raw: `{"ok":false,"error":{"code":"provider_rate_limit_exceeded","message":"local admission exhausted","http_status":429,"stop_retry":true}}`, wantCalls: 1, wantStatus: http.StatusTooManyRequests},
		{name: "429-omitted-stream", stream: true, raw: `{"ok":false,"error":{"code":"provider_rate_limit_exceeded","message":"local admission exhausted","http_status":429}}`, wantCalls: 3, wantStatus: http.StatusTooManyRequests},
		{name: "429-false-stream", stream: true, raw: `{"ok":false,"error":{"code":"provider_rate_limit_exceeded","message":"local admission exhausted","http_status":429,"stop_retry":false}}`, wantCalls: 3, wantStatus: http.StatusTooManyRequests},
		{name: "503-omitted-stream", stream: true, raw: `{"ok":false,"error":{"code":"provider_rate_limiter_stopped","message":"local admission stopped","http_status":503}}`, wantCalls: 3, wantStatus: http.StatusServiceUnavailable},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			manager, client, registered := newSchedulerStopRetryFixture(t, []byte(tc.raw), "gemini", "stop-retry-model", fmt.Sprintf("stop-retry-auth-%d", i))
			opts := coreexecutor.Options{Metadata: map[string]any{}}
			req := coreexecutor.Request{Model: "stop-retry-model"}
			var err error
			if tc.stream {
				_, err = manager.ExecuteStream(context.Background(), []string{"gemini"}, req, opts)
			} else {
				_, err = manager.Execute(context.Background(), []string{"gemini"}, req, opts)
			}
			if err == nil {
				t.Fatal("expected scheduler rejection error")
			}
			if client.calls != tc.wantCalls {
				t.Fatalf("scheduler calls = %d, want %d", client.calls, tc.wantCalls)
			}
			var sc interface{ StatusCode() int }
			if !errors.As(err, &sc) || sc.StatusCode() != tc.wantStatus {
				t.Fatalf("error status = %v, want %d", err, tc.wantStatus)
			}
			if tc.wantCalls == 1 {
				var stop interface{ IsRequestStop() bool }
				if !errors.As(err, &stop) || !stop.IsRequestStop() {
					t.Fatal("returned error does not preserve IsRequestStop")
				}
			}
			current, ok := manager.GetByID(registered.ID)
			if !ok {
				t.Fatal("registered auth missing")
			}
			if current.Unavailable {
				t.Fatal("scheduler rejection marked auth unavailable")
			}
			if current.LastError != nil {
				t.Fatalf("scheduler rejection set auth LastError: %+v", current.LastError)
			}
			if !current.NextRetryAfter.IsZero() {
				t.Fatal("scheduler rejection set auth NextRetryAfter")
			}
		})
	}
}

func TestMarshalRPCErrorPreservesHTTPStatus(t *testing.T) {
	for _, status := range []int{http.StatusTooManyRequests, http.StatusServiceUnavailable} {
		raw := marshalRPCError("host_call_failed", "synthetic", status)
		var env pluginabi.Envelope
		if errUnmarshal := json.Unmarshal(raw, &env); errUnmarshal != nil {
			t.Fatalf("unmarshal envelope: %v", errUnmarshal)
		}
		if env.OK {
			t.Fatal("expected envelope OK=false")
		}
		if env.Error == nil {
			t.Fatal("expected non-nil Error in envelope")
		}
		if env.Error.HTTPStatus != status {
			t.Fatalf("HTTPStatus = %d, want %d", env.Error.HTTPStatus, status)
		}
		_, errDecode := decodeEnvelopeResult[rpcEmptyResponse](env)
		if errDecode == nil {
			t.Fatal("expected decode error")
		}
		statusProvider, ok := errDecode.(interface{ StatusCode() int })
		if !ok {
			t.Fatalf("decoded error does not expose StatusCode: %T", errDecode)
		}
		if got := statusProvider.StatusCode(); got != status {
			t.Fatalf("StatusCode = %d, want %d", got, status)
		}
	}
}
