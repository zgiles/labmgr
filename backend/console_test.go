package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// fakeConsole is a console whose "BMC side" the test drives
type fakeConsole struct {
	toViewer *io.PipeWriter // test writes here: what the BMC prints
	reader   *io.PipeReader

	mu      sync.Mutex
	typed   bytes.Buffer // what viewers typed
	resizes [][2]int
	closed  bool
}

func newFakeConsole() *fakeConsole {
	r, w := io.Pipe()
	return &fakeConsole{toViewer: w, reader: r}
}

func (f *fakeConsole) Read(p []byte) (int, error) { return f.reader.Read(p) }
func (f *fakeConsole) Write(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.typed.Write(p)
}
func (f *fakeConsole) Resize(cols, rows int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.resizes = append(f.resizes, [2]int{cols, rows})
	return nil
}
func (f *fakeConsole) Close() error {
	f.mu.Lock()
	f.closed = true
	f.mu.Unlock()
	return f.toViewer.Close()
}
func (f *fakeConsole) Typed() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.typed.String()
}

// fakeDialer hands out one fakeConsole per dial and remembers the attach command
type fakeDialer struct {
	mu       sync.Mutex
	dials    int
	commands []string
	consoles []*fakeConsole
}

func (d *fakeDialer) dial(_ context.Context, _ *Server, cmd string, _, _ int) (consoleConn, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.dials++
	d.commands = append(d.commands, cmd)
	c := newFakeConsole()
	d.consoles = append(d.consoles, c)
	return c, nil
}

func (d *fakeDialer) last() *fakeConsole {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.consoles[len(d.consoles)-1]
}

func receive(t *testing.T, v *consoleViewer, want string) {
	t.Helper()
	var got strings.Builder
	deadline := time.After(3 * time.Second)
	for !strings.Contains(got.String(), want) {
		select {
		case chunk, ok := <-v.out:
			if !ok {
				t.Fatalf("viewer closed before %q arrived (got %q)", want, got.String())
			}
			got.Write(chunk)
		case <-deadline:
			t.Fatalf("timed out waiting for %q (got %q)", want, got.String())
		}
	}
}

func TestConsoleViewersShareOneSession(t *testing.T) {
	app, _ := newTestApp(t)
	dialer := &fakeDialer{}
	app.consoles.dial = dialer.dial
	ctx := context.Background()

	s1, v1, _, err := app.consoles.Attach(ctx, "bmconly", 100, 30)
	if err != nil {
		t.Fatal(err)
	}
	bmc := dialer.last()
	bmc.toViewer.Write([]byte("POST complete\r\n"))
	receive(t, v1, "POST complete")

	// A second viewer joins the same session and gets the backlog
	s2, v2, backlog, err := app.consoles.Attach(ctx, "bmconly", 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	if s1 != s2 || dialer.dials != 1 {
		t.Fatalf("second viewer should share the session (dials=%d)", dialer.dials)
	}
	if !strings.Contains(string(backlog), "POST complete") {
		t.Errorf("backlog = %q", backlog)
	}

	bmc.toViewer.Write([]byte("login: "))
	receive(t, v1, "login: ")
	receive(t, v2, "login: ")

	s2.Write([]byte("root\r"))
	if bmc.Typed() != "root\r" {
		t.Errorf("typed = %q", bmc.Typed())
	}
	if dialer.commands[0] != "console com2" {
		t.Errorf("Dell BMC should get 'console com2', got %q", dialer.commands[0])
	}
}

func TestConsoleClosesWhenIdleAndKeepsLog(t *testing.T) {
	app, _ := newTestApp(t)
	dialer := &fakeDialer{}
	app.consoles.dial = dialer.dial
	app.consoles.idle = 20 * time.Millisecond

	s, v, _, err := app.consoles.Attach(context.Background(), "bmconly", 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	dialer.last().toViewer.Write([]byte("kernel panic\r\n"))
	receive(t, v, "kernel panic")
	s.Detach(v)

	deadline := time.Now().Add(2 * time.Second)
	for {
		dialer.last().mu.Lock()
		closed := dialer.last().closed
		dialer.last().mu.Unlock()
		if closed {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("idle session never closed")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !strings.Contains(string(app.consoles.Log("bmconly")), "kernel panic") {
		t.Error("console log lost after the session closed")
	}

	// Reattaching dials a fresh session
	if _, _, _, err := app.consoles.Attach(context.Background(), "bmconly", 80, 24); err != nil {
		t.Fatal(err)
	}
	if dialer.dials != 2 {
		t.Errorf("dials = %d, want 2", dialer.dials)
	}
}

func TestConsoleRequiresBMC(t *testing.T) {
	app, _ := newTestApp(t)
	if _, _, _, err := app.consoles.Attach(context.Background(), "pduonly", 80, 24); err == nil {
		t.Fatal("PDU-only server shouldn't have a console")
	}
}

func TestPromptTyperWaitsForQuiet(t *testing.T) {
	bmc := newFakeConsole()
	typer := newPromptTyper(bmc, "console com2")
	go io.Copy(io.Discard, typer)

	bmc.toViewer.Write([]byte("Welcome to the iDRAC\r\nracadm>>"))
	time.Sleep(300 * time.Millisecond)
	if bmc.Typed() != "" {
		t.Fatal("typed before the banner went quiet")
	}
	deadline := time.Now().Add(3 * time.Second)
	for bmc.Typed() == "" && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if bmc.Typed() != "console com2\r" {
		t.Errorf("typed = %q", bmc.Typed())
	}
}

func TestConsoleWebSocket(t *testing.T) {
	app, _ := newTestApp(t)
	dialer := &fakeDialer{}
	app.consoles.dial = dialer.dial
	server := httptest.NewServer(app.router())
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	url := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/v2/servers/bmconly/console?cols=100&rows=30"
	ws, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.CloseNow()

	// Status arrives once attached
	kind, data, err := ws.Read(ctx)
	if err != nil || kind != websocket.MessageText || !strings.Contains(string(data), `"connected"`) {
		t.Fatalf("first frame: %v %s %v", kind, data, err)
	}

	dialer.last().toViewer.Write([]byte("ubuntu login: "))
	kind, data, err = ws.Read(ctx)
	if err != nil || kind != websocket.MessageBinary || string(data) != "ubuntu login: " {
		t.Fatalf("output frame: %v %q %v", kind, data, err)
	}

	ws.Write(ctx, websocket.MessageBinary, []byte("root\r"))
	resize, _ := json.Marshal(consoleControl{Type: "resize", Cols: 132, Rows: 43})
	ws.Write(ctx, websocket.MessageText, resize)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		c := dialer.last()
		c.mu.Lock()
		done := c.typed.String() == "root\r" && len(c.resizes) > 0 && c.resizes[len(c.resizes)-1] == [2]int{132, 43}
		c.mu.Unlock()
		if done {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	c := dialer.last()
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.typed.String() != "root\r" {
		t.Errorf("typed = %q", c.typed.String())
	}
	if len(c.resizes) == 0 || c.resizes[len(c.resizes)-1] != [2]int{132, 43} {
		t.Errorf("resizes = %v", c.resizes)
	}

	events, _ := app.store.Events("bmconly", 10)
	if len(events) == 0 || events[0].Kind != "console" {
		t.Errorf("console open not recorded: %+v", events)
	}
}
