package executor

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/runtime/executor/helps"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	log "github.com/sirupsen/logrus"
)

const websocketTraceHeader = "X-AI-Cove-WS-Trace"

type codexWebsocketSession struct {
	sessionID string

	reqMu sync.Mutex

	connMu                    sync.Mutex
	conn                      *websocket.Conn
	connCloser                *websocketConnectionCloser
	wsURL                     string
	authID                    string
	multiAgentV2OptimizedConn *websocket.Conn
	lifecycleBindMu           sync.Mutex
	lifecycle                 cliproxyexecutor.ExecutionLifecycle
	lifecycleModel            string

	writeMu sync.Mutex

	activeMu     sync.Mutex
	activeConn   *websocket.Conn
	activeCh     chan codexWebsocketRead
	activeQueue  *codexWebsocketReadQueue
	activeDone   <-chan struct{}
	activeCancel context.CancelFunc

	observationMu  sync.RWMutex
	observation    codexWebsocketObservation
	traceGenerator func() (string, error)

	readerConn *websocket.Conn

	upstreamDisconnectOnce    sync.Once
	upstreamDisconnectCh      chan error
	upstreamDisconnectErrMu   sync.RWMutex
	upstreamDisconnectErrConn *websocket.Conn
	upstreamDisconnectErr     error
}

type codexWebsocketRead struct {
	conn    *websocket.Conn
	msgType int
	payload []byte
	err     error
	queue   *codexWebsocketReadQueue
}

type codexWebsocketRuntimeMetrics struct {
	opened  atomic.Uint64
	closed  atomic.Uint64
	cleanup atomic.Uint64
}

var globalCodexWebsocketRuntimeMetrics codexWebsocketRuntimeMetrics

var websocketObservationKey, websocketObservationKeyErr = helps.NewWebsocketTrace()
var websocketObservationKeyErrorLogged atomic.Bool

type codexWebsocketReadQueue struct {
	messages atomic.Int64
	bytes    atomic.Int64
}

func (q *codexWebsocketReadQueue) add(size int) {
	q.messages.Add(1)
	q.bytes.Add(int64(size))
}

func (q *codexWebsocketReadQueue) remove(size int) {
	q.messages.Add(-1)
	q.bytes.Add(-int64(size))
}

type codexWebsocketObservation struct {
	downstreamTrace, upstreamTrace      string
	conn                                *websocket.Conn
	lastApplicationRx                   time.Time
	lastPingRx, lastPongRx              time.Time
	readDeadline                        time.Time
	downstreamOrdinal                   uint64
	upstreamGeneration, upstreamOrdinal uint64
	terminalSeen, cleanupAccounted      bool
	closeReason, failureReason          string
	closeCode                           int
	networkErrorKind                    string
	authDigest                          string
}

type codexWebsocketObservationSnapshot struct {
	downstreamTrace, upstreamTrace           string
	requestState, sessionDigest, authDigest  string
	lastApplicationRxAgeMS                   int64
	lastPingRxAgeMS, lastPingTxAgeMS         int64
	lastPongRxAgeMS                          int64
	readDeadlineRemainingMS                  int64
	downstreamOrdinal                        uint64
	upstreamGeneration, upstreamOrdinal      uint64
	terminalSeen, cleanupOrFailureAccounted  bool
	failureAccounted                         bool
	closeReason                              string
	closeCode                                int
	failureReason, networkErrorKind          string
	queueMessages, queueBytes, queueCapacity int64
}

func (s *codexWebsocketSession) setDownstreamTrace(value string) {
	if s == nil || !helps.ValidWebsocketTrace(value) {
		return
	}
	s.observationMu.Lock()
	s.observation.downstreamTrace = value
	s.observationMu.Unlock()
}

func (s *codexWebsocketSession) recordConnection(conn *websocket.Conn, responseHeader, authID string) {
	if s == nil || conn == nil {
		return
	}
	upstreamTrace, traceErr := helps.WebsocketTraceFromHandshake(responseHeader, s.traceGenerator)
	if traceErr != nil {
		log.WithError(traceErr).Warn("codex websockets: local upstream trace generation failed")
	}
	if websocketObservationKeyErr != nil && websocketObservationKeyErrorLogged.CompareAndSwap(false, true) {
		log.WithError(websocketObservationKeyErr).Warn("codex websockets: observation key generation failed")
	}
	s.observationMu.Lock()
	s.observation.conn = conn
	s.observation.upstreamTrace = upstreamTrace
	s.observation.authDigest = helps.ObservationDigestForDomain(websocketObservationKey, "auth", authID)
	s.observation.lastApplicationRx = time.Time{}
	s.observation.lastPingRx, s.observation.lastPongRx = time.Time{}, time.Time{}
	s.observation.readDeadline = time.Time{}
	s.observation.upstreamGeneration++
	s.observation.upstreamOrdinal = 0
	s.observation.terminalSeen = false
	s.observation.cleanupAccounted = false
	s.observation.closeReason, s.observation.failureReason = "", ""
	s.observation.closeCode, s.observation.networkErrorKind = 0, ""
	s.observationMu.Unlock()
	globalCodexWebsocketRuntimeMetrics.opened.Add(1)
}

func (s *codexWebsocketSession) beginRequest(conn *websocket.Conn) {
	s.updateObservation(conn, func(o *codexWebsocketObservation) {
		o.downstreamOrdinal++
		o.terminalSeen = false
		o.failureReason, o.networkErrorKind = "", ""
	})
}

func (s *codexWebsocketSession) commitRequest(conn *websocket.Conn) {
	if s == nil {
		return
	}
	s.updateObservation(conn, func(o *codexWebsocketObservation) { o.upstreamOrdinal++ })
	logCodexWebsocketObservation("accepted", s, false)
}

func (s *codexWebsocketSession) markTerminal(conn *websocket.Conn, eventType string) {
	if s == nil || (eventType != "response.completed" && eventType != "response.done" && eventType != "error") {
		return
	}
	s.updateObservation(conn, func(o *codexWebsocketObservation) { o.terminalSeen = true })
	logCodexWebsocketObservation("terminal", s, false)
}

func (s *codexWebsocketSession) updateObservation(conn *websocket.Conn, update func(*codexWebsocketObservation)) {
	if s == nil || conn == nil || update == nil {
		return
	}
	s.observationMu.Lock()
	if s.observation.conn == conn {
		update(&s.observation)
	}
	s.observationMu.Unlock()
}

func (s *codexWebsocketSession) markCleanup(conn *websocket.Conn, reason string) bool {
	if s == nil {
		return false
	}
	s.observationMu.Lock()
	defer s.observationMu.Unlock()
	if (conn != nil && s.observation.conn != conn) || s.observation.cleanupAccounted {
		return false
	}
	s.observation.cleanupAccounted = true
	if s.observation.upstreamGeneration > 0 {
		globalCodexWebsocketRuntimeMetrics.closed.Add(1)
	}
	if s.observation.closeReason == "" {
		s.observation.closeReason = helps.ObservationReason(reason)
	}
	globalCodexWebsocketRuntimeMetrics.cleanup.Add(1)
	return true
}

func (s *codexWebsocketSession) observationSnapshot() codexWebsocketObservationSnapshot {
	if s == nil {
		return codexWebsocketObservationSnapshot{}
	}
	s.observationMu.RLock()
	o := s.observation
	s.observationMu.RUnlock()
	s.activeMu.Lock()
	active := s.activeConn != nil && s.activeCh != nil
	queue := s.activeQueue
	queueCapacity := cap(s.activeCh)
	s.activeMu.Unlock()
	now := time.Now()
	var messages, bytes int64
	if queue != nil {
		messages, bytes = queue.messages.Load(), queue.bytes.Load()
	}
	requestState := "idle"
	if active {
		requestState = "active"
	}
	return codexWebsocketObservationSnapshot{
		downstreamTrace: o.downstreamTrace, upstreamTrace: o.upstreamTrace,
		requestState: requestState, sessionDigest: helps.ObservationDigestForDomain(websocketObservationKey, "session", s.sessionID), authDigest: o.authDigest,
		lastApplicationRxAgeMS:  helps.ObservationAgeMS(now, o.lastApplicationRx),
		lastPingRxAgeMS:         helps.ObservationAgeMS(now, o.lastPingRx),
		lastPingTxAgeMS:         -1,
		lastPongRxAgeMS:         helps.ObservationAgeMS(now, o.lastPongRx),
		readDeadlineRemainingMS: helps.ObservationRemainingMS(now, o.readDeadline),
		downstreamOrdinal:       o.downstreamOrdinal, upstreamGeneration: o.upstreamGeneration,
		upstreamOrdinal: o.upstreamOrdinal, terminalSeen: o.terminalSeen,
		cleanupOrFailureAccounted: o.cleanupAccounted || o.failureReason != "",
		failureAccounted:          o.failureReason != "", closeReason: o.closeReason, closeCode: o.closeCode,
		failureReason: o.failureReason, networkErrorKind: o.networkErrorKind,
		queueMessages: messages, queueBytes: bytes, queueCapacity: int64(queueCapacity),
	}
}

func logCodexWebsocketObservation(event string, sess *codexWebsocketSession, includeRuntime bool) {
	facts := sess.observationSnapshot()
	fields := log.Fields{
		"event": event, "session_digest": facts.sessionDigest, "downstream_trace": facts.downstreamTrace, "upstream_trace": facts.upstreamTrace,
		"request_state": facts.requestState, "last_application_rx_age_ms": facts.lastApplicationRxAgeMS, "last_ping_rx_age_ms": facts.lastPingRxAgeMS, "last_ping_tx_age_ms": facts.lastPingTxAgeMS,
		"last_pong_rx_age_ms": facts.lastPongRxAgeMS, "read_deadline_remaining_ms": facts.readDeadlineRemainingMS,
		"downstream_ordinal": facts.downstreamOrdinal, "upstream_generation": facts.upstreamGeneration, "upstream_ordinal": facts.upstreamOrdinal,
		"terminal_seen": facts.terminalSeen, "cleanup_or_failure_accounted": facts.cleanupOrFailureAccounted, "failure_accounted": facts.failureAccounted,
		"close_reason": facts.closeReason, "close_code": facts.closeCode, "failure_reason": facts.failureReason, "network_error_kind": facts.networkErrorKind,
		"active_channel_messages": facts.queueMessages, "active_channel_bytes": facts.queueBytes, "active_channel_capacity": facts.queueCapacity,
	}
	if facts.authDigest != "" {
		fields["auth_digest"] = facts.authDigest
	}
	if includeRuntime {
		for key, value := range helps.CaptureCodexWebsocketRuntimeSnapshot(
			globalCodexWebsocketRuntimeMetrics.opened.Load(),
			globalCodexWebsocketRuntimeMetrics.closed.Load(),
			globalCodexWebsocketRuntimeMetrics.cleanup.Load(),
		) {
			fields[key] = value
		}
	}
	log.WithFields(fields).Info("codex websockets: observation")
}
