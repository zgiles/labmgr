package main

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/stmcginnis/gofish"
	"github.com/stmcginnis/gofish/redfish"
)

// validIPMIActions are the actions performIPMIAction understands
var validIPMIActions = map[string]bool{
	"on":                true,
	"off":               true,
	"reboot":            true,
	"graceful_shutdown": true,
	"graceful_restart":  true,
}

func performIPMIAction(serverID, action string) error {
	if !validIPMIActions[action] {
		return fmt.Errorf("unknown action: %s", action)
	}

	config, err := loadConfig()
	if err != nil {
		return err
	}

	var server *Server
	for _, s := range config.Servers {
		if s.ID == serverID {
			server = &s
			break
		}
	}

	if server == nil {
		return fmt.Errorf("server %s not found", serverID)
	}

	clientConfig := gofish.ClientConfig{
		Endpoint: fmt.Sprintf("https://%s", server.IPMI.Host),
		Username: server.IPMI.Username,
		Password: server.IPMI.Password,
		Insecure: true,
	}

	// Bound the whole action so a hung BMC can't stall the job processor
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	c, err := gofish.ConnectContext(ctx, clientConfig)
	if err != nil {
		return fmt.Errorf("failed to connect to IPMI: %v", err)
	}
	defer c.Logout()

	service := c.Service
	systems, err := service.Systems()
	if err != nil {
		return fmt.Errorf("failed to get systems: %v", err)
	}

	if len(systems) == 0 {
		return fmt.Errorf("no systems found")
	}

	system := systems[0]

	switch action {
	case "on":
		return system.Reset(redfish.OnResetType)
	case "off":
		return system.Reset(redfish.ForceOffResetType)
	case "reboot":
		return system.Reset(redfish.ForceRestartResetType)
	case "graceful_shutdown":
		return system.Reset(redfish.GracefulShutdownResetType)
	case "graceful_restart":
		return system.Reset(redfish.GracefulRestartResetType)
	default:
		return fmt.Errorf("unknown action: %s", action)
	}
}

func getIPMIStatusInternal(serverID string) (string, error) {
	config, err := loadConfig()
	if err != nil {
		return "", err
	}

	var server *Server
	for _, s := range config.Servers {
		if s.ID == serverID {
			server = &s
			break
		}
	}

	if server == nil {
		return "", fmt.Errorf("server %s not found", serverID)
	}

	clientConfig := gofish.ClientConfig{
		Endpoint: fmt.Sprintf("https://%s", server.IPMI.Host),
		Username: server.IPMI.Username,
		Password: server.IPMI.Password,
		Insecure: true,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	c, err := gofish.ConnectContext(ctx, clientConfig)
	if err != nil {
		return "unreachable", nil
	}
	defer c.Logout()

	service := c.Service
	systems, err := service.Systems()
	if err != nil {
		return "error", err
	}

	if len(systems) == 0 {
		return "no_systems", nil
	}

	system := systems[0]
	return string(system.PowerState), nil
}

// getDeviceStatusDual returns both system (IPMI) and power (PDU) status
func getDeviceStatusDual(serverID string) (systemStatus, powerStatus string, err error) {
	config, err := loadConfig()
	if err != nil {
		return "", "", err
	}

	var server *Server
	for _, s := range config.Servers {
		if s.ID == serverID {
			server = &s
			break
		}
	}

	if server == nil {
		return "", "", fmt.Errorf("server %s not found", serverID)
	}

	// Get IPMI/system status if configured
	if server.IPMI.Host != "" {
		systemStatus, err = getIPMIStatusInternal(serverID)
		if err != nil || systemStatus == "unreachable" {
			systemStatus = "unreachable"
		}
	} else {
		systemStatus = "no_ipmi"
	}

	// Get PDU/power status if configured
	if len(server.PDUOutlets) > 0 {
		powerStatus, err = getPDUStatusForServer(serverID)
		if err != nil {
			powerStatus = "unreachable"
		}
	} else {
		powerStatus = "no_pdu"
	}

	return systemStatus, powerStatus, nil
}

// getDeviceStatus is the smart router that decides between IPMI and PDU status (legacy)
func getDeviceStatus(serverID string) (string, error) {
	systemStatus, powerStatus, err := getDeviceStatusDual(serverID)
	if err != nil {
		return "", err
	}

	// Return system status if available, otherwise fall back to power status
	if systemStatus != "unreachable" && systemStatus != "no_ipmi" {
		return systemStatus, nil
	}
	
	if powerStatus != "unreachable" && powerStatus != "no_pdu" {
		return powerStatus, nil
	}
	
	return "unreachable", nil
}

// getPDUStatusForServer gets PDU status for a server's first outlet
func getPDUStatusForServer(serverID string) (string, error) {
	config, err := loadConfig()
	if err != nil {
		return "", err
	}

	var server *Server
	for _, s := range config.Servers {
		if s.ID == serverID {
			server = &s
			break
		}
	}

	if server == nil {
		return "", fmt.Errorf("server %s not found", serverID)
	}

	if len(server.PDUOutlets) == 0 {
		return "", fmt.Errorf("server %s has no PDU outlets configured", serverID)
	}

	// Parse first PDU outlet (format: "pdu-id:outlet")
	parts := strings.Split(server.PDUOutlets[0], ":")
	if len(parts) != 2 {
		return "", fmt.Errorf("invalid PDU outlet format: %s", server.PDUOutlets[0])
	}

	pduID := parts[0]
	outlet, err := strconv.Atoi(parts[1])
	if err != nil {
		return "", fmt.Errorf("invalid outlet number: %s", parts[1])
	}

	// Find the PDU configuration
	var pdu *PDU
	for _, p := range config.PDUs {
		if p.ID == pduID {
			pdu = &p
			break
		}
	}

	if pdu == nil {
		return "", fmt.Errorf("PDU %s not found", pduID)
	}

	// Get PDU outlet status
	return getPDUOutletStatus(pdu, outlet)
}

// getDeviceStatusDirect is like getDeviceStatus but doesn't use cache
func getDeviceStatusDirect(serverID string) (string, error) {
	return getDeviceStatus(serverID)
}

// getIPMIStatus now returns cached data, including transitional states
func getIPMIStatus(serverID string) (string, error) {
	if cachedStatus, exists := cache.Get(serverID); exists {
		// If it's transitional and not expired, return transitional status
		if cachedStatus.IsTransitional {
			// Keep transitional status for up to 2 minutes
			if time.Since(cachedStatus.TransitionalSince) < 2*time.Minute {
				return cachedStatus.Status, nil
			}
			// Transitional expired, fall through to get real status
		} else {
			// Normal cached status
			if cachedStatus.Error != "" {
				return cachedStatus.Status, fmt.Errorf(cachedStatus.Error)
			}
			return cachedStatus.Status, nil
		}
	}
	
	// If not in cache or transitional expired, poll directly once
	return getIPMIStatusInternal(serverID)
}