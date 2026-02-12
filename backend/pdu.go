package main

import (
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gosnmp/gosnmp"
)

func performPDUAction(outlet, action string) error {
	parts := strings.Split(outlet, ":")
	if len(parts) != 2 {
		return fmt.Errorf("invalid outlet format, expected pdu_id:outlet_number")
	}

	pduID := parts[0]
	outletNum, err := strconv.Atoi(parts[1])
	if err != nil {
		return fmt.Errorf("invalid outlet number: %v", err)
	}

	config, err := loadConfig()
	if err != nil {
		return err
	}

	var pdu *PDU
	for _, p := range config.PDUs {
		if p.ID == pduID {
			pdu = &p
			break
		}
	}

	if pdu == nil {
		return fmt.Errorf("PDU %s not found", pduID)
	}

	// Use SNMP for APC PDUs, HTTP for others
	if pdu.Type == "apc" {
		return controlAPCOutletSNMP(pdu, outletNum, action)
	} else {
		return controlAPCOutlet(pdu, outletNum, action)
	}
}

// controlAPCOutletSNMP controls APC PDU outlets via SNMP (for AP8641 and similar models)
func controlAPCOutletSNMP(pdu *PDU, outlet int, action string) error {
	// Create individual SNMP connection to avoid global state conflicts
	snmp := &gosnmp.GoSNMP{
		Target: pdu.Host,
		Community: pdu.SNMPCommunity,
		Timeout: time.Duration(5) * time.Second,  // Reduced timeout
		Retries: 2,  // Reduced retries
	}
	
	// Set SNMP port (default 161 if not specified)
	if pdu.SNMPPort > 0 {
		snmp.Port = uint16(pdu.SNMPPort)
	} else {
		snmp.Port = 161
	}
	
	// Set SNMP version (default v1 if not specified)
	switch pdu.SNMPVersion {
	case "v2c":
		snmp.Version = gosnmp.Version2c
	case "v3":
		snmp.Version = gosnmp.Version3
	default:
		snmp.Version = gosnmp.Version1
	}

	if debugMode {
		log.Printf("DEBUG: Connecting to PDU %s via SNMP %s (community: %s) for outlet %d action %s", pdu.Host, pdu.SNMPVersion, pdu.SNMPCommunity, outlet, action)
	}
	
	err := snmp.Connect()
	if err != nil {
		return fmt.Errorf("failed to connect to PDU via SNMP: %v", err)
	}
	
	// Ensure connection is properly closed regardless of how we exit
	defer func() {
		if debugMode {
			log.Printf("DEBUG: Closing SNMP connection to PDU %s", pdu.Host)
		}
		if snmp.Conn != nil {
			snmp.Conn.Close()
		}
	}()
	
	if debugMode {
		log.Printf("DEBUG: Successfully connected to PDU %s, sending command", pdu.Host)
	}

	// Convert action to SNMP value
	var command int
	switch action {
	case "on":
		command = 1
	case "off":
		command = 2
	case "reboot":
		command = 3
	default:
		return fmt.Errorf("unknown PDU action: %s", action)
	}

	// APC AP8641 SNMP OID for outlet control
	// Format: .1.3.6.1.4.1.318.1.1.4.4.2.1.3.{outlet_number}
	oid := fmt.Sprintf(".1.3.6.1.4.1.318.1.1.4.4.2.1.3.%d", outlet)

	pdus := []gosnmp.SnmpPDU{
		{
			Name:  oid,
			Type:  gosnmp.Integer,
			Value: command,
		},
	}

	if debugMode {
		log.Printf("DEBUG: Sending SNMP SET to OID %s with value %d", oid, command)
	}
	
	// Create a timeout context for the SNMP operation
	done := make(chan bool, 1)
	var result *gosnmp.SnmpPacket
	var setErr error
	
	// Run SNMP SET in goroutine with timeout
	go func() {
		result, setErr = snmp.Set(pdus)
		done <- true
	}()
	
	// Wait for either completion or timeout
	select {
	case <-done:
		if setErr != nil {
			if debugMode {
				log.Printf("DEBUG: SNMP SET failed: %v", setErr)
			}
			return fmt.Errorf("failed to set PDU outlet %d to %s: %v", outlet, action, setErr)
		}
		if debugMode {
			log.Printf("DEBUG: SNMP SET successful: %+v", result)
		}
		return nil
		
	case <-time.After(8 * time.Second):  // Timeout after 8 seconds
		if debugMode {
			log.Printf("DEBUG: SNMP SET timed out after 8 seconds")
		}
		return fmt.Errorf("SNMP SET operation timed out for outlet %d", outlet)
	}
}

// controlAPCOutlet controls PDU outlets via HTTP (legacy method for non-APC PDUs)
func controlAPCOutlet(pdu *PDU, outlet int, action string) error {
	client := &http.Client{
		Timeout: 10 * time.Second,
	}

	var command string
	switch action {
	case "on":
		command = "1"
	case "off":
		command = "2"
	case "reboot":
		command = "3"
	default:
		return fmt.Errorf("unknown PDU action: %s", action)
	}

	url := fmt.Sprintf("http://%s/outlet?%d=%s", pdu.Host, outlet, command)
	
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %v", err)
	}

	req.SetBasicAuth(pdu.Username, pdu.Password)

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send PDU command: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("PDU returned status %d", resp.StatusCode)
	}

	return nil
}

func getPDUOutletStatus(pdu *PDU, outlet int) (string, error) {
	// Use SNMP for APC PDUs, HTTP for others
	if pdu.Type == "apc" {
		return getPDUOutletStatusSNMP(pdu, outlet)
	} else {
		return getPDUOutletStatusHTTP(pdu, outlet)
	}
}

// getPDUOutletStatusSNMP gets APC PDU outlet status via SNMP
func getPDUOutletStatusSNMP(pdu *PDU, outlet int) (string, error) {
	// Create individual SNMP connection to avoid global state conflicts
	snmp := &gosnmp.GoSNMP{
		Target: pdu.Host,
		Community: pdu.SNMPCommunity,
		Timeout: time.Duration(5) * time.Second,  // More reasonable timeout
		Retries: 2,  // Two retries for reliability
	}
	
	// Set SNMP port (default 161 if not specified)
	if pdu.SNMPPort > 0 {
		snmp.Port = uint16(pdu.SNMPPort)
	} else {
		snmp.Port = 161
	}
	
	// Set SNMP version (default v1 if not specified)
	switch pdu.SNMPVersion {
	case "v2c":
		snmp.Version = gosnmp.Version2c
	case "v3":
		snmp.Version = gosnmp.Version3
	default:
		snmp.Version = gosnmp.Version1
	}

	err := snmp.Connect()
	if err != nil {
		log.Printf("DEBUG: PDU SNMP connect failed for %s outlet %d: %v", pdu.Host, outlet, err)
		return "unreachable", nil
	}
	
	// Ensure connection is properly closed
	defer func() {
		if snmp.Conn != nil {
			snmp.Conn.Close()
		}
	}()

	// APC AP8641 SNMP OID for outlet status
	// Format: .1.3.6.1.4.1.318.1.1.4.4.2.1.3.{outlet_number}
	oid := fmt.Sprintf(".1.3.6.1.4.1.318.1.1.4.4.2.1.3.%d", outlet)

	// Use timeout for GET operation too
	done := make(chan bool, 1)
	var result *gosnmp.SnmpPacket
	var getErr error
	
	go func() {
		result, getErr = snmp.Get([]string{oid})
		done <- true
	}()
	
	select {
	case <-done:
		if getErr != nil {
			log.Printf("DEBUG: PDU SNMP GET failed for %s outlet %d: %v", pdu.Host, outlet, getErr)
			return "unreachable", nil
		}
	case <-time.After(10 * time.Second):
		log.Printf("DEBUG: PDU SNMP GET timeout for %s outlet %d after 10s", pdu.Host, outlet)
		return "unreachable", nil
	}

	if len(result.Variables) == 0 {
		return "error", nil
	}

	value := result.Variables[0].Value
	switch v := value.(type) {
	case int:
		switch v {
		case 1:
			return "on", nil
		case 2:
			return "off", nil
		default:
			return "unknown", nil
		}
	default:
		return "error", nil
	}
}

// getPDUOutletStatusHTTP gets PDU outlet status via HTTP (legacy method)
func getPDUOutletStatusHTTP(pdu *PDU, outlet int) (string, error) {
	client := &http.Client{
		Timeout: 10 * time.Second,
	}

	url := fmt.Sprintf("http://%s/status.xml", pdu.Host)
	
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %v", err)
	}

	req.SetBasicAuth(pdu.Username, pdu.Password)

	resp, err := client.Do(req)
	if err != nil {
		return "unreachable", nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "error", nil
	}

	return "on", nil
}

// PDU outlet status cache to reduce SNMP queries
type PDUStatusCache struct {
	pduOutletStatus map[string]map[int]string // pduID -> outlet -> status
	lastUpdated     map[string]time.Time      // pduID -> last update time
	mu              sync.RWMutex
}

var pduStatusCache = &PDUStatusCache{
	pduOutletStatus: make(map[string]map[int]string),
	lastUpdated:     make(map[string]time.Time),
}

const PDUCacheTimeout = 20 * time.Second // Cache PDU status for 20 seconds

// getBulkPDUStatus gets status for multiple outlets on a PDU with a single SNMP query
func getBulkPDUStatus(pdu *PDU, outlets []int) (map[int]string, error) {
	return getBulkPDUStatusWithCache(pdu, outlets, false) // Default to use cache
}

// getBulkPDUStatusFresh forces a fresh query, bypassing cache
func getBulkPDUStatusFresh(pdu *PDU, outlets []int) (map[int]string, error) {
	return getBulkPDUStatusWithCache(pdu, outlets, true) // Force fresh
}

// getBulkPDUStatusWithCache gets status with optional cache bypass
func getBulkPDUStatusWithCache(pdu *PDU, outlets []int, bypassCache bool) (map[int]string, error) {
	pduStatusCache.mu.Lock()
	defer pduStatusCache.mu.Unlock()
	
	// Check if cached data is still valid (unless bypassing cache)
	if !bypassCache {
		if lastUpdate, exists := pduStatusCache.lastUpdated[pdu.ID]; exists {
			if time.Since(lastUpdate) < PDUCacheTimeout {
				// Return cached data for requested outlets
				result := make(map[int]string)
				if pduOutlets, exists := pduStatusCache.pduOutletStatus[pdu.ID]; exists {
					for _, outlet := range outlets {
						if status, found := pduOutlets[outlet]; found {
							result[outlet] = status
						}
					}
					if len(result) == len(outlets) {
						return result, nil // All outlets found in cache
					}
				}
			}
		}
	}
	
	// Cache miss, expired, or bypassed - fetch fresh data
	if pdu.Type == "apc" {
		return getBulkPDUStatusSNMP(pdu, outlets)
	} else {
		return getBulkPDUStatusHTTP(pdu, outlets)
	}
}

// getBulkPDUStatusSNMP gets multiple outlet statuses via SNMP with bulk query
func getBulkPDUStatusSNMP(pdu *PDU, outlets []int) (map[int]string, error) {
	// Create individual SNMP connection
	snmp := &gosnmp.GoSNMP{
		Target: pdu.Host,
		Community: pdu.SNMPCommunity,
		Timeout: time.Duration(8) * time.Second,  // Increased timeout for bulk query
		Retries: 1,  // Reduce retries since we're doing bulk
	}
	
	// Set SNMP port (default 161 if not specified)
	if pdu.SNMPPort > 0 {
		snmp.Port = uint16(pdu.SNMPPort)
	} else {
		snmp.Port = 161
	}
	
	// Set SNMP version (default v1 if not specified)
	switch pdu.SNMPVersion {
	case "v2c":
		snmp.Version = gosnmp.Version2c
	case "v3":
		snmp.Version = gosnmp.Version3
	default:
		snmp.Version = gosnmp.Version1
	}

	if debugMode {
		log.Printf("DEBUG: Bulk SNMP query to PDU %s for outlets %v", pdu.Host, outlets)
	}
	
	err := snmp.Connect()
	if err != nil {
		log.Printf("DEBUG: PDU SNMP bulk connect failed for %s: %v", pdu.Host, err)
		return nil, fmt.Errorf("SNMP connect failed: %v", err)
	}
	
	defer func() {
		if snmp.Conn != nil {
			snmp.Conn.Close()
		}
	}()

	// Build OID list for all requested outlets
	var oids []string
	for _, outlet := range outlets {
		oid := fmt.Sprintf(".1.3.6.1.4.1.318.1.1.4.4.2.1.3.%d", outlet)
		oids = append(oids, oid)
	}

	// Use timeout for bulk GET operation
	done := make(chan bool, 1)
	var result *gosnmp.SnmpPacket
	var getErr error
	
	go func() {
		result, getErr = snmp.Get(oids)
		done <- true
	}()
	
	select {
	case <-done:
		if getErr != nil {
			log.Printf("DEBUG: PDU SNMP bulk GET failed for %s: %v", pdu.Host, getErr)
			return nil, fmt.Errorf("SNMP GET failed: %v", getErr)
		}
	case <-time.After(15 * time.Second):
		log.Printf("DEBUG: PDU SNMP bulk GET timeout for %s after 15s", pdu.Host)
		return nil, fmt.Errorf("SNMP bulk GET timeout")
	}

	// Parse results
	statusMap := make(map[int]string)
	for i, variable := range result.Variables {
		if i >= len(outlets) {
			break
		}
		
		outlet := outlets[i]
		switch v := variable.Value.(type) {
		case int:
			switch v {
			case 1:
				statusMap[outlet] = "on"
			case 2:
				statusMap[outlet] = "off"
			default:
				statusMap[outlet] = "unknown"
			}
		default:
			statusMap[outlet] = "error"
		}
	}
	
	// Update cache
	if pduStatusCache.pduOutletStatus[pdu.ID] == nil {
		pduStatusCache.pduOutletStatus[pdu.ID] = make(map[int]string)
	}
	
	for outlet, status := range statusMap {
		pduStatusCache.pduOutletStatus[pdu.ID][outlet] = status
	}
	pduStatusCache.lastUpdated[pdu.ID] = time.Now()
	
	if debugMode {
		log.Printf("DEBUG: Bulk SNMP query successful, cached %d outlet statuses for PDU %s", len(statusMap), pdu.ID)
	}
	
	return statusMap, nil
}

// getBulkPDUStatusHTTP gets multiple outlet statuses via HTTP (legacy, less efficient)
func getBulkPDUStatusHTTP(pdu *PDU, outlets []int) (map[int]string, error) {
	// For HTTP PDUs, we still need individual queries (or implement status.xml parsing)
	// For now, fall back to individual queries but cache the results
	statusMap := make(map[int]string)
	
	for _, outlet := range outlets {
		status, err := getPDUOutletStatusHTTP(pdu, outlet)
		if err != nil {
			return nil, err
		}
		statusMap[outlet] = status
	}
	
	// Update cache
	if pduStatusCache.pduOutletStatus[pdu.ID] == nil {
		pduStatusCache.pduOutletStatus[pdu.ID] = make(map[int]string)
	}
	
	for outlet, status := range statusMap {
		pduStatusCache.pduOutletStatus[pdu.ID][outlet] = status
	}
	pduStatusCache.lastUpdated[pdu.ID] = time.Now()
	
	return statusMap, nil
}

// clearPDUCache clears the PDU status cache for fresh polling
func clearPDUCache() {
	pduStatusCache.mu.Lock()
	defer pduStatusCache.mu.Unlock()
	
	pduStatusCache.pduOutletStatus = make(map[string]map[int]string)
	pduStatusCache.lastUpdated = make(map[string]time.Time)
	log.Printf("PDU status cache cleared")
}