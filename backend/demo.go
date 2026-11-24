package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Demo mode (-demo): a simulated lab, for trying Lab Manager without hardware.
// Nothing here touches the network.

const demoConfigYAML = `
servers:
  - id: node01
    name: node01
    description: "Compute, Dell R640"
    ipmi: {host: "192.0.2.11", username: "demo", password: "demo"}
    pdu_outlets: ["pdu-a:1", "pdu-a:2"]
    metadata: {role: compute, hostname: node01, mac: "02:00:00:00:00:01"}
  - id: node02
    name: node02
    description: "Compute, Dell R740"
    ipmi: {host: "192.0.2.12", username: "demo", password: "demo"}
    pdu_outlets: ["pdu-a:3"]
    metadata: {role: compute, hostname: node02, mac: "02:00:00:00:00:02"}
  - id: node03
    name: node03
    description: "Compute, Lenovo SR630"
    ipmi: {host: "192.0.2.13", username: "demo", password: "demo"}
    metadata: {role: compute, hostname: node03, mac: "02:00:00:00:00:03"}
  - id: node04
    name: node04
    description: "GPU, Lenovo SR670"
    ipmi: {host: "192.0.2.14", username: "demo", password: "demo"}
    pdu_outlets: ["pdu-a:4", "pdu-a:5"]
    metadata: {role: gpu, hostname: node04, mac: "02:00:00:00:00:04"}
  - id: storage01
    name: storage01
    description: "Storage, Dell R740xd"
    ipmi: {host: "192.0.2.21", username: "demo", password: "demo"}
    pdu_outlets: ["pdu-a:6"]
    metadata: {role: storage, hostname: storage01, mac: "02:00:00:00:00:21"}
  - id: spare01
    name: spare01
    description: "Spare, Lenovo SR630"
    ipmi: {host: "192.0.2.31", username: "demo", password: "demo"}
    metadata: {role: spare, hostname: spare01}
  - id: switch01
    name: switch01
    description: "InfiniBand switch"
    pdu_outlets: ["pdu-a:8"]
pdus:
  - {id: pdu-a, name: "Rack A PDU", host: "192.0.2.50", type: apc, snmp_community: demo}
delays:
  pdu_to_power_on_seconds: 5
  power_off_to_pdu_seconds: 2
  between_pdu_seconds: 3
console_capture:
  minutes: 2
`

type demoServer struct {
	vendor, model string
	idle, busy    float64 // watts when on
	health        string
	dark          bool // BMC never answers
}

var demoServers = map[string]demoServer{
	"node01":    {"Dell Inc.", "PowerEdge R640", 210, 340, "OK", false},
	"node02":    {"Dell Inc.", "PowerEdge R740", 260, 420, "OK", false},
	"node03":    {"Lenovo", "ThinkSystem SR630", 240, 380, "Warning", false},
	"node04":    {"Lenovo", "ThinkSystem SR670", 520, 1350, "OK", false},
	"storage01": {"Dell Inc.", "PowerEdge R740xd", 310, 390, "OK", false},
	"spare01":   {"Lenovo", "ThinkSystem SR630", 0, 0, "", true},
}

type demoLab struct {
	mu      sync.Mutex
	config  *Config
	power   map[string]PowerState
	outlets map[string]PowerState
	boot    map[string]string
	pending map[string]*time.Timer
}

// setupDemo writes the demo config to a temp directory and returns its path and a simulated lab
func setupDemo() (string, *demoLab, error) {
	dir, err := os.MkdirTemp("", "labmgr-demo-")
	if err != nil {
		return "", nil, err
	}
	path := filepath.Join(dir, "servers.yaml")
	if err := os.WriteFile(path, []byte(demoConfigYAML), 0600); err != nil {
		return "", nil, err
	}
	config, err := parseConfig([]byte(demoConfigYAML))
	if err != nil {
		return "", nil, err
	}
	lab := &demoLab{
		config:  config,
		power:   map[string]PowerState{"node01": PowerOn, "node02": PowerOn, "node03": PowerOff, "node04": PowerOn, "storage01": PowerOn},
		outlets: make(map[string]PowerState),
		boot:    make(map[string]string),
		pending: make(map[string]*time.Timer),
	}
	for _, s := range config.Servers {
		for _, ref := range s.Outlets() {
			lab.outlets[ref.String()] = PowerOn
		}
	}
	// node04's outlets are cut: its BMC is dark and the server reads as off
	lab.outlets["pdu-a:4"], lab.outlets["pdu-a:5"] = PowerOff, PowerOff
	lab.power["node04"] = PowerOff
	return path, lab, nil
}

func (l *demoLab) bmcPowered(id string) bool {
	if d, ok := demoServers[id]; ok && d.dark {
		return false
	}
	server := l.config.Server(id)
	if server == nil || len(server.Outlets()) == 0 {
		return true
	}
	for _, ref := range server.Outlets() {
		if l.outlets[ref.String()] == PowerOn {
			return true
		}
	}
	return false
}

func (l *demoLab) BMC(server *Server) BMC {
	if !server.HasBMC() {
		return nil
	}
	return &demoBMC{lab: l, id: server.ID}
}

func (l *demoLab) PDU(pdu *PDU) PDUDriver { return &demoPDU{lab: l, id: pdu.ID} }

// watts simulates power draw: standby when off, an idle/busy mix with some wander when on
func (l *demoLab) watts(id string, t time.Time) float64 {
	d := demoServers[id]
	if l.power[id] != PowerOn {
		return 12 + 3*math.Sin(float64(t.Unix())/97)
	}
	phase := float64(t.Unix()) / 600
	load := 0.5 + 0.35*math.Sin(phase+float64(len(id))) + 0.1*math.Sin(phase*7.3)
	return math.Round(d.idle + (d.busy-d.idle)*math.Max(0, math.Min(1, load)) + rand.Float64()*8)
}

// seedPowerHistory backfills a few hours of readings so charts aren't empty on first look
func (l *demoLab) seedPowerHistory(store *Store) {
	now := time.Now()
	for id := range demoServers {
		if demoServers[id].dark {
			continue
		}
		var samples []PowerSample
		for t := now.Add(-6 * time.Hour); t.Before(now); t = t.Add(time.Minute) {
			samples = append(samples, PowerSample{float64(t.UnixMilli()), l.watts(id, t)})
		}
		store.AppendPowerBatch(id, samples)
	}
}

var errDemoDark = errors.New("no route to host (is it plugged in and on the network?)")

type demoBMC struct {
	lab *demoLab
	id  string
}

func (b *demoBMC) PowerState(ctx context.Context) (PowerState, error) {
	b.lab.mu.Lock()
	defer b.lab.mu.Unlock()
	if !b.lab.bmcPowered(b.id) {
		return PowerUnknown, errDemoDark
	}
	return b.lab.power[b.id], nil
}

// Reset changes power a few seconds later, like real hardware
func (b *demoBMC) Reset(ctx context.Context, action string) error {
	b.lab.mu.Lock()
	defer b.lab.mu.Unlock()
	if !b.lab.bmcPowered(b.id) {
		return errDemoDark
	}
	var target PowerState
	switch action {
	case ActionOn:
		target = PowerOn
	case ActionOff, ActionGracefulShutdown:
		target = PowerOff
	case ActionReboot, ActionGracefulRestart, ActionPowerCycle:
		b.lab.power[b.id] = PowerOff
		target = PowerOn
	default:
		return nil
	}
	if t := b.lab.pending[b.id]; t != nil {
		t.Stop()
	}
	id := b.id
	b.lab.pending[id] = time.AfterFunc(4*time.Second, func() {
		b.lab.mu.Lock()
		b.lab.power[id] = target
		b.lab.mu.Unlock()
	})
	return nil
}

func (b *demoBMC) BootOverride(ctx context.Context) (BootOverride, error) {
	b.lab.mu.Lock()
	defer b.lab.mu.Unlock()
	target := b.lab.boot[b.id]
	if target == "" {
		return BootOverride{Target: BootNone, Enabled: "Disabled", Mode: "UEFI"}, nil
	}
	return BootOverride{Target: target, Enabled: "Once", Mode: "UEFI"}, nil
}

func (b *demoBMC) SetBootOverride(ctx context.Context, target string, persistent bool) error {
	b.lab.mu.Lock()
	defer b.lab.mu.Unlock()
	b.lab.boot[b.id] = target
	return nil
}

func (b *demoBMC) Info(ctx context.Context) (BMCInfo, error) {
	d := demoServers[b.id]
	bmc := "iDRAC9"
	if d.vendor == "Lenovo" {
		bmc = "XClarity Controller"
	}
	return BMCInfo{Manufacturer: d.vendor, Model: d.model, SerialNumber: fmt.Sprintf("DEMO%04d", len(b.id)*137), BIOSVersion: "2.19.1",
		Health: d.health, BMCModel: bmc, BMCFirmware: "7.10.30.00", GraphicalKVM: true}, nil
}

func (b *demoBMC) Logs(ctx context.Context, limit int) ([]BMCLogEntry, error) {
	now := time.Now()
	entries := []BMCLogEntry{
		{Created: now.Add(-2 * time.Hour).Format(time.RFC3339), Severity: "OK", Message: "The system booted."},
		{Created: now.Add(-26 * time.Hour).Format(time.RFC3339), Severity: "OK", Message: "Power supply redundancy restored."},
	}
	if demoServers[b.id].health == "Warning" {
		entries = append([]BMCLogEntry{{Created: now.Add(-40 * time.Minute).Format(time.RFC3339), Severity: "Warning", Message: "Power Supply 1 has lost input."}}, entries...)
	}
	return entries, nil
}

func (b *demoBMC) KVM(ctx context.Context) (KVMLaunch, error) {
	return KVMLaunch{URL: "about:blank", Kind: "login", Note: "Demo mode: there's no real console to open."}, nil
}

func (b *demoBMC) PowerWatts(ctx context.Context) (float64, error) {
	b.lab.mu.Lock()
	defer b.lab.mu.Unlock()
	if !b.lab.bmcPowered(b.id) {
		return 0, errDemoDark
	}
	return b.lab.watts(b.id, time.Now()), nil
}

func (b *demoBMC) ConsoleToken(ctx context.Context) (ConsoleToken, error) {
	return ConsoleToken{}, errors.New("demo mode has no graphical console")
}

type demoPDU struct {
	lab *demoLab
	id  string
}

func (p *demoPDU) OutletStates(ctx context.Context, outlets []int) (map[int]PowerState, error) {
	p.lab.mu.Lock()
	defer p.lab.mu.Unlock()
	states := make(map[int]PowerState)
	for _, o := range outlets {
		states[o] = p.lab.outlets[OutletRef{PDUID: p.id, Outlet: o}.String()]
	}
	return states, nil
}

func (p *demoPDU) SetOutlet(ctx context.Context, outlet int, action string) error {
	p.lab.mu.Lock()
	defer p.lab.mu.Unlock()
	p.lab.outlets[OutletRef{PDUID: p.id, Outlet: outlet}.String()] = PowerState(action)
	for _, s := range p.lab.config.Servers {
		if len(s.Outlets()) > 0 && !p.lab.bmcPowered(s.ID) {
			p.lab.power[s.ID] = PowerOff
		}
	}
	return nil
}

// demoConsole prints a short boot and a login prompt, and echoes what's typed
type demoConsole struct {
	r *io.PipeReader
	w *io.PipeWriter
}

func dialDemoConsole(_ context.Context, server *Server, _ string, _, _ int) (consoleConn, error) {
	r, w := io.Pipe()
	c := &demoConsole{r: r, w: w}
	go func() {
		lines := []string{
			"[    0.000000] Linux version 6.12.0 (demo)",
			"[    1.204511] ACPI: button: Power Button [PWRF]",
			"[    3.882140] systemd[1]: Starting Lab Manager demo boot...",
			"[  OK  ] Reached target Multi-User System.",
			"",
			server.Hostname() + " login: ",
		}
		for _, line := range lines {
			time.Sleep(400 * time.Millisecond)
			if _, err := fmt.Fprint(w, line+"\r\n"); err != nil {
				return
			}
		}
	}()
	return c, nil
}

func (c *demoConsole) Read(p []byte) (int, error)  { return c.r.Read(p) }
func (c *demoConsole) Write(p []byte) (int, error) { return c.w.Write(p) }
func (c *demoConsole) Resize(int, int) error       { return nil }
func (c *demoConsole) Close() error                { return c.w.Close() }
