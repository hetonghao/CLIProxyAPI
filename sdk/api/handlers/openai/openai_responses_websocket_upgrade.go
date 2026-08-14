package openai

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/misc"
	log "github.com/sirupsen/logrus"
)

const wsTraceHeader = "X-AI-Cove-WS-Trace"

var responsesWebsocketUpgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

var generateResponsesWebsocketTrace = misc.GenerateRandomState

func upgradeResponsesWebsocket(c *gin.Context) (*websocket.Conn, string, error) {
	serverTrace, errTrace := generateResponsesWebsocketTrace()
	if errTrace != nil {
		log.Error("responses websocket: server trace generation failed")
		c.AbortWithStatus(http.StatusInternalServerError)
		return nil, "", errTrace
	}
	upgradeHeaders := websocketUpgradeHeaders(c.Request)
	upgradeHeaders.Set(wsTraceHeader, serverTrace)
	conn, err := responsesWebsocketUpgrader.Upgrade(c.Writer, c.Request, upgradeHeaders)
	if err != nil {
		return nil, serverTrace, err
	}
	return conn, serverTrace, nil
}

func websocketUpgradeHeaders(req *http.Request) http.Header {
	headers := http.Header{}
	if req == nil {
		return headers
	}

	// Keep the same sticky turn-state across reconnects when provided by the client.
	turnState := strings.TrimSpace(req.Header.Get(wsTurnStateHeader))
	if turnState != "" {
		headers.Set(wsTurnStateHeader, turnState)
	}
	return headers
}
