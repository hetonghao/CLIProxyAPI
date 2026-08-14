package openai

import (
	"errors"
	"maps"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	internalconfig "github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/registry"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/api/handlers"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executionregistry"
	coreexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	sdkconfig "github.com/router-for-me/CLIProxyAPI/v7/sdk/config"
	log "github.com/sirupsen/logrus"
	logtest "github.com/sirupsen/logrus/hooks/test"
	"github.com/tidwall/gjson"
)

func TestResponsesWebsocketHandshakeEmitsIndependentServerTrace(t *testing.T) {
	gin.SetMode(gin.TestMode)
	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, nil)
	handler := NewOpenAIResponsesAPIHandler(base)
	router := gin.New()
	router.GET("/v1/responses/ws", handler.ResponsesWebsocket)
	server := httptest.NewServer(router)
	defer server.Close()

	dial := func(clientTrace string) string {
		headers := http.Header{}
		headers.Set("X-AI-Cove-WS-Trace", clientTrace)
		url := "ws" + strings.TrimPrefix(server.URL, "http") + "/v1/responses/ws"
		conn, response, errDial := websocket.DefaultDialer.Dial(url, headers)
		if errDial != nil {
			t.Fatalf("dial websocket: %v", errDial)
		}
		if errClose := conn.Close(); errClose != nil {
			t.Fatalf("close websocket: %v", errClose)
		}
		return response.Header.Get("X-AI-Cove-WS-Trace")
	}

	first := dial("client-controlled-trace")
	second := dial("another-client-controlled-trace")
	if first == "" || second == "" {
		t.Fatalf("server traces = %q, %q; want non-empty 101 response headers", first, second)
	}
	if first == second {
		t.Fatalf("server reused websocket trace %q across accepted upgrades", first)
	}
	if !regexp.MustCompile(`^[0-9a-f]{32}$`).MatchString(first) || !regexp.MustCompile(`^[0-9a-f]{32}$`).MatchString(second) {
		t.Fatalf("server traces = %q, %q; want 32 lowercase hex characters", first, second)
	}
	if first == "client-controlled-trace" || second == "another-client-controlled-trace" {
		t.Fatal("server echoed a client-controlled websocket trace")
	}
}

func TestResponsesWebsocketTraceGenerationFailureLogsStableError(t *testing.T) {
	previousGenerator := generateResponsesWebsocketTrace
	generateResponsesWebsocketTrace = func() (string, error) {
		return "", errors.New("test trace generation failure")
	}
	t.Cleanup(func() { generateResponsesWebsocketTrace = previousGenerator })

	previousLevel := log.GetLevel()
	log.SetLevel(log.ErrorLevel)
	hook := logtest.NewLocal(log.StandardLogger())
	t.Cleanup(func() {
		hook.Reset()
		log.SetLevel(previousLevel)
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, nil)
	handler := NewOpenAIResponsesAPIHandler(base)
	router := gin.New()
	router.GET("/v1/responses/ws", handler.ResponsesWebsocket)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/v1/responses/ws", nil)
	context, _ := gin.CreateTestContext(recorder)
	context.Request = request

	handler.ResponsesWebsocket(context)

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusInternalServerError)
	}
	entries := hook.AllEntries()
	if len(entries) != 1 || entries[0].Message != "responses websocket: server trace generation failed" {
		t.Fatalf("log entries = %#v, want one stable trace-generation error", entries)
	}
}

func TestResponsesWebsocketHomeSelectedAuthCallbackPinsAndReusesFirstSelection(t *testing.T) {
	gin.SetMode(gin.TestMode)

	dispatcher := &homeResponsesWebsocketDispatcher{}
	executor := &homeResponsesWebsocketExecutor{}
	manager := coreauth.NewManager(nil, nil, nil)
	manager.SetConfig(&internalconfig.Config{Home: internalconfig.HomeConfig{Enabled: true}})
	manager.PublishHomeDispatch(dispatcher, executionregistry.New(), 1)
	manager.RegisterExecutor(executor)
	registry.GetGlobalRegistry().RegisterClient("home-responses-websocket-auth", "codex", []*registry.ModelInfo{{ID: "gpt-5.4"}})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIResponsesAPIHandler(base)
	router := gin.New()
	router.GET("/v1/responses/ws", h.ResponsesWebsocket)
	server := httptest.NewServer(router)
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/v1/responses/ws"
	conn, response, errDial := websocket.DefaultDialer.Dial(wsURL, nil)
	if errDial != nil {
		t.Fatalf("dial websocket: %v", errDial)
	}
	serverTrace := response.Header.Get(wsTraceHeader)
	if serverTrace == "" {
		t.Fatal("ResponsesWebsocket handshake did not return a server trace")
	}
	defer func() {
		if errClose := conn.Close(); errClose != nil {
			t.Errorf("close websocket: %v", errClose)
		}
	}()

	requests := []string{
		`{"type":"response.create","model":"gpt-5.4","input":[]}`,
		`{"type":"response.create","model":"gpt-5.4","input":[]}`,
	}
	for index, request := range requests {
		if errWrite := conn.WriteMessage(websocket.TextMessage, []byte(request)); errWrite != nil {
			t.Fatalf("write websocket request %d: %v", index+1, errWrite)
		}
		_, payload, errRead := conn.ReadMessage()
		if errRead != nil {
			t.Fatalf("read websocket response %d: %v", index+1, errRead)
		}
		if got := gjson.GetBytes(payload, "type").String(); got != wsEventTypeCompleted {
			t.Fatalf("response %d type = %q, want %q: %s", index+1, got, wsEventTypeCompleted, payload)
		}
		if index == 0 {
			executor.mu.Lock()
			firstMetadata := maps.Clone(executor.metadata[0])
			executor.mu.Unlock()
			sessionID, _ := firstMetadata[coreexecutor.ExecutionSessionMetadataKey].(string)
			if _, ok := manager.GetExecutionSessionAuthByID(sessionID, "home-responses-websocket-auth"); !ok {
				t.Fatal("first selected-auth callback did not stage the session runtime auth")
			}
		}
	}

	executor.mu.Lock()
	metadata := append([]map[string]any(nil), executor.metadata...)
	executor.mu.Unlock()
	if len(metadata) != 2 {
		t.Fatalf("executor metadata calls = %d, want 2", len(metadata))
	}
	if got := metadata[1][coreexecutor.PinnedAuthMetadataKey]; got != "home-responses-websocket-auth" {
		t.Fatalf("second turn pinned auth metadata = %#v, want home selected auth (first metadata: %#v, second metadata: %#v)", got, metadata[0], metadata[1])
	}
	for index, item := range metadata {
		if got := item[coreexecutor.WebsocketTraceMetadataKey]; got != serverTrace {
			t.Fatalf("turn %d websocket trace metadata = %#v, want handshake trace %q", index+1, got, serverTrace)
		}
	}
	if got := dispatcher.calls.Load(); got != 1 {
		t.Fatalf("Home RPOP calls = %d, want 1 after selected-auth callback pin", got)
	}
	if got := executor.calls.Load(); got != 2 {
		t.Fatalf("executor calls = %d, want 2", got)
	}
}
