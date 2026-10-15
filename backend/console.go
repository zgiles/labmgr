package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

const (
	consoleScrollback = 256 * 1024       // bytes of output kept per server
	consoleIdleClose  = 60 * time.Second // keep the BMC session this long after the last viewer leaves
	consoleDialTime   = 20 * time.Second
)

// consoleConn is a live serial console: bytes in, bytes out, resizable
type consoleConn interface {
	io.ReadWriteCloser
	Resize(cols, rows int) error
}

// consoleDialer opens a console connection to a server
type consoleDialer func(ctx context.Context, server *Server, attachCommand string, cols, rows int) (consoleConn, error)

// ConsoleHub shares one console session per server between any number of
// browser viewers (BMCs usually allow only one serial session) and keeps
// recent output so a reopened console, or a log download, shows what scrolled past.
type ConsoleHub struct {
	config *ConfigStore
	hw     Hardware
	dial   consoleDialer
	idle   time.Duration

	mu       sync.Mutex
	sessions map[string]*consoleSession
	logs     map[string]*ring  // survives sessions
	commands map[string]string // detected attach command per server
}

func NewConsoleHub(config *ConfigStore, hw Hardware) *ConsoleHub {
	return &ConsoleHub{
		config:   config,
		hw:       hw,
		dial:     dialSSHConsole,
		idle:     consoleIdleClose,
		sessions: make(map[string]*consoleSession),
		logs:     make(map[string]*ring),
		commands: make(map[string]string),
	}
}

type consoleSession struct {
	hub      *ConsoleHub
	serverID string
	ready    chan struct{} // closed once dialing finishes
	err      error         // dial error, valid after ready

	mu      sync.Mutex
	conn    consoleConn
	viewers map[*consoleViewer]struct{}
	closed  bool
	idle    *time.Timer
}

// consoleViewer receives output; out is closed when the session ends or the viewer falls behind
type consoleViewer struct {
	out    chan []byte
	reason string // why out was closed
}

// Attach joins (or starts) the server's console session and returns the backlog to replay
func (h *ConsoleHub) Attach(ctx context.Context, serverID string, cols, rows int) (*consoleSession, *consoleViewer, []byte, error) {
	server := h.config.Get().Server(serverID)
	if server == nil {
		return nil, nil, nil, notFound("server %s not found", serverID)
	}
	if !server.HasBMC() {
		return nil, nil, nil, badRequest("%s has no BMC, so there's no serial console", server.Name)
	}

	h.mu.Lock()
	s, ok := h.sessions[serverID]
	if !ok {
		s = &consoleSession{hub: h, serverID: serverID, ready: make(chan struct{}), viewers: make(map[*consoleViewer]struct{})}
		h.sessions[serverID] = s
		if h.logs[serverID] == nil {
			h.logs[serverID] = newRing(consoleScrollback)
		}
		go s.start(server, cols, rows)
	}
	h.mu.Unlock()

	select {
	case <-s.ready:
	case <-ctx.Done():
		return nil, nil, nil, ctx.Err()
	}
	if s.err != nil {
		return nil, nil, nil, s.err
	}

	v := &consoleViewer{out: make(chan []byte, 256)}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, nil, nil, fmt.Errorf("console session ended")
	}
	if s.idle != nil {
		s.idle.Stop()
		s.idle = nil
	}
	s.viewers[v] = struct{}{}
	if len(s.viewers) == 1 {
		s.conn.Resize(cols, rows) // first viewer sets the size
	}
	return s, v, h.Log(serverID), nil
}

// Log returns the server's recent console output
func (h *ConsoleHub) Log(serverID string) []byte {
	h.mu.Lock()
	r := h.logs[serverID]
	h.mu.Unlock()
	if r == nil {
		return nil
	}
	return r.Bytes()
}

// attachCommand picks what to type at the BMC prompt to reach the host's serial port
func (h *ConsoleHub) attachCommand(ctx context.Context, server *Server) string {
	if server.Console.Command != "" {
		return server.Console.Command
	}
	h.mu.Lock()
	cmd, ok := h.commands[server.ID]
	h.mu.Unlock()
	if ok {
		return cmd
	}

	infoCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	info, err := h.hw.BMC(server).Info(infoCtx)
	if err != nil {
		return "" // leave the user at the BMC prompt
	}
	vendor := strings.ToLower(info.Manufacturer + " " + info.BMCModel)
	switch {
	case strings.Contains(vendor, "dell"):
		cmd = "console com2"
	case strings.Contains(vendor, "lenovo"):
		cmd = "console 1"
	case strings.Contains(vendor, "hpe"), strings.Contains(vendor, "hewlett"):
		cmd = "vsp"
	case strings.Contains(vendor, "supermicro"):
		cmd = "cd system1/sol1\rstart"
	}
	h.mu.Lock()
	h.commands[server.ID] = cmd
	h.mu.Unlock()
	return cmd
}

func (s *consoleSession) start(server *Server, cols, rows int) {
	ctx, cancel := context.WithTimeout(context.Background(), consoleDialTime)
	defer cancel()
	cmd := s.hub.attachCommand(ctx, server)
	conn, err := s.hub.dial(ctx, server, cmd, cols, rows)
	if err != nil {
		s.err = fmt.Errorf("couldn't open the console: %w", err)
		s.hub.mu.Lock()
		delete(s.hub.sessions, s.serverID)
		s.hub.mu.Unlock()
		close(s.ready)
		return
	}
	s.conn = conn
	close(s.ready)
	log.Printf("Console session opened for %s", s.serverID)
	s.readLoop()
}

func (s *consoleSession) readLoop() {
	h := s.hub
	h.mu.Lock()
	scrollback := h.logs[s.serverID]
	h.mu.Unlock()

	buf := make([]byte, 8192)
	for {
		n, err := s.conn.Read(buf)
		if n > 0 {
			chunk := append([]byte(nil), buf[:n]...)
			scrollback.Write(chunk)
			s.mu.Lock()
			for v := range s.viewers {
				select {
				case v.out <- chunk:
				default:
					v.reason = "fell behind"
					delete(s.viewers, v)
					close(v.out)
				}
			}
			s.mu.Unlock()
		}
		if err != nil {
			reason := "the BMC closed the session"
			if !errors.Is(err, io.EOF) {
				reason = err.Error()
			}
			s.shutdown(reason)
			return
		}
	}
}

// Write sends keystrokes to the console
func (s *consoleSession) Write(p []byte) error {
	_, err := s.conn.Write(p)
	return err
}

func (s *consoleSession) Resize(cols, rows int) error {
	return s.conn.Resize(cols, rows)
}

// Detach removes a viewer; the session closes after it has been idle a while
func (s *consoleSession) Detach(v *consoleViewer) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.viewers[v]; ok {
		delete(s.viewers, v)
		close(v.out)
	}
	if len(s.viewers) == 0 && !s.closed && s.idle == nil {
		s.idle = time.AfterFunc(s.hub.idle, func() { s.shutdown("idle") })
	}
}

func (s *consoleSession) shutdown(reason string) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	for v := range s.viewers {
		v.reason = reason
		close(v.out)
	}
	s.viewers = nil
	s.mu.Unlock()

	s.conn.Close()
	s.hub.mu.Lock()
	if s.hub.sessions[s.serverID] == s {
		delete(s.hub.sessions, s.serverID)
	}
	s.hub.mu.Unlock()
	log.Printf("Console session for %s closed: %s", s.serverID, reason)
}

// ring keeps the last n bytes written to it
type ring struct {
	mu   sync.Mutex
	data []byte
	max  int
}

func newRing(max int) *ring { return &ring{max: max} }

func (r *ring) Write(p []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.data = append(r.data, p...)
	if over := len(r.data) - r.max; over > 0 {
		r.data = append([]byte(nil), r.data[over:]...)
	}
}

func (r *ring) Bytes() []byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]byte(nil), r.data...)
}

// sshConsole is a BMC CLI session over SSH with a PTY
type sshConsole struct {
	client  *ssh.Client
	session *ssh.Session
	stdin   io.WriteCloser
	stdout  io.Reader
}

func (c *sshConsole) Read(p []byte) (int, error)  { return c.stdout.Read(p) }
func (c *sshConsole) Write(p []byte) (int, error) { return c.stdin.Write(p) }
func (c *sshConsole) Resize(cols, rows int) error {
	if cols <= 0 || rows <= 0 {
		return nil
	}
	return c.session.WindowChange(rows, cols)
}
func (c *sshConsole) Close() error {
	c.session.Close()
	return c.client.Close()
}

// bmcSSHAddress turns the configured BMC host (maybe a URL) into host:port for SSH
func bmcSSHAddress(server *Server) string {
	host := server.IPMI.Host
	if i := strings.Index(host, "://"); i >= 0 {
		host = host[i+3:]
	}
	host = strings.TrimSuffix(host, "/")
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	port := 22
	if server.Console.SSHPort > 0 {
		port = server.Console.SSHPort
	}
	return net.JoinHostPort(host, strconv.Itoa(port))
}

// dialSSHConsole logs in to the BMC over SSH, opens a PTY, and types the attach command at the prompt
func dialSSHConsole(ctx context.Context, server *Server, attachCommand string, cols, rows int) (consoleConn, error) {
	password := server.IPMI.Password
	config := &ssh.ClientConfig{
		User: server.IPMI.Username,
		Auth: []ssh.AuthMethod{
			ssh.Password(password),
			// iDRAC and XCC often ask through keyboard-interactive instead
			ssh.KeyboardInteractive(func(_, _ string, questions []string, _ []bool) ([]string, error) {
				answers := make([]string, len(questions))
				for i := range answers {
					answers[i] = password
				}
				return answers, nil
			}),
		},
		// BMCs regenerate self-signed host keys on reset; there's nothing stable to pin on a lab network
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         consoleDialTime,
	}

	addr := bmcSSHAddress(server)
	var dialer net.Dialer
	raw, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, friendlyError(err)
	}
	if deadline, ok := ctx.Deadline(); ok {
		raw.SetDeadline(deadline)
	}
	conn, chans, reqs, err := ssh.NewClientConn(raw, addr, config)
	if err != nil {
		raw.Close()
		if strings.Contains(err.Error(), "unable to authenticate") {
			return nil, fmt.Errorf("SSH login to the BMC was rejected (check the username and password, and that SSH is enabled)")
		}
		return nil, err
	}
	raw.SetDeadline(time.Time{})
	client := ssh.NewClient(conn, chans, reqs)

	session, err := client.NewSession()
	if err != nil {
		client.Close()
		return nil, err
	}
	stdin, err := session.StdinPipe()
	if err != nil {
		client.Close()
		return nil, err
	}
	pr, pw := io.Pipe()
	session.Stdout = pw
	session.Stderr = pw
	if cols <= 0 || rows <= 0 {
		cols, rows = 120, 40
	}
	modes := ssh.TerminalModes{ssh.ECHO: 1, ssh.TTY_OP_ISPEED: 115200, ssh.TTY_OP_OSPEED: 115200}
	if err := session.RequestPty("xterm-256color", rows, cols, modes); err != nil {
		client.Close()
		return nil, err
	}
	if err := session.Shell(); err != nil {
		client.Close()
		return nil, err
	}
	go func() {
		session.Wait()
		pw.Close()
	}()

	c := &sshConsole{client: client, session: session, stdin: stdin, stdout: pr}
	if attachCommand == "" {
		return c, nil
	}
	return newPromptTyper(c, attachCommand), nil
}

// promptTyper types a command once the BMC CLI has gone quiet after its banner and prompt
type promptTyper struct {
	consoleConn
	mu       sync.Mutex
	lastData time.Time
	sent     bool
}

func newPromptTyper(conn consoleConn, command string) *promptTyper {
	t := &promptTyper{consoleConn: conn}
	started := time.Now()
	go func() {
		for range time.Tick(200 * time.Millisecond) {
			t.mu.Lock()
			quiet := !t.lastData.IsZero() && time.Since(t.lastData) > 700*time.Millisecond
			due := quiet || time.Since(started) > 6*time.Second
			if due && !t.sent {
				t.sent = true
				t.mu.Unlock()
				t.consoleConn.Write([]byte(command + "\r"))
				return
			}
			done := t.sent
			t.mu.Unlock()
			if done {
				return
			}
		}
	}()
	return t
}

func (t *promptTyper) Read(p []byte) (int, error) {
	n, err := t.consoleConn.Read(p)
	if n > 0 {
		t.mu.Lock()
		t.lastData = time.Now()
		t.mu.Unlock()
	}
	return n, err
}
