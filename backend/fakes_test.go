package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// fakeLab simulates the hardware: outlets feed servers, a server's BMC is dark
// while all its outlets are off, and BMC resets change system power.
type fakeLab struct {
	mu      sync.Mutex
	outlets map[string]PowerState // "pdu-x:1" -> state
	power   map[string]PowerState // server ID -> system power
	boot    map[string]string     // server ID -> boot override target
	calls   []string
	config  *Config

	failResets map[string]int       // server ID -> remaining Reset failures
	gate       chan struct{}        // if set, Reset blocks until it's closed
	onReset    func(string, string) // hook: server ID, action
}

func newFakeLab(config *Config) *fakeLab {
	lab := &fakeLab{
		outlets:    make(map[string]PowerState),
		power:      make(map[string]PowerState),
		boot:       make(map[string]string),
		failResets: make(map[string]int),
		config:     config,
	}
	for _, s := range config.Servers {
		lab.power[s.ID] = PowerOff
		for _, ref := range s.Outlets() {
			lab.outlets[ref.String()] = PowerOff
		}
	}
	return lab
}

func (l *fakeLab) record(format string, args ...any) {
	l.calls = append(l.calls, fmt.Sprintf(format, args...))
}

func (l *fakeLab) Calls() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.calls...)
}

// bmcPowered reports whether a server's BMC has standby power (caller holds mu)
func (l *fakeLab) bmcPowered(id string) bool {
	server := l.config.Server(id)
	if len(server.Outlets()) == 0 {
		return true
	}
	for _, ref := range server.Outlets() {
		if l.outlets[ref.String()] == PowerOn {
			return true
		}
	}
	return false
}

// set puts the lab into a starting state
func (l *fakeLab) set(serverID string, power PowerState, outlets PowerState) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.power[serverID] = power
	for _, ref := range l.config.Server(serverID).Outlets() {
		l.outlets[ref.String()] = outlets
	}
}

func (l *fakeLab) BMC(server *Server) BMC {
	if !server.HasBMC() {
		return nil
	}
	return &fakeBMC{lab: l, id: server.ID}
}

func (l *fakeLab) PDU(pdu *PDU) PDUDriver { return &fakePDU{lab: l, id: pdu.ID} }

type fakeBMC struct {
	lab *fakeLab
	id  string
}

var errDark = errors.New("connection refused")

func (b *fakeBMC) PowerState(ctx context.Context) (PowerState, error) {
	b.lab.mu.Lock()
	defer b.lab.mu.Unlock()
	if !b.lab.bmcPowered(b.id) {
		return PowerUnknown, errDark
	}
	return b.lab.power[b.id], nil
}

func (b *fakeBMC) Reset(ctx context.Context, action string) error {
	if gate := b.lab.gate; gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	b.lab.mu.Lock()
	if !b.lab.bmcPowered(b.id) {
		b.lab.mu.Unlock()
		return errDark
	}
	if b.lab.failResets[b.id] > 0 {
		b.lab.failResets[b.id]--
		b.lab.mu.Unlock()
		return errors.New("BMC busy")
	}
	b.lab.record("bmc %s %s", b.id, action)
	switch action {
	case ActionOn:
		b.lab.power[b.id] = PowerOn
	case ActionOff, ActionGracefulShutdown:
		b.lab.power[b.id] = PowerOff
	}
	hook := b.lab.onReset
	b.lab.mu.Unlock()
	if hook != nil {
		hook(b.id, action)
	}
	return nil
}

func (b *fakeBMC) BootOverride(ctx context.Context) (BootOverride, error) {
	b.lab.mu.Lock()
	defer b.lab.mu.Unlock()
	return BootOverride{Target: b.lab.boot[b.id], Enabled: "Once"}, nil
}

func (b *fakeBMC) SetBootOverride(ctx context.Context, target string, persistent bool) error {
	b.lab.mu.Lock()
	defer b.lab.mu.Unlock()
	if !b.lab.bmcPowered(b.id) {
		return errDark
	}
	b.lab.record("boot %s %s", b.id, target)
	b.lab.boot[b.id] = target
	return nil
}

func (b *fakeBMC) Info(ctx context.Context) (BMCInfo, error) {
	return BMCInfo{Manufacturer: "Dell Inc.", Model: "Fake"}, nil
}

func (b *fakeBMC) PowerWatts(ctx context.Context) (float64, error) {
	b.lab.mu.Lock()
	defer b.lab.mu.Unlock()
	if !b.lab.bmcPowered(b.id) {
		return 0, errDark
	}
	if b.lab.power[b.id] == PowerOn {
		return 250, nil
	}
	return 12, nil
}

func (b *fakeBMC) KVM(ctx context.Context) (KVMLaunch, error) {
	return KVMLaunch{URL: "https://bmc.example/console?tempPassword=one-time", Kind: "direct"}, nil
}

func (b *fakeBMC) Logs(ctx context.Context, limit int) ([]BMCLogEntry, error) { return nil, nil }

type fakePDU struct {
	lab *fakeLab
	id  string
}

func (p *fakePDU) OutletStates(ctx context.Context, outlets []int) (map[int]PowerState, error) {
	p.lab.mu.Lock()
	defer p.lab.mu.Unlock()
	states := make(map[int]PowerState)
	for _, o := range outlets {
		states[o] = p.lab.outlets[OutletRef{PDUID: p.id, Outlet: o}.String()]
	}
	return states, nil
}

func (p *fakePDU) SetOutlet(ctx context.Context, outlet int, action string) error {
	p.lab.mu.Lock()
	defer p.lab.mu.Unlock()
	ref := OutletRef{PDUID: p.id, Outlet: outlet}.String()
	p.lab.record("outlet %s %s", ref, action)
	p.lab.outlets[ref] = PowerState(action)
	// Cutting every outlet kills the system too
	for _, s := range p.lab.config.Servers {
		if len(s.Outlets()) > 0 && !p.lab.bmcPowered(s.ID) {
			p.lab.power[s.ID] = PowerOff
		}
	}
	return nil
}

const testConfigYAML = `
servers:
  - id: "both"
    name: "both"
    ipmi: {host: "192.0.2.1", username: "u", password: "secret-pw"}
    pdu_outlets: ["pdu-x:1", "pdu-x:2"]
    metadata: {mac: "AA:BB:CC:DD:EE:01"}
  - id: "bmconly"
    name: "bmconly"
    ipmi: {host: "192.0.2.2", username: "u", password: "secret-pw"}
    metadata: {mac: "aa:bb:cc:dd:ee:02", hostname: "bmc-only"}
  - id: "pduonly"
    name: "pduonly"
    pdu_outlets: ["pdu-x:3"]
pdus:
  - {id: "pdu-x", name: "x", host: "192.0.2.9", type: "apc", snmp_community: "secret-community"}
delays:
  pdu_to_power_on_seconds: 5
  power_off_to_pdu_seconds: 2
  between_pdu_seconds: 3
  between_ipmi_seconds: 1
`

// newTestApp builds the whole app over a fake lab with time shrunk to milliseconds
func newTestApp(t *testing.T) (*App, *fakeLab) {
	t.Helper()
	dir := t.TempDir()
	configPath = filepath.Join(dir, "servers.yaml")
	if err := os.WriteFile(configPath, []byte(testConfigYAML), 0600); err != nil {
		t.Fatal(err)
	}
	config, err := NewConfigStore(configPath)
	if err != nil {
		t.Fatal(err)
	}
	osManager = NewOSManager()

	store, err := OpenStore(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}

	hub := NewHub()
	state := NewStateStore(hub)
	state.Sync(config.Get())
	lab := newFakeLab(config.Get())
	poller := NewPoller(config, lab, state)
	poller.store = store
	engine := NewEngine(config, lab, state, poller, store, hub, osManager)
	engine.timing = Timing{PollInterval: time.Millisecond, RetryBase: time.Millisecond, ActionTime: 5 * time.Second, Unit: time.Millisecond}

	ctx, cancel := context.WithCancel(context.Background())
	go engine.Run(ctx)
	t.Cleanup(func() {
		cancel()
		engine.Wait()
		store.Close()
	})
	return &App{config: config, state: state, poller: poller, engine: engine, store: store, hub: hub, hw: lab, consoles: NewConsoleHub(config, lab)}, lab
}

// waitOp waits for an operation to finish and returns it
func waitOp(t *testing.T, app *App, id string) Operation {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if op, ok := app.engine.Get(id); ok && op.Status.Finished() {
			return op
		}
		time.Sleep(2 * time.Millisecond)
	}
	op, _ := app.engine.Get(id)
	t.Fatalf("operation %s didn't finish: %+v", id, op)
	return op
}

// submit polls the server (so planning sees the lab's state), submits, and waits
func submit(t *testing.T, app *App, req OpRequest) Operation {
	t.Helper()
	app.poller.PollNow(context.Background(), req.ServerID)
	op, err := app.engine.Submit(context.Background(), req)
	if err != nil {
		t.Fatalf("submit %s: %v", req.Kind, err)
	}
	if op.Status == OpScheduled {
		return op
	}
	return waitOp(t, app, op.ID)
}
