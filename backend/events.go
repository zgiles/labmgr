package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// Message is one update pushed to subscribers
type Message struct {
	Type string `json:"type"` // "state", "operation", "event", "os"
	Data any    `json:"data"`
}

// Hub fans updates out to connected browsers. A subscriber that falls behind is
// dropped; its client reconnects and gets a fresh snapshot.
type Hub struct {
	mu   sync.Mutex
	subs map[chan Message]struct{}
}

func NewHub() *Hub {
	return &Hub{subs: make(map[chan Message]struct{})}
}

func (h *Hub) Subscribe() chan Message {
	ch := make(chan Message, 256)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	h.mu.Unlock()
	return ch
}

func (h *Hub) Unsubscribe(ch chan Message) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.subs[ch]; ok {
		delete(h.subs, ch)
		close(ch)
	}
}

func (h *Hub) Publish(msgType string, data any) {
	h.mu.Lock()
	defer h.mu.Unlock()
	msg := Message{Type: msgType, Data: data}
	for ch := range h.subs {
		select {
		case ch <- msg:
		default:
			delete(h.subs, ch)
			close(ch)
		}
	}
}

// Event is one line in a server's activity history
type Event struct {
	ID       string    `json:"id"`
	At       time.Time `json:"at"`
	ServerID string    `json:"server_id,omitempty"`
	Kind     string    `json:"kind"` // operation, os, config, boot
	Message  string    `json:"message"`
	Actor    string    `json:"actor,omitempty"`
	Level    string    `json:"level,omitempty"` // info, warning, error
}

// serveStream streams updates as Server-Sent Events, starting with a full snapshot
func serveStream(c *gin.Context, hub *Hub, snapshot func() any) {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")

	ch := hub.Subscribe()
	defer hub.Unsubscribe(ch)

	write := func(event string, data any) error {
		payload, err := json.Marshal(data)
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintf(c.Writer, "event: %s\ndata: %s\n\n", event, payload); err != nil {
			return err
		}
		c.Writer.Flush()
		return nil
	}

	c.Status(http.StatusOK)
	if err := write("snapshot", snapshot()); err != nil {
		return
	}

	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-c.Request.Context().Done():
			return
		case msg, ok := <-ch:
			if !ok {
				return // dropped for falling behind; the client will reconnect
			}
			if err := write(msg.Type, msg.Data); err != nil {
				return
			}
		case <-heartbeat.C:
			if _, err := fmt.Fprint(c.Writer, ": keepalive\n\n"); err != nil {
				return
			}
			c.Writer.Flush()
		}
	}
}
