package executor

import (
	"strings"

	"github.com/gorilla/websocket"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/runtime/executor/helps"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	log "github.com/sirupsen/logrus"
)

func (e *CodexWebsocketsExecutor) invalidateUpstreamConn(sess *codexWebsocketSession, conn *websocket.Conn, reason string, err error) {
	e.invalidateUpstreamConnWithNotify(sess, conn, reason, err, true)
}

func (e *CodexWebsocketsExecutor) invalidateUpstreamConnWithoutDisconnectNotify(sess *codexWebsocketSession, conn *websocket.Conn, reason string, err error) {
	e.invalidateUpstreamConnWithNotify(sess, conn, reason, err, false)
}

func (e *CodexWebsocketsExecutor) invalidateUpstreamConnForResponsesWebsocketError(sess *codexWebsocketSession, conn *websocket.Conn, reason string, err error) {
	if cliproxyexecutor.IsResponsesWebsocketCapacityRejected(err) {
		e.invalidateUpstreamConnWithoutDisconnectNotify(sess, conn, reason, err)
		return
	}
	e.invalidateUpstreamConn(sess, conn, reason, err)
}

func (e *CodexWebsocketsExecutor) invalidateUpstreamConnWithNotify(sess *codexWebsocketSession, conn *websocket.Conn, reason string, err error, notify bool) {
	if sess == nil || conn == nil {
		return
	}

	sess.connMu.Lock()
	current := sess.conn
	authID := sess.authID
	wsURL := sess.wsURL
	sessionID := sess.sessionID
	if current == nil || current != conn {
		sess.connMu.Unlock()
		return
	}
	lifecycle := sess.lifecycle
	closer := sess.connCloser
	sess.lifecycle = nil
	sess.lifecycleModel = ""
	sess.conn = nil
	sess.connCloser = nil
	sess.multiAgentV2OptimizedConn = nil
	if sess.readerConn == conn {
		sess.readerConn = nil
	}
	sess.connMu.Unlock()

	failureReason, networkErrorKind := helps.ObservationReason(reason), helps.NetworkErrorKind(err)
	sess.updateObservation(conn, func(o *codexWebsocketObservation) {
		if reason == "upstream_error" || reason == "terminal_failure" {
			o.terminalSeen = true
		}
		o.failureReason, o.networkErrorKind = failureReason, networkErrorKind
	})
	if sess.markCleanup(conn, reason) {
		logCodexWebsocketObservation("failure", sess, true)
	}
	logCodexWebsocketDisconnected(sessionID, authID, wsURL, reason, err)
	if notify {
		sess.notifyUpstreamDisconnect(err)
	}
	if closer != nil {
		if errClose := closer.Close(); errClose != nil {
			log.Errorf("codex websockets executor: close websocket error: %v", errClose)
		}
	}
	if lifecycle != nil {
		lifecycle.End(reason)
	}
}

func closeCodexWebsocketSession(sess *codexWebsocketSession, reason string) {
	if sess == nil {
		return
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "session_closed"
	}

	sess.connMu.Lock()
	conn := sess.conn
	authID := sess.authID
	wsURL := sess.wsURL
	lifecycle := sess.lifecycle
	closer := sess.connCloser
	sess.lifecycle = nil
	sess.lifecycleModel = ""
	sess.conn = nil
	sess.connCloser = nil
	sess.multiAgentV2OptimizedConn = nil
	if sess.readerConn == conn {
		sess.readerConn = nil
	}
	sessionID := sess.sessionID
	sess.connMu.Unlock()

	if sess.markCleanup(conn, reason) {
		logCodexWebsocketObservation("cleanup", sess, true)
	}
	if conn != nil {
		logCodexWebsocketDisconnected(sessionID, authID, wsURL, reason, nil)
		if closer != nil {
			if errClose := closer.Close(); errClose != nil {
				log.Errorf("codex websockets executor: close websocket error: %v", errClose)
			}
		}
	}
	if lifecycle != nil {
		lifecycle.End(reason)
	}
}
