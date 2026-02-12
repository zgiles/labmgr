package main

import (
	"sync"
	"time"
)

type ServerStatus struct {
	ServerID      string    `json:"server_id"`
	Status        string    `json:"status"` // Legacy field for backward compatibility
	SystemStatus  string    `json:"system_status"` // IPMI/BMC status (on/off/unreachable)
	PowerStatus   string    `json:"power_status"`  // PDU power status (on/off/unreachable)
	LastChecked   time.Time `json:"last_checked"`
	IsReachable   bool      `json:"is_reachable"`
	Error         string    `json:"error,omitempty"`
	IsTransitional bool     `json:"is_transitional"`
	TransitionalSince time.Time `json:"transitional_since,omitempty"`
	PreviousStatus string    `json:"previous_status,omitempty"` // Status before transitional state
	
	// Communication status tracking
	IPMIReachable bool   `json:"ipmi_reachable"`  // Is IPMI accessible
	PDUReachable  bool   `json:"pdu_reachable"`   // Is PDU accessible
	IPMIError     string `json:"ipmi_error,omitempty"` // Last IPMI error
	PDUError      string `json:"pdu_error,omitempty"`  // Last PDU error
}

type StatusCache struct {
	mu      sync.RWMutex
	servers map[string]*ServerStatus
}

var cache *StatusCache

func init() {
	cache = &StatusCache{
		servers: make(map[string]*ServerStatus),
	}
}

func (c *StatusCache) Get(serverID string) (*ServerStatus, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	status, exists := c.servers[serverID]
	return status, exists
}

func (c *StatusCache) Set(serverID string, status *ServerStatus) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.servers[serverID] = status
}

func (c *StatusCache) GetAll() map[string]*ServerStatus {
	c.mu.RLock()
	defer c.mu.RUnlock()
	
	result := make(map[string]*ServerStatus)
	for k, v := range c.servers {
		result[k] = v
	}
	return result
}

func (c *StatusCache) UpdateStatus(serverID, status string, isReachable bool, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	
	existing := c.servers[serverID]
	statusEntry := &ServerStatus{
		ServerID:    serverID,
		Status:      status, // Legacy field
		LastChecked: time.Now(),
		IsReachable: isReachable,
	}
	
	// Preserve existing power status when updating system status
	if existing != nil {
		statusEntry.PowerStatus = existing.PowerStatus
	}
	
	// Only clear transitional state if the new status is different from the previous status
	// This keeps "Powering On..." while BMC still reports "Off", until BMC actually reports "On"
	if existing != nil && existing.IsTransitional {
		if status != existing.PreviousStatus {
			// BMC status changed from what it was before the action - clear transitional
			statusEntry.IsTransitional = false
		} else {
			// BMC still shows old status - keep transitional state
			statusEntry.IsTransitional = true
			statusEntry.TransitionalSince = existing.TransitionalSince
			statusEntry.PreviousStatus = existing.PreviousStatus
			statusEntry.Status = existing.Status // Keep the transitional status text
		}
	} else {
		// Not transitional, normal update
		statusEntry.IsTransitional = false
	}
	
	if err != nil {
		statusEntry.Error = err.Error()
	}
	
	c.servers[serverID] = statusEntry
}

// UpdateDualStatus updates both system and power status separately
func (c *StatusCache) UpdateDualStatus(serverID, systemStatus, powerStatus string, isReachable bool, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	
	existing := c.servers[serverID]
	statusEntry := &ServerStatus{
		ServerID:     serverID,
		SystemStatus: systemStatus,
		PowerStatus:  powerStatus,
		LastChecked:  time.Now(),
		IsReachable:  isReachable,
	}
	
	// Set communication reachability flags
	statusEntry.IPMIReachable = (systemStatus != "unreachable" && systemStatus != "no_ipmi")
	statusEntry.PDUReachable = (powerStatus != "unreachable" && powerStatus != "no_pdu")
	
	// Store communication errors
	if systemStatus == "unreachable" && err != nil {
		statusEntry.IPMIError = err.Error()
	} else {
		statusEntry.IPMIError = ""
	}
	
	// Determine device status based on best available information
	if systemStatus != "unreachable" && systemStatus != "no_ipmi" {
		// IPMI is working - use IPMI status (most reliable)
		statusEntry.Status = systemStatus
		statusEntry.IsReachable = true
	} else if systemStatus == "no_ipmi" && powerStatus != "unreachable" && powerStatus != "no_pdu" {
		// PDU-only device (no IPMI configured) - use PDU status directly
		statusEntry.Status = powerStatus
		statusEntry.IsReachable = true
	} else if powerStatus == "on" {
		// IPMI+PDU device where IPMI is failing but PDU shows device is powered
		// Show "powered" to indicate PDU working but system status unknown due to IPMI failure
		statusEntry.Status = "powered" // Device is powered via PDU, but IPMI communication failing
		statusEntry.IsReachable = true  // Device is reachable via PDU
	} else if powerStatus == "off" {
		// Device is off (from PDU status)
		statusEntry.Status = "off"
		statusEntry.IsReachable = true  // PDU is reachable
	} else if powerStatus != "unreachable" && powerStatus != "no_pdu" {
		// PDU status available but not standard on/off
		statusEntry.Status = powerStatus
		statusEntry.IsReachable = true
	} else if systemStatus == "no_ipmi" && powerStatus == "no_pdu" {
		// Device has neither IPMI nor PDU configured - this is a config issue
		statusEntry.Status = "no control configured"
		statusEntry.IsReachable = false
	} else {
		// Both IPMI and PDU are truly unreachable - unknown device state
		statusEntry.Status = "unreachable"
		statusEntry.IsReachable = false
	}
	
	// Handle transitional state
	if existing != nil && existing.IsTransitional {
		if systemStatus != existing.PreviousStatus {
			statusEntry.IsTransitional = false
		} else {
			statusEntry.IsTransitional = true
			statusEntry.TransitionalSince = existing.TransitionalSince
			statusEntry.PreviousStatus = existing.PreviousStatus
			statusEntry.Status = existing.Status
		}
	} else {
		statusEntry.IsTransitional = false
	}
	
	if err != nil {
		statusEntry.Error = err.Error()
	}
	
	c.servers[serverID] = statusEntry
}

func (c *StatusCache) SetTransitionalStatus(serverID, action string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	
	var transitionalStatus string
	switch action {
	case "on":
		transitionalStatus = "powered" // Changed from "Powering On..." to match powered state
	case "off":
		transitionalStatus = "Powering Off..."
	case "reboot":
		transitionalStatus = "Rebooting..."
	default:
		transitionalStatus = "Pending..."
	}
	
	existing := c.servers[serverID]
	statusEntry := &ServerStatus{
		ServerID:          serverID,
		Status:            transitionalStatus,
		LastChecked:       time.Now(),
		IsReachable:       true,
		IsTransitional:    true,
		TransitionalSince: time.Now(),
	}
	
	// Preserve some existing data if available and store previous status
	if existing != nil {
		statusEntry.IsReachable = existing.IsReachable
		statusEntry.PreviousStatus = existing.Status // Store the status before transitional
	}
	
	c.servers[serverID] = statusEntry
}

func (c *StatusCache) ClearTransitionalStatus(serverID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	
	existing := c.servers[serverID]
	if existing != nil && existing.IsTransitional {
		// Reset to "pending" status to force re-discovery
		statusEntry := &ServerStatus{
			ServerID:    serverID,
			Status:      "pending",
			LastChecked: time.Now(),
			IsReachable: true,
			IsTransitional: false,
		}
		c.servers[serverID] = statusEntry
	}
}