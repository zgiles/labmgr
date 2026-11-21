package main

import (
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Servers        []Server             `yaml:"servers"`
	PDUs           []PDU                `yaml:"pdus"`
	Delays         DelayConfig          `yaml:"delays"`
	ConsoleCapture ConsoleCaptureConfig `yaml:"console_capture"`
}

// ConsoleCaptureConfig controls recording serial console output to disk
type ConsoleCaptureConfig struct {
	// Operations that start a recording (default: power_on, restart, graceful_restart, power_cycle, reinstall)
	After []string `yaml:"after,omitempty" json:"after"`
	// How long a recording runs after the operation starts (default 15)
	Minutes int `yaml:"minutes,omitempty" json:"minutes"`
	// Record every server continuously, one log per server per day (default off).
	// Holds the BMC's serial session open all the time, so nothing else can use it.
	Always bool `yaml:"always,omitempty" json:"always"`
	// Delete recordings older than this many days (default 30)
	RetainDays int `yaml:"retain_days,omitempty" json:"retain_days"`
}

var defaultCaptureAfter = []string{OpPowerOn, OpRestart, OpGracefulRestart, OpPowerCycle, OpReinstall}

// WithDefaults fills unset fields
func (c ConsoleCaptureConfig) WithDefaults() ConsoleCaptureConfig {
	if c.After == nil {
		c.After = defaultCaptureAfter
	}
	if c.Minutes <= 0 {
		c.Minutes = 15
	}
	if c.RetainDays <= 0 {
		c.RetainDays = 30
	}
	return c
}

type DelayConfig struct {
	PDUToPowerOn  int `yaml:"pdu_to_power_on_seconds" json:"pdu_to_power_on_seconds"`   // Delay from PDU on to IPMI power on
	PowerOffToPDU int `yaml:"power_off_to_pdu_seconds" json:"power_off_to_pdu_seconds"` // Delay from IPMI off to PDU off
	BetweenIPMI   int `yaml:"between_ipmi_seconds" json:"between_ipmi_seconds"`         // Pause between the off and on halves of a power cycle
	BetweenPDU    int `yaml:"between_pdu_seconds" json:"between_pdu_seconds"`           // Delay between PDU outlet actions
}

type Server struct {
	ID          string            `yaml:"id" json:"id"`
	Name        string            `yaml:"name" json:"name"`
	Description string            `yaml:"description" json:"description"`
	IPMI        IPMIConfig        `yaml:"ipmi" json:"ipmi"`
	PDUOutlets  []string          `yaml:"pdu_outlets" json:"pdu_outlets"`
	Metadata    map[string]string `yaml:"metadata" json:"metadata"`
	Console     ConsoleConfig     `yaml:"console" json:"console"`

	outlets []OutletRef
}

type IPMIConfig struct {
	Host     string `yaml:"host" json:"host"`
	Username string `yaml:"username" json:"username"`
	Password string `yaml:"password" json:"password"`
}

// ConsoleConfig tunes how the serial console reaches this server's BMC
type ConsoleConfig struct {
	// Command typed after logging in to the BMC over SSH to attach to the host serial port.
	// Defaults by vendor: "console 1" (Lenovo XCC), "console com2" (Dell iDRAC).
	Command string `yaml:"command,omitempty" json:"command,omitempty"`
	// SSHPort overrides the BMC SSH port (default 22)
	SSHPort int `yaml:"ssh_port,omitempty" json:"ssh_port,omitempty"`
	// WebURL overrides the link used for the BMC's own web console
	WebURL string `yaml:"web_url,omitempty" json:"web_url,omitempty"`
	// KVMPort overrides the Lenovo XCC remote presence port (default 3900)
	KVMPort int `yaml:"kvm_port,omitempty" json:"kvm_port,omitempty"`
	// Capture overrides console_capture for this server: "off" or "always"
	Capture string `yaml:"capture,omitempty" json:"capture,omitempty"`
}

type PDU struct {
	ID            string `yaml:"id" json:"id"`
	Name          string `yaml:"name" json:"name"`
	Host          string `yaml:"host" json:"host"`
	Username      string `yaml:"username" json:"username"`                                 // HTTP username
	Password      string `yaml:"password" json:"password"`                                 // HTTP password
	Type          string `yaml:"type" json:"type"`                                         // "apc" for SNMP, others for HTTP
	SNMPCommunity string `yaml:"snmp_community,omitempty" json:"snmp_community,omitempty"` // SNMP community string
	SNMPVersion   string `yaml:"snmp_version,omitempty" json:"snmp_version,omitempty"`     // "v1" or "v2c"
	SNMPPort      int    `yaml:"snmp_port,omitempty" json:"snmp_port,omitempty"`           // Default: 161
}

// OutletRef is a parsed "pdu-id:outlet" reference
type OutletRef struct {
	PDUID  string
	Outlet int
}

func (o OutletRef) String() string { return fmt.Sprintf("%s:%d", o.PDUID, o.Outlet) }

func parseOutletRef(ref string) (OutletRef, error) {
	pduID, num, ok := strings.Cut(strings.TrimSpace(ref), ":")
	if !ok || pduID == "" {
		return OutletRef{}, fmt.Errorf("invalid outlet %q, expected 'pdu-id:outlet-number'", ref)
	}
	outlet, err := strconv.Atoi(num)
	if err != nil || outlet < 1 {
		return OutletRef{}, fmt.Errorf("invalid outlet number in %q", ref)
	}
	return OutletRef{PDUID: pduID, Outlet: outlet}, nil
}

// HasBMC reports whether the server has a BMC configured
func (s *Server) HasBMC() bool { return s.IPMI.Host != "" }

// Outlets returns the server's parsed PDU outlets
func (s *Server) Outlets() []OutletRef { return s.outlets }

// MAC returns the server's boot MAC, normalized
func (s *Server) MAC() string { return normalizeMAC(s.Metadata["mac"]) }

// Hostname returns metadata hostname, falling back to the server name
func (s *Server) Hostname() string {
	if h := s.Metadata["hostname"]; h != "" {
		return h
	}
	return s.Name
}

// normalizeMAC lowercases a MAC and uses colons, so "AA-BB-..." and "aa:bb:..." match
func normalizeMAC(mac string) string {
	return strings.ReplaceAll(strings.ToLower(strings.TrimSpace(mac)), "-", ":")
}

// Server looks up a server by ID
func (c *Config) Server(id string) *Server {
	for i := range c.Servers {
		if c.Servers[i].ID == id {
			return &c.Servers[i]
		}
	}
	return nil
}

// PDU looks up a PDU by ID
func (c *Config) PDU(id string) *PDU {
	for i := range c.PDUs {
		if c.PDUs[i].ID == id {
			return &c.PDUs[i]
		}
	}
	return nil
}

// ServerForOutlet finds the server an outlet belongs to, if any
func (c *Config) ServerForOutlet(ref OutletRef) *Server {
	for i := range c.Servers {
		for _, o := range c.Servers[i].outlets {
			if o == ref {
				return &c.Servers[i]
			}
		}
	}
	return nil
}

// Public returns a copy of the config that is safe to send to clients: credentials are removed
func (c *Config) Public() *Config {
	public := &Config{Delays: c.Delays}
	for _, server := range c.Servers {
		server.IPMI.Password = ""
		public.Servers = append(public.Servers, server)
	}
	for _, pdu := range c.PDUs {
		pdu.Password = ""
		pdu.SNMPCommunity = ""
		public.PDUs = append(public.PDUs, pdu)
	}
	return public
}

func parseConfig(data []byte) (*Config, error) {
	var config Config
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, err
	}
	if err := validateConfig(&config); err != nil {
		return nil, err
	}
	return &config, nil
}

func validateConfig(config *Config) error {
	pduIDs := make(map[string]bool)
	for i := range config.PDUs {
		pdu := &config.PDUs[i]
		if pdu.ID == "" {
			return fmt.Errorf("pdu[%d]: ID cannot be empty", i)
		}
		if pduIDs[pdu.ID] {
			return fmt.Errorf("pdu[%d]: duplicate ID '%s'", i, pdu.ID)
		}
		pduIDs[pdu.ID] = true

		if pdu.Host == "" {
			return fmt.Errorf("pdu[%d] '%s': host cannot be empty", i, pdu.ID)
		}
		if pdu.Type == "" {
			return fmt.Errorf("pdu[%d] '%s': type cannot be empty", i, pdu.ID)
		}
		switch pdu.SNMPVersion {
		case "", "v1", "v2c":
		default:
			return fmt.Errorf("pdu[%d] '%s': unsupported snmp_version %q (use v1 or v2c)", i, pdu.ID, pdu.SNMPVersion)
		}
	}

	for _, kind := range config.ConsoleCapture.After {
		if _, ok := opLabels[kind]; !ok {
			return fmt.Errorf("console_capture.after: unknown operation %q", kind)
		}
	}

	serverIDs := make(map[string]bool)
	macs := make(map[string]string)
	for i := range config.Servers {
		server := &config.Servers[i]
		if server.ID == "" {
			return fmt.Errorf("server[%d]: ID cannot be empty", i)
		}
		if serverIDs[server.ID] {
			return fmt.Errorf("server[%d]: duplicate ID '%s'", i, server.ID)
		}
		serverIDs[server.ID] = true
		if server.Name == "" {
			server.Name = server.ID
		}

		switch server.Console.Capture {
		case "", "off", "always":
		default:
			return fmt.Errorf("server[%d] '%s': console.capture must be off or always, not %q", i, server.ID, server.Console.Capture)
		}

		if !server.HasBMC() && len(server.PDUOutlets) == 0 {
			return fmt.Errorf("server[%d] '%s': must have either IPMI host or PDU outlets configured", i, server.ID)
		}

		server.outlets = nil
		for j, raw := range server.PDUOutlets {
			ref, err := parseOutletRef(raw)
			if err != nil {
				return fmt.Errorf("server[%d] '%s' pdu_outlets[%d]: %v", i, server.ID, j, err)
			}
			if !pduIDs[ref.PDUID] {
				return fmt.Errorf("server[%d] '%s': references unknown PDU '%s'", i, server.ID, ref.PDUID)
			}
			server.outlets = append(server.outlets, ref)
		}

		if mac := server.MAC(); mac != "" {
			if other, dup := macs[mac]; dup {
				return fmt.Errorf("server '%s': MAC %s already used by '%s'", server.ID, mac, other)
			}
			macs[mac] = server.ID
		}
	}

	return nil
}

// ConfigStore holds the current config and reloads it when the file changes
type ConfigStore struct {
	path string

	mu       sync.RWMutex
	config   *Config
	modTime  time.Time
	onReload []func(*Config)
}

func NewConfigStore(path string) (*ConfigStore, error) {
	s := &ConfigStore{path: path}
	if err := s.Reload(); err != nil {
		return nil, err
	}
	return s, nil
}

// Get returns the current config. Treat it as read-only.
func (s *ConfigStore) Get() *Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.config
}

// OnReload registers a callback run after each successful reload
func (s *ConfigStore) OnReload(fn func(*Config)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onReload = append(s.onReload, fn)
}

// Reload re-reads the config file. On error the previous config stays active.
func (s *ConfigStore) Reload() error {
	info, err := os.Stat(s.path)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(s.path)
	if err != nil {
		return err
	}
	config, err := parseConfig(data)
	if err != nil {
		return fmt.Errorf("%s: %w", s.path, err)
	}

	s.mu.Lock()
	first := s.config == nil
	s.config = config
	s.modTime = info.ModTime()
	callbacks := append([]func(*Config){}, s.onReload...)
	s.mu.Unlock()

	if !first {
		log.Printf("Config reloaded: %d servers, %d PDUs", len(config.Servers), len(config.PDUs))
	}
	for _, fn := range callbacks {
		fn(config)
	}
	return nil
}

// reloadIfChanged reloads when the file's modification time has moved
func (s *ConfigStore) reloadIfChanged() {
	info, err := os.Stat(s.path)
	if err != nil {
		return
	}
	s.mu.RLock()
	changed := !info.ModTime().Equal(s.modTime)
	s.mu.RUnlock()
	if changed {
		if err := s.Reload(); err != nil {
			log.Printf("Config reload failed, keeping previous config: %v", err)
			// Don't retry the same broken file every tick
			s.mu.Lock()
			s.modTime = info.ModTime()
			s.mu.Unlock()
		}
	}
}
