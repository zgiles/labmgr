package main

import (
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"
)

const (
	ReachableInterval   = 30 * time.Second  // Poll reachable servers every 30s (reduced from 15s)
	UnreachableInterval = 120 * time.Second // Poll unreachable servers every 2 minutes (reduced from 60s)
)

func startStatusPoller() {
	log.Println("Starting background status poller...")
	
	go func() {
		// Initial poll of all servers
		pollAllServers()
		
		// Set up ticker for regular polling
		ticker := time.NewTicker(15 * time.Second) // Check every 15 seconds which servers need polling (reduced frequency)
		defer ticker.Stop()
		
		for range ticker.C {
			pollServersIfNeeded()
		}
	}()
}

func pollAllServers() {
	config, err := loadConfig()
	if err != nil {
		log.Printf("Error loading config for polling: %v", err)
		return
	}
	
	// Use bulk PDU polling to reduce SNMP load
	go bulkPollPDUs(config)
	
	// Poll IPMI systems individually (still parallel)
	for _, server := range config.Servers {
		go pollServerIPMI(server.ID)
	}
}

func pollServersIfNeeded() {
	config, err := loadConfig()
	if err != nil {
		log.Printf("Error loading config for polling: %v", err)
		return
	}
	
	now := time.Now()
	
	// Collect servers that need PDU polling, grouped by PDU
	pduServersToCheck := make(map[string][]string) // pduID -> serverIDs
	
	for _, server := range config.Servers {
		cachedStatus, exists := cache.Get(server.ID)
		
		var shouldPoll bool
		if !exists {
			// Never polled before
			shouldPoll = true
		} else {
			var interval time.Duration
			if cachedStatus.IsReachable {
				interval = ReachableInterval
			} else {
				interval = UnreachableInterval
			}
			
			shouldPoll = now.Sub(cachedStatus.LastChecked) >= interval
		}
		
		if shouldPoll {
			// Poll IPMI individually
			go pollServerIPMI(server.ID)
			
			// Group PDU polling by PDU ID
			for _, outlet := range server.PDUOutlets {
				parts := strings.Split(outlet, ":")
				if len(parts) == 2 {
					pduID := parts[0]
					pduServersToCheck[pduID] = append(pduServersToCheck[pduID], server.ID)
				}
			}
		}
	}
	
	// Bulk poll PDUs that need checking
	for pduID, serverIDs := range pduServersToCheck {
		go bulkPollPDUByID(config, pduID, serverIDs)
	}
}

func pollServer(serverID string) {
	log.Printf("Polling server %s", serverID)
	
	systemStatus, powerStatus, err := getDeviceStatusDual(serverID)
	
	// Determine overall reachability - device is reachable if either system or power is reachable
	isReachable := (systemStatus != "unreachable" && systemStatus != "no_ipmi") || 
	              (powerStatus != "unreachable" && powerStatus != "no_pdu")
	
	if err != nil {
		log.Printf("Error polling server %s: %v", serverID, err)
		// Don't immediately mark as unreachable - individual status checks handle their own errors
		// Only mark as unreachable if BOTH IPMI and PDU fail
		cache.UpdateDualStatus(serverID, systemStatus, powerStatus, isReachable, err)
	} else {
		// Create a descriptive status message for logging
		var logStatus string
		if systemStatus != "unreachable" && systemStatus != "no_ipmi" && powerStatus != "unreachable" && powerStatus != "no_pdu" {
			logStatus = fmt.Sprintf("system:%s, power:%s", systemStatus, powerStatus)
		} else if systemStatus != "unreachable" && systemStatus != "no_ipmi" {
			logStatus = fmt.Sprintf("system:%s (no PDU)", systemStatus)
		} else if powerStatus != "unreachable" && powerStatus != "no_pdu" {
			logStatus = fmt.Sprintf("power:%s (no IPMI)", powerStatus)
		} else {
			logStatus = "unreachable"
		}
		
		log.Printf("Server %s status: %s (reachable: %v)", serverID, logStatus, isReachable)
		cache.UpdateDualStatus(serverID, systemStatus, powerStatus, isReachable, nil)
	}
}

// pollServerIPMI polls only the IPMI status of a server
func pollServerIPMI(serverID string) {
	config, err := loadConfig()
	if err != nil {
		log.Printf("Error loading config for IPMI polling: %v", err)
		return
	}

	var server *Server
	for _, s := range config.Servers {
		if s.ID == serverID {
			server = &s
			break
		}
	}

	if server == nil || server.IPMI.Host == "" {
		return // No IPMI to poll
	}

	systemStatus, err := getIPMIStatusInternal(serverID)
	if err != nil {
		systemStatus = "unreachable"
	}

	// Update cache with IPMI status only
	// The PDU status will be updated separately by bulk polling
	cachedStatus, exists := cache.Get(serverID)
	if exists {
		cache.UpdateDualStatus(serverID, systemStatus, cachedStatus.PowerStatus, 
			(systemStatus != "unreachable" && systemStatus != "no_ipmi") || 
			(cachedStatus.PowerStatus != "unreachable" && cachedStatus.PowerStatus != "no_pdu"), nil)
	} else {
		// First time - assume no PDU for now, bulk polling will update
		cache.UpdateDualStatus(serverID, systemStatus, "no_pdu", 
			systemStatus != "unreachable" && systemStatus != "no_ipmi", nil)
	}
}

// bulkPollPDUs polls all PDUs with bulk SNMP queries
func bulkPollPDUs(config *Config) {
	// Group outlets by PDU
	pduOutlets := make(map[string][]int)
	pduServerMap := make(map[string][]string) // pduID:outlet -> serverIDs

	for _, server := range config.Servers {
		for _, outlet := range server.PDUOutlets {
			parts := strings.Split(outlet, ":")
			if len(parts) == 2 {
				pduID := parts[0]
				outletNum, err := strconv.Atoi(parts[1])
				if err != nil {
					continue
				}
				
				pduOutlets[pduID] = append(pduOutlets[pduID], outletNum)
				key := fmt.Sprintf("%s:%d", pduID, outletNum)
				pduServerMap[key] = append(pduServerMap[key], server.ID)
			}
		}
	}

	// Bulk query each PDU
	for pduID, outlets := range pduOutlets {
		go bulkQueryPDU(config, pduID, outlets, pduServerMap)
	}
}

// bulkPollPDUByID polls a specific PDU for specific servers
func bulkPollPDUByID(config *Config, pduID string, serverIDs []string) {
	// Collect outlets to check for this PDU
	outlets := make([]int, 0)
	pduServerMap := make(map[string][]string)

	for _, serverID := range serverIDs {
		var server *Server
		for _, s := range config.Servers {
			if s.ID == serverID {
				server = &s
				break
			}
		}
		
		if server == nil {
			continue
		}

		for _, outlet := range server.PDUOutlets {
			parts := strings.Split(outlet, ":")
			if len(parts) == 2 && parts[0] == pduID {
				outletNum, err := strconv.Atoi(parts[1])
				if err != nil {
					continue
				}
				
				outlets = append(outlets, outletNum)
				key := fmt.Sprintf("%s:%d", pduID, outletNum)
				pduServerMap[key] = append(pduServerMap[key], serverID)
			}
		}
	}

	if len(outlets) > 0 {
		bulkQueryPDU(config, pduID, outlets, pduServerMap)
	}
}

// bulkQueryPDU performs the actual bulk SNMP query
func bulkQueryPDU(config *Config, pduID string, outlets []int, pduServerMap map[string][]string) {
	// Find PDU configuration
	var pdu *PDU
	for _, p := range config.PDUs {
		if p.ID == pduID {
			pdu = &p
			break
		}
	}

	if pdu == nil {
		log.Printf("PDU %s not found in config", pduID)
		return
	}

	// Remove duplicates from outlets
	uniqueOutlets := make([]int, 0)
	seen := make(map[int]bool)
	for _, outlet := range outlets {
		if !seen[outlet] {
			uniqueOutlets = append(uniqueOutlets, outlet)
			seen[outlet] = true
		}
	}

	log.Printf("Bulk polling PDU %s for outlets %v", pduID, uniqueOutlets)

	// Use the new bulk PDU status function
	outletStatuses, err := getBulkPDUStatus(pdu, uniqueOutlets)
	if err != nil {
		log.Printf("Failed to bulk poll PDU %s: %v", pduID, err)
		
		// Mark all servers using this PDU as having unreachable power status
		for _, outlet := range uniqueOutlets {
			key := fmt.Sprintf("%s:%d", pduID, outlet)
			for _, serverID := range pduServerMap[key] {
				cachedStatus, exists := cache.Get(serverID)
				systemStatus := "no_ipmi"
				if exists {
					systemStatus = cachedStatus.SystemStatus
				}
				
				cache.UpdateDualStatus(serverID, systemStatus, "unreachable", 
					systemStatus != "unreachable" && systemStatus != "no_ipmi", err)
			}
		}
		return
	}

	// Update status for each server
	for outlet, status := range outletStatuses {
		key := fmt.Sprintf("%s:%d", pduID, outlet)
		for _, serverID := range pduServerMap[key] {
			cachedStatus, exists := cache.Get(serverID)
			systemStatus := "no_ipmi"
			if exists {
				systemStatus = cachedStatus.SystemStatus
			}

			// Determine overall reachability
			isReachable := (systemStatus != "unreachable" && systemStatus != "no_ipmi") || 
						 (status != "unreachable" && status != "no_pdu")

			cache.UpdateDualStatus(serverID, systemStatus, status, isReachable, nil)
		}
	}

	log.Printf("Successfully bulk polled PDU %s: %d outlets updated", pduID, len(outletStatuses))
}

// schedulePostActionPolling schedules immediate polling after user actions
func schedulePostActionPolling(serverID string) {
	log.Printf("Scheduling post-action polling for server %s", serverID)
	
	// Poll immediately (bypassing cache)
	go pollServerFresh(serverID)
	
	// Schedule follow-up polls to catch state changes
	go func() {
		time.Sleep(3 * time.Second)
		pollServerFresh(serverID)
		
		time.Sleep(7 * time.Second) // 10s total
		pollServerFresh(serverID)
		
		time.Sleep(20 * time.Second) // 30s total
		pollServerFresh(serverID)
	}()
}

// pollServerFresh polls a server with cache bypass for fresh data
func pollServerFresh(serverID string) {
	log.Printf("Fresh polling server %s (bypassing cache)", serverID)
	
	config, err := loadConfig()
	if err != nil {
		log.Printf("Error loading config for fresh polling: %v", err)
		return
	}

	var server *Server
	for _, s := range config.Servers {
		if s.ID == serverID {
			server = &s
			break
		}
	}

	if server == nil {
		log.Printf("Server %s not found for fresh polling", serverID)
		return
	}

	// Get fresh IPMI status
	var systemStatus string
	if server.IPMI.Host != "" {
		systemStatus, err = getIPMIStatusInternal(serverID)
		if err != nil {
			systemStatus = "unreachable"
		}
	} else {
		systemStatus = "no_ipmi"
	}

	// Get fresh PDU status (bypassing cache)
	var powerStatus string
	if len(server.PDUOutlets) > 0 {
		powerStatus, err = getPDUStatusForServerFresh(serverID)
		if err != nil {
			powerStatus = "unreachable"
		}
	} else {
		powerStatus = "no_pdu"
	}

	// Determine overall reachability
	isReachable := (systemStatus != "unreachable" && systemStatus != "no_ipmi") || 
	              (powerStatus != "unreachable" && powerStatus != "no_pdu")

	cache.UpdateDualStatus(serverID, systemStatus, powerStatus, isReachable, nil)
	
	log.Printf("Fresh poll complete for %s: system=%s, power=%s", serverID, systemStatus, powerStatus)
}

// refreshAllServersFresh forces fresh polling of all servers (for manual refresh)
func refreshAllServersFresh() {
	log.Printf("Manual refresh: clearing PDU cache and polling all servers fresh")
	
	// Clear PDU cache to force fresh queries
	clearPDUCache()
	
	config, err := loadConfig()
	if err != nil {
		log.Printf("Error loading config for manual refresh: %v", err)
		return
	}
	
	// Poll all servers with fresh data in parallel
	for _, server := range config.Servers {
		go pollServerFresh(server.ID)
	}
}

// getPDUStatusForServerFresh gets PDU status with cache bypass
func getPDUStatusForServerFresh(serverID string) (string, error) {
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

	// Get fresh PDU outlet status (bypassing cache)
	statusMap, err := getBulkPDUStatusFresh(pdu, []int{outlet})
	if err != nil {
		return "", err
	}
	
	if status, exists := statusMap[outlet]; exists {
		return status, nil
	}
	
	return "", fmt.Errorf("outlet %d status not found", outlet)
}

// getIPMIStatusDirect is the same as getIPMIStatus but doesn't cache the result
func getIPMIStatusDirect(serverID string) (string, error) {
	return getIPMIStatusInternal(serverID)
}