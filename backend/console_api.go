package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/coder/websocket"
	"github.com/gin-gonic/gin"
)

// consoleControl is a JSON text frame: resize from the browser, status from us
type consoleControl struct {
	Type    string `json:"type"` // resize, status, error
	Cols    int    `json:"cols,omitempty"`
	Rows    int    `json:"rows,omitempty"`
	State   string `json:"state,omitempty"` // connected, closed
	Message string `json:"message,omitempty"`
}

// v2Console bridges a browser WebSocket to the server's shared serial console session.
// Binary frames carry terminal bytes both ways; text frames carry consoleControl.
func (a *App) v2Console(c *gin.Context) {
	serverID := c.Param("id")
	ws, err := websocket.Accept(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	defer ws.CloseNow()
	ws.SetReadLimit(64 * 1024)

	ctx := c.Request.Context()
	sendControl := func(msg consoleControl) error {
		data, _ := json.Marshal(msg)
		writeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		return ws.Write(writeCtx, websocket.MessageText, data)
	}

	cols, _ := strconv.Atoi(c.Query("cols"))
	rows, _ := strconv.Atoi(c.Query("rows"))
	attachCtx, cancel := context.WithTimeout(ctx, consoleDialTime+5*time.Second)
	session, viewer, backlog, err := a.consoles.Attach(attachCtx, serverID, cols, rows)
	cancel()
	if err != nil {
		sendControl(consoleControl{Type: "error", Message: err.Error()})
		ws.Close(websocket.StatusNormalClosure, "")
		return
	}
	defer session.Detach(viewer)

	a.recordEvent(&Event{ServerID: serverID, Kind: "console", Message: "Opened the serial console", Actor: requester(c, ""), Level: "info"})

	if len(backlog) > 0 {
		if err := ws.Write(ctx, websocket.MessageBinary, backlog); err != nil {
			return
		}
	}
	sendControl(consoleControl{Type: "status", State: "connected"})

	// Browser -> console
	go func() {
		defer session.Detach(viewer)
		for {
			kind, data, err := ws.Read(ctx)
			if err != nil {
				return
			}
			if kind == websocket.MessageBinary {
				if err := session.Write(data); err != nil {
					return
				}
				continue
			}
			var msg consoleControl
			if json.Unmarshal(data, &msg) == nil && msg.Type == "resize" {
				session.Resize(msg.Cols, msg.Rows)
			}
		}
	}()

	// Console -> browser
	for chunk := range viewer.out {
		writeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		err := ws.Write(writeCtx, websocket.MessageBinary, chunk)
		cancel()
		if err != nil {
			return
		}
	}
	reason := viewer.reason
	if reason == "" {
		reason = "disconnected"
	}
	sendControl(consoleControl{Type: "status", State: "closed", Message: reason})
	ws.Close(websocket.StatusNormalClosure, "")
}

// v2ConsoleLog downloads the server's recent console output
func (a *App) v2ConsoleLog(c *gin.Context) {
	server := a.config.Get().Server(c.Param("id"))
	if server == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "server not found"})
		return
	}
	data := a.consoles.Log(server.ID)
	if len(data) == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "no console output captured yet"})
		return
	}
	name := fmt.Sprintf("%s-console-%s.log", server.ID, time.Now().Format("20060102-150405"))
	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, name))
	c.Data(http.StatusOK, "text/plain; charset=utf-8", data)
}
