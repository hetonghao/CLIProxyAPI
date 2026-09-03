package executor

import (
	"testing"

	"github.com/gorilla/websocket"
)

func TestCodexWebsocketSessionMarksTerminalEvents(t *testing.T) {
	for _, eventType := range []string{
		"response.completed",
		"response.done",
		"response.incomplete",
		"response.failed",
		"error",
	} {
		t.Run(eventType, func(t *testing.T) {
			conn := new(websocket.Conn)
			sess := &codexWebsocketSession{}
			sess.observation.conn = conn

			sess.markTerminal(conn, eventType)

			if !sess.observation.terminalSeen {
				t.Fatalf("terminal_seen = false for %s", eventType)
			}
		})
	}
}
