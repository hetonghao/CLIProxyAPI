package executor

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/runtime/executor/helps"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	log "github.com/sirupsen/logrus"
)

func (s *codexWebsocketSession) configureConn(conn *websocket.Conn) {
	if s == nil || conn == nil {
		return
	}
	s.resetUpstreamDisconnectError(conn)
	conn.SetPingHandler(func(appData string) error {
		s.updateObservation(conn, func(o *codexWebsocketObservation) { o.lastPingRx = time.Now() })
		setCodexWebsocketReadDeadline(s, conn)
		s.writeMu.Lock()
		defer s.writeMu.Unlock()
		return conn.WriteControl(websocket.PongMessage, []byte(appData), time.Now().Add(10*time.Second))
	})
	conn.SetPongHandler(func(appData string) error {
		s.updateObservation(conn, func(o *codexWebsocketObservation) { o.lastPongRx = time.Now() })
		setCodexWebsocketReadDeadline(s, conn)
		s.signalPong(conn, appData)
		return nil
	})
	defaultCloseHandler := conn.CloseHandler()
	conn.SetCloseHandler(func(code int, text string) error {
		s.updateObservation(conn, func(o *codexWebsocketObservation) {
			o.closeCode, o.closeReason = code, helps.CloseReason(code)
		})
		s.setUpstreamDisconnectError(conn, &websocket.CloseError{Code: code, Text: text})
		return defaultCloseHandler(code, text)
	})
}

func configureCodexWebsocketLiveness(ctx context.Context, conn *websocket.Conn) {
	if conn == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	conn.SetPingHandler(func(appData string) error {
		setCodexWebsocketReadDeadlineForContext(ctx, nil, conn)
		return conn.WriteControl(websocket.PongMessage, []byte(appData), time.Now().Add(10*time.Second))
	})
	conn.SetPongHandler(func(string) error {
		setCodexWebsocketReadDeadlineForContext(ctx, nil, conn)
		return nil
	})
}

func setCodexWebsocketReadDeadlineForContext(ctx context.Context, sess *codexWebsocketSession, conn *websocket.Conn) {
	if conn == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	deadline := time.Now().Add(codexResponsesWebsocketIdleTimeout)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	_ = conn.SetReadDeadline(deadline)
	if sess != nil {
		sess.updateObservation(conn, func(o *codexWebsocketObservation) { o.readDeadline = deadline })
	}
}

func setCodexWebsocketReadDeadline(sess *codexWebsocketSession, conn *websocket.Conn) {
	if conn == nil {
		return
	}
	deadline := time.Now().Add(codexResponsesWebsocketIdleTimeout)
	_ = conn.SetReadDeadline(deadline)
	if sess != nil {
		sess.updateObservation(conn, func(o *codexWebsocketObservation) { o.readDeadline = deadline })
	}
}

func (s *codexWebsocketSession) signalPong(conn *websocket.Conn, appData string) {
	if s == nil || conn == nil {
		return
	}
	s.pongMu.Lock()
	if s.pongWaitConn == conn && s.pongWait != nil && s.pongWaitData == appData {
		wait := s.pongWait
		s.pongWaitConn = nil
		s.pongWait = nil
		s.pongWaitData = ""
		close(wait)
	}
	s.pongMu.Unlock()
}

func (s *codexWebsocketSession) clearPongWait(conn *websocket.Conn, wait chan struct{}) {
	if s == nil {
		return
	}
	s.pongMu.Lock()
	if s.pongWaitConn == conn && s.pongWait == wait {
		s.pongWaitConn = nil
		s.pongWait = nil
		s.pongWaitData = ""
	}
	s.pongMu.Unlock()
}

func (s *codexWebsocketSession) probeConnection(ctx context.Context, conn *websocket.Conn) error {
	if s == nil || conn == nil {
		return fmt.Errorf("codex websockets executor: websocket probe connection is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	wait := make(chan struct{})
	s.pongMu.Lock()
	s.pongWaitConn = conn
	s.pongWait = wait
	probeData := fmt.Sprintf("codex-probe-%d", time.Now().UnixNano())
	s.pongWaitData = probeData
	s.pongMu.Unlock()
	defer s.clearPongWait(conn, wait)

	s.updateObservation(conn, func(o *codexWebsocketObservation) { o.lastPingTx = time.Now() })
	probeDeadline := time.Now().Add(codexResponsesWebsocketProbeTimeout)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(probeDeadline) {
		probeDeadline = ctxDeadline
	}
	lockTimer := time.NewTimer(time.Until(probeDeadline))
	lockTicker := time.NewTicker(time.Millisecond)
	defer lockTimer.Stop()
	defer lockTicker.Stop()
	for !s.writeMu.TryLock() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-lockTimer.C:
			return fmt.Errorf("codex websockets executor: upstream websocket probe timed out waiting for write lock")
		case <-lockTicker.C:
		}
	}
	if time.Now().After(probeDeadline) {
		s.writeMu.Unlock()
		return fmt.Errorf("codex websockets executor: upstream websocket probe timed out waiting for write lock")
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		s.writeMu.Unlock()
		return ctxErr
	}
	errWrite := conn.WriteControl(websocket.PingMessage, []byte(probeData), probeDeadline)
	s.writeMu.Unlock()
	if errWrite != nil {
		return errWrite
	}
	remaining := time.Until(probeDeadline)
	if remaining <= 0 {
		return fmt.Errorf("codex websockets executor: upstream websocket probe timed out waiting for Pong")
	}
	timer := time.NewTimer(remaining)
	defer timer.Stop()
	select {
	case <-wait:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return fmt.Errorf("codex websockets executor: upstream websocket probe timed out waiting for Pong")
	}
}

func (e *CodexWebsocketsExecutor) ensureUpstreamConn(ctx context.Context, auth *cliproxyauth.Auth, sess *codexWebsocketSession, authID string, wsURL string, headers http.Header) (*websocket.Conn, *websocketConnectionCloser, *http.Response, error) {
	if sess == nil {
		return e.dialCodexWebsocket(ctx, auth, wsURL, headers)
	}

	if staleConn, staleCloser, staleAuthID, staleWSURL, staleLifecycle := detachMismatchedWebsocketSessionConn(sess, authID, wsURL); staleConn != nil {
		logCodexWebsocketDisconnected(sess.sessionID, staleAuthID, staleWSURL, "target_changed", nil)
		if staleCloser != nil {
			if errClose := staleCloser.Close(); errClose != nil {
				log.Errorf("codex websockets executor: close stale websocket error: %v", errClose)
			}
		}
		if staleLifecycle != nil {
			staleLifecycle.End("target_changed")
		}
	}

	sess.connMu.Lock()
	conn := sess.conn
	closer := sess.connCloser
	readerConn := sess.readerConn
	sess.connMu.Unlock()
	if conn != nil {
		if readerConn != conn {
			sess.connMu.Lock()
			sess.readerConn = conn
			sess.connMu.Unlock()
			sess.configureConn(conn)
			go e.readUpstreamLoop(sess, conn)
		}
		return conn, closer, nil, nil
	}

	conn, closer, resp, errDial := e.dialCodexWebsocket(ctx, auth, wsURL, headers)
	if errDial != nil {
		return nil, closer, resp, errDial
	}

	sess.connMu.Lock()
	if sess.conn != nil {
		previous := sess.conn
		previousCloser := sess.connCloser
		sess.connMu.Unlock()
		if errClose := closer.Close(); errClose != nil {
			log.Errorf("codex websockets executor: close websocket error: %v", errClose)
		}
		return previous, previousCloser, nil, nil
	}
	sess.conn = conn
	sess.connCloser = closer
	sess.multiAgentV2OptimizedConn = nil
	sess.wsURL = wsURL
	sess.authID = authID
	sess.readerConn = conn
	sess.connMu.Unlock()

	responseTrace := ""
	if resp != nil {
		responseTrace = resp.Header.Get(websocketTraceHeader)
	}
	sess.recordConnection(conn, responseTrace, authID)
	sess.configureConn(conn)
	go e.readUpstreamLoop(sess, conn)
	logCodexWebsocketConnected(sess.sessionID, authID, wsURL)
	return conn, closer, resp, nil
}

func (e *CodexWebsocketsExecutor) readUpstreamLoop(sess *codexWebsocketSession, conn *websocket.Conn) {
	if e == nil || sess == nil || conn == nil {
		return
	}
	for {
		setCodexWebsocketReadDeadline(sess, conn)
		msgType, payload, errRead := conn.ReadMessage()
		if errRead != nil {
			invalidate := func() {
				e.invalidateUpstreamConn(sess, conn, "upstream_disconnected", errRead)
			}
			invalidated := false
			ch, done := sess.activeForConn(conn)
			if ch != nil {
				invalidated = sendTerminalWebsocketRead(ch, done, codexWebsocketRead{conn: conn, err: errRead}, invalidate)
				if sess.clearActive(conn, ch) {
					close(ch)
				}
			}
			if !invalidated {
				invalidate()
			}
			return
		}

		if msgType != websocket.TextMessage {
			if msgType == websocket.BinaryMessage {
				errBinary := fmt.Errorf("codex websockets executor: unexpected binary message")
				invalidate := func() {
					e.invalidateUpstreamConn(sess, conn, "unexpected_binary", errBinary)
				}
				invalidated := false
				ch, done := sess.activeForConn(conn)
				if ch != nil {
					invalidated = sendTerminalWebsocketRead(ch, done, codexWebsocketRead{conn: conn, err: errBinary}, invalidate)
					if sess.clearActive(conn, ch) {
						close(ch)
					}
				}
				if !invalidated {
					invalidate()
				}
				return
			}
			continue
		}
		sess.updateObservation(conn, func(o *codexWebsocketObservation) { o.lastApplicationRx = time.Now() })

		ch, done := sess.activeForConn(conn)
		if ch == nil {
			continue
		}
		sess.activeMu.Lock()
		queue := sess.activeQueue
		sess.activeMu.Unlock()
		if queue != nil {
			queue.add(len(payload))
		}
		event := codexWebsocketRead{conn: conn, msgType: msgType, payload: payload, queue: queue}
		select {
		case ch <- event:
		case <-done:
			if queue != nil {
				queue.remove(len(payload))
			}
		}
	}
}
