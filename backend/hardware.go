package main

import (
	"context"
	"sync"
)

// PowerState is a normalized power state for a system or an outlet
type PowerState string

const (
	PowerOn      PowerState = "on"
	PowerOff     PowerState = "off"
	PowerUnknown PowerState = "unknown"
)

// BMC actions understood by BMC.Reset
const (
	ActionOn               = "on"
	ActionOff              = "off"
	ActionReboot           = "reboot" // hard reset
	ActionGracefulShutdown = "graceful_shutdown"
	ActionGracefulRestart  = "graceful_restart"
	ActionPowerCycle       = "power_cycle"
	ActionNMI              = "nmi"
)

// validIPMIActions are the actions a BMC step can perform
var validIPMIActions = map[string]bool{
	ActionOn:               true,
	ActionOff:              true,
	ActionReboot:           true,
	ActionGracefulShutdown: true,
	ActionGracefulRestart:  true,
	ActionPowerCycle:       true,
	ActionNMI:              true,
}

// Boot override targets
const (
	BootPXE  = "pxe"
	BootDisk = "disk"
	BootBIOS = "bios"
	BootCD   = "cd"
	BootNone = "none"
)

var validBootTargets = map[string]bool{BootPXE: true, BootDisk: true, BootBIOS: true, BootCD: true, BootNone: true}

// BootOverride is a BMC's current boot source override
type BootOverride struct {
	Target  string `json:"target"`  // pxe, disk, bios, cd, none, or the raw Redfish value
	Enabled string `json:"enabled"` // Disabled, Once, Continuous
	Mode    string `json:"mode,omitempty"`
}

// BMCLogEntry is one entry from the BMC's event log
type BMCLogEntry struct {
	Created  string `json:"created"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
}

// BMCInfo is descriptive data about a server's BMC and system
type BMCInfo struct {
	Manufacturer string `json:"manufacturer,omitempty"`
	Model        string `json:"model,omitempty"`
	SerialNumber string `json:"serial_number,omitempty"`
	BIOSVersion  string `json:"bios_version,omitempty"`
	Health       string `json:"health,omitempty"`
	BMCModel     string `json:"bmc_model,omitempty"`
	BMCFirmware  string `json:"bmc_firmware,omitempty"`
	SerialSSH    bool   `json:"serial_ssh"`
	SerialIPMI   bool   `json:"serial_ipmi"`
	GraphicalKVM bool   `json:"graphical_kvm"`
}

// BMC controls one server's baseboard management controller
type BMC interface {
	PowerState(ctx context.Context) (PowerState, error)
	Reset(ctx context.Context, action string) error
	BootOverride(ctx context.Context) (BootOverride, error)
	SetBootOverride(ctx context.Context, target string, persistent bool) error
	Info(ctx context.Context) (BMCInfo, error)
	Logs(ctx context.Context, limit int) ([]BMCLogEntry, error)
}

// PDUDriver controls one PDU's outlets
type PDUDriver interface {
	OutletStates(ctx context.Context, outlets []int) (map[int]PowerState, error)
	SetOutlet(ctx context.Context, outlet int, action string) error
}

// Hardware hands out drivers for configured devices; tests substitute a fake
type Hardware interface {
	BMC(server *Server) BMC
	PDU(pdu *PDU) PDUDriver
}

type realHardware struct {
	mu      sync.Mutex
	pduLock map[string]*sync.Mutex
}

func newRealHardware() *realHardware {
	return &realHardware{pduLock: make(map[string]*sync.Mutex)}
}

func (h *realHardware) BMC(server *Server) BMC {
	if !server.HasBMC() {
		return nil
	}
	return &redfishBMC{config: server.IPMI}
}

// PDU returns a driver for the PDU. Calls to the same PDU are serialized,
// since these PDUs handle concurrent SNMP requests poorly.
func (h *realHardware) PDU(pdu *PDU) PDUDriver {
	h.mu.Lock()
	lock, ok := h.pduLock[pdu.ID]
	if !ok {
		lock = &sync.Mutex{}
		h.pduLock[pdu.ID] = lock
	}
	h.mu.Unlock()

	var driver PDUDriver
	if pdu.Type == "apc" {
		driver = &apcSNMP{pdu: *pdu}
	} else {
		driver = &httpPDU{pdu: *pdu}
	}
	return &serializedPDU{lock: lock, driver: driver}
}

type serializedPDU struct {
	lock   *sync.Mutex
	driver PDUDriver
}

func (s *serializedPDU) OutletStates(ctx context.Context, outlets []int) (map[int]PowerState, error) {
	s.lock.Lock()
	defer s.lock.Unlock()
	return s.driver.OutletStates(ctx, outlets)
}

func (s *serializedPDU) SetOutlet(ctx context.Context, outlet int, action string) error {
	s.lock.Lock()
	defer s.lock.Unlock()
	return s.driver.SetOutlet(ctx, outlet, action)
}
