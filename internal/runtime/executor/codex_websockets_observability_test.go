package executor

import (
	"errors"
	"testing"

	"github.com/gorilla/websocket"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/runtime/executor/helps"
	log "github.com/sirupsen/logrus"
	logtest "github.com/sirupsen/logrus/hooks/test"
)

type failingTraceGenerator struct{}

func TestCodexWebsocketObservationUsesStableMessage(t *testing.T) {
	previousLevel := log.GetLevel()
	log.SetLevel(log.InfoLevel)
	hook := logtest.NewLocal(log.StandardLogger())
	t.Cleanup(func() {
		hook.Reset()
		log.SetLevel(previousLevel)
	})

	logCodexWebsocketObservation("cleanup", &codexWebsocketSession{sessionID: "stable-message-session"}, false)

	entries := hook.AllEntries()
	if len(entries) != 1 {
		t.Fatalf("diagnostic entries = %d, want 1", len(entries))
	}
	if entries[0].Message != "codex websockets: observation" {
		t.Fatalf("message = %q, want stable observation message", entries[0].Message)
	}
	if entries[0].Data["event"] != "cleanup" {
		t.Fatalf("event field = %#v, want cleanup", entries[0].Data["event"])
	}
}

func TestWebsocketTraceFromHandshakeReturnsRandomError(t *testing.T) {
	if _, errTrace := helps.WebsocketTraceFromHandshake("", failingTraceGenerator{}.generate); errTrace == nil {
		t.Fatal("WebsocketTraceFromHandshake() error = nil, want random-source error")
	}
}

func TestRecordConnectionLogsLocalTraceGenerationFailure(t *testing.T) {
	previousLevel := log.GetLevel()
	log.SetLevel(log.WarnLevel)
	hook := logtest.NewLocal(log.StandardLogger())
	t.Cleanup(func() {
		hook.Reset()
		log.SetLevel(previousLevel)
	})

	sess := &codexWebsocketSession{traceGenerator: failingTraceGenerator{}.generate}
	sess.recordConnection(&websocket.Conn{}, "", "")

	entries := hook.AllEntries()
	if len(entries) != 1 {
		t.Fatalf("diagnostic entries = %d, want 1", len(entries))
	}
	if entries[0].Message != "codex websockets: local upstream trace generation failed" {
		t.Fatalf("message = %q, want stable trace-generation failure message", entries[0].Message)
	}
	if entries[0].Data["error"] == nil {
		t.Fatal("trace-generation failure log omitted error field")
	}
	if entries[0].Level != log.WarnLevel {
		t.Fatalf("trace-generation failure level = %s, want warning", entries[0].Level)
	}
}

func (failingTraceGenerator) generate() (string, error) {
	return "", errors.New("random source unavailable")
}

func TestCodexWebsocketObservationOmitsAuthDigestWhenAuthIDAbsent(t *testing.T) {
	previousLevel := log.GetLevel()
	log.SetLevel(log.InfoLevel)
	hook := logtest.NewLocal(log.StandardLogger())
	t.Cleanup(func() {
		hook.Reset()
		log.SetLevel(previousLevel)
	})

	sess := &codexWebsocketSession{sessionID: "anonymous-session"}
	logCodexWebsocketObservation("cleanup", sess, false)

	entries := hook.AllEntries()
	if len(entries) != 1 {
		t.Fatalf("diagnostic entries = %d, want 1", len(entries))
	}
	if _, ok := entries[0].Data["auth_digest"]; ok {
		t.Fatalf("auth_digest = %#v, want omitted when auth ID is absent", entries[0].Data["auth_digest"])
	}
}

func TestCodexWebsocketCleanupWithoutPhysicalConnectionDoesNotIncrementClosed(t *testing.T) {
	previousLevel := log.GetLevel()
	log.SetLevel(log.InfoLevel)
	hook := logtest.NewLocal(log.StandardLogger())
	t.Cleanup(func() {
		hook.Reset()
		log.SetLevel(previousLevel)
	})

	exec := NewCodexWebsocketsExecutor(nil)
	sessionID := "cleanup-without-connection"
	if exec.UpstreamDisconnectChan(sessionID) == nil {
		t.Fatal("UpstreamDisconnectChan returned nil")
	}
	opened := globalCodexWebsocketRuntimeMetrics.opened.Load()
	closed := globalCodexWebsocketRuntimeMetrics.closed.Load()
	cleanupCount := globalCodexWebsocketRuntimeMetrics.cleanup.Load()

	exec.CloseExecutionSession(sessionID)

	if got := globalCodexWebsocketRuntimeMetrics.opened.Load(); got != opened {
		t.Fatalf("opened = %d, want unchanged %d", got, opened)
	}
	if got := globalCodexWebsocketRuntimeMetrics.closed.Load(); got != closed {
		t.Fatalf("closed = %d, want unchanged %d", got, closed)
	}
	if got := globalCodexWebsocketRuntimeMetrics.cleanup.Load(); got != cleanupCount+1 {
		t.Fatalf("cleanup = %d, want %d", got, cleanupCount+1)
	}
}
