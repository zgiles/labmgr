package main

import (
	"fmt"
	"strings"
	"time"
)

// createPowerOnSequence creates a sequence of jobs for powering on a server
// State-aware: Only attempts operations that make sense based on current state
func createPowerOnSequence(serverID string) ([]*Job, error) {
	config, err := loadConfig()
	if err != nil {
		return nil, err
	}
	
	var server *Server
	for _, s := range config.Servers {
		if s.ID == serverID {
			server = &s
			break
		}
	}
	
	if server == nil {
		return nil, fmt.Errorf("server %s not found", serverID)
	}
	
	var jobs []*Job
	
	// Get current device status to make intelligent decisions
	systemStatus, powerStatus, _ := getDeviceStatusDual(serverID)
	
	// Check if device is already on
	if systemStatus == "On" || (powerStatus == "on" && systemStatus == "unreachable") {
		return nil, fmt.Errorf("server %s is already powered on (system: %s, power: %s)", serverID, systemStatus, powerStatus)
	}
	
	// If server has PDU outlets and they're currently off, power them on first
	if len(server.PDUOutlets) > 0 && powerStatus == "off" {
		for i, outlet := range server.PDUOutlets {
			description := fmt.Sprintf("Power on PDU outlet %s for %s", outlet, server.Name)
			job := jobQueue.AddJob(JobPDU, serverID, "on", outlet, description)
			jobs = append(jobs, job)
			
			// Add delay between PDU outlet actions (minimum 3 seconds even if config is 0)
			delaySeconds := config.Delays.BetweenPDU
			if delaySeconds < 3 {
				delaySeconds = 3  // Minimum 3-second delay to avoid overwhelming PDU
			}
			if i > 0 {
				delay := time.Duration(i * delaySeconds) * time.Second
				notBefore := time.Now().Add(delay)
				job.NotBefore = &notBefore
				job.Status = JobWaiting
			}
		}
		
		// Only add IPMI job if server has IPMI configured and we need system-level power on
		// Skip IPMI if it's already reachable (system already on)
		if server.IPMI.Host != "" && systemStatus != "On" {
			// Add IPMI power on job with dependency on last PDU job and delay
			lastPDUJob := jobs[len(jobs)-1]
			description := fmt.Sprintf("IPMI power on %s (after PDU delay)", server.Name)
			ipmiJob := jobQueue.AddJobWithDependency(JobIPMI, serverID, "on", "", description, lastPDUJob.ID)
			
			// Add delay from PDU to IPMI
			if config.Delays.PDUToPowerOn > 0 {
				delay := time.Duration(config.Delays.PDUToPowerOn) * time.Second
				notBefore := time.Now().Add(delay)
				ipmiJob.NotBefore = &notBefore
			}
			
			jobs = append(jobs, ipmiJob)
		}
	} else if server.IPMI.Host != "" && powerStatus == "on" {
		// PDU is already on, just need IPMI power on (system-level)
		description := fmt.Sprintf("IPMI power on %s (PDU already on)", server.Name)
		job := jobQueue.AddJob(JobIPMI, serverID, "on", "", description)
		jobs = append(jobs, job)
	} else if server.IPMI.Host != "" && len(server.PDUOutlets) == 0 {
		// No PDU outlets but has IPMI, just do IPMI power on
		description := fmt.Sprintf("IPMI power on %s", server.Name)
		job := jobQueue.AddJob(JobIPMI, serverID, "on", "", description)
		jobs = append(jobs, job)
	} else {
		// Device is already on or no viable control method
		if powerStatus == "on" {
			return nil, fmt.Errorf("server %s PDU is already on, but IPMI is unavailable for system power", serverID)
		}
		return nil, fmt.Errorf("server %s has no reachable control method available", serverID)
	}
	
	return jobs, nil
}

// createPowerOffSequence creates a sequence of jobs for powering off a server
// State-aware: Only attempts IPMI if IPMI is actually reachable
func createPowerOffSequence(serverID string) ([]*Job, error) {
	config, err := loadConfig()
	if err != nil {
		return nil, err
	}
	
	var server *Server
	for _, s := range config.Servers {
		if s.ID == serverID {
			server = &s
			break
		}
	}
	
	if server == nil {
		return nil, fmt.Errorf("server %s not found", serverID)
	}
	
	var jobs []*Job
	
	// Get current device status to make intelligent decisions
	systemStatus, powerStatus, _ := getDeviceStatusDual(serverID)
	
	// Check if server has IPMI configured AND is currently reachable
	ipmiAvailable := server.IPMI.Host != "" && systemStatus != "unreachable" && systemStatus != "no_ipmi"
	
	if ipmiAvailable {
		// IPMI power off first
		description := fmt.Sprintf("IPMI power off %s", server.Name)
		ipmiJob := jobQueue.AddJob(JobIPMI, serverID, "off", "", description)
		jobs = append(jobs, ipmiJob)
		
		// If server has PDU outlets, power them off after delay
		if len(server.PDUOutlets) > 0 {
			for i, outlet := range server.PDUOutlets {
				description := fmt.Sprintf("Power off PDU outlet %s for %s", outlet, server.Name)
				pduJob := jobQueue.AddJobWithDependency(JobPDU, serverID, "off", outlet, description, ipmiJob.ID)
				
				// Add delay from IPMI off to PDU off
				if config.Delays.PowerOffToPDU > 0 {
					delay := time.Duration(config.Delays.PowerOffToPDU) * time.Second
					notBefore := time.Now().Add(delay)
					pduJob.NotBefore = &notBefore
				}
				
				// Add delay between PDU outlet actions (minimum 3 seconds)
				delaySeconds := config.Delays.BetweenPDU
				if delaySeconds < 3 {
					delaySeconds = 3  // Minimum 3-second delay to avoid overwhelming PDU
				}
				if i > 0 {
					extraDelay := time.Duration(i * delaySeconds) * time.Second
					if pduJob.NotBefore != nil {
						newTime := pduJob.NotBefore.Add(extraDelay)
						pduJob.NotBefore = &newTime
					} else {
						// If no existing delay, add just the PDU interval delay
						notBefore := time.Now().Add(extraDelay)
						pduJob.NotBefore = &notBefore
						pduJob.Status = JobWaiting
					}
				}
				
				jobs = append(jobs, pduJob)
			}
		}
	} else if len(server.PDUOutlets) > 0 && powerStatus == "on" {
		// IPMI unavailable but PDU shows device is on - power off PDU only
		for i, outlet := range server.PDUOutlets {
			description := fmt.Sprintf("Power off PDU outlet %s for %s", outlet, server.Name)
			pduJob := jobQueue.AddJob(JobPDU, serverID, "off", outlet, description)
			
			// Add delay between PDU outlet actions (minimum 3 seconds)
			delaySeconds := config.Delays.BetweenPDU
			if delaySeconds < 3 {
				delaySeconds = 3  // Minimum 3-second delay to avoid overwhelming PDU
			}
			if i > 0 {
				delay := time.Duration(i * delaySeconds) * time.Second
				notBefore := time.Now().Add(delay)
				pduJob.NotBefore = &notBefore
				pduJob.Status = JobWaiting
			}
			
			jobs = append(jobs, pduJob)
		}
	} else {
		// Device is already off or no viable control method available
		if powerStatus == "off" {
			return nil, fmt.Errorf("server %s is already powered off (PDU status: %s)", serverID, powerStatus)
		}
		return nil, fmt.Errorf("server %s has no reachable control method available (IPMI: %s, PDU: %s)", serverID, systemStatus, powerStatus)
	}
	
	return jobs, nil
}

// createRebootSequence creates a power off followed by power on sequence
func createRebootSequence(serverID string) ([]*Job, error) {
	// First create power off sequence
	powerOffJobs, err := createPowerOffSequence(serverID)
	if err != nil {
		return nil, err
	}
	
	// Wait for all power off jobs to complete, then start power on
	lastPowerOffJob := powerOffJobs[len(powerOffJobs)-1]
	
	// Create power on sequence that depends on power off completion
	powerOnJobs, err := createPowerOnSequence(serverID)
	if err != nil {
		return nil, err
	}
	
	// Make first power on job depend on last power off job
	if len(powerOnJobs) > 0 {
		firstPowerOnJob := powerOnJobs[0]
		firstPowerOnJob.DependsOn = lastPowerOffJob.ID
		firstPowerOnJob.Status = JobWaiting
		
		// Add reboot delay
		config, _ := loadConfig()
		if config.Delays.BetweenIPMI > 0 {
			delay := time.Duration(config.Delays.BetweenIPMI * 2) * time.Second // Double delay for reboot
			notBefore := time.Now().Add(delay)
			firstPowerOnJob.NotBefore = &notBefore
		}
	}
	
	// Combine all jobs
	allJobs := append(powerOffJobs, powerOnJobs...)
	return allJobs, nil
}

func createPowerOffSequenceScheduled(serverID string, scheduledTime time.Time) ([]*Job, error) {
	config, err := loadConfig()
	if err != nil {
		return nil, err
	}

	var server *Server
	for _, s := range config.Servers {
		if s.ID == serverID {
			server = &s
			break
		}
	}

	if server == nil {
		return nil, fmt.Errorf("server %s not found", serverID)
	}

	var jobs []*Job
	
	// If server has PDU outlets, create PDU power off job scheduled for the specified time
	if len(server.PDUOutlets) > 0 {
		description := fmt.Sprintf("Scheduled PDU power off %s outlets %v", server.Name, server.PDUOutlets)
		pduJob := jobQueue.AddScheduledJob(JobPDU, serverID, "off", strings.Join(server.PDUOutlets, ","), description, scheduledTime)
		jobs = append(jobs, pduJob)
	} else {
		// No PDU, just IPMI power off scheduled
		description := fmt.Sprintf("Scheduled IPMI power off %s", server.Name)
		ipmiJob := jobQueue.AddScheduledJob(JobIPMI, serverID, "off", "", description, scheduledTime)
		jobs = append(jobs, ipmiJob)
	}

	return jobs, nil
}

func createPowerOnSequenceScheduled(serverID string, scheduledTime time.Time) ([]*Job, error) {
	config, err := loadConfig()
	if err != nil {
		return nil, err
	}

	var server *Server
	for _, s := range config.Servers {
		if s.ID == serverID {
			server = &s
			break
		}
	}

	if server == nil {
		return nil, fmt.Errorf("server %s not found", serverID)
	}

	var jobs []*Job
	currentTime := scheduledTime
	
	// If server has PDU outlets, create PDU power on first
	if len(server.PDUOutlets) > 0 {
		description := fmt.Sprintf("Scheduled PDU power on %s outlets %v", server.Name, server.PDUOutlets)
		pduJob := jobQueue.AddScheduledJob(JobPDU, serverID, "on", strings.Join(server.PDUOutlets, ","), description, currentTime)
		jobs = append(jobs, pduJob)
		
		// Schedule IPMI power on with delay
		delaySeconds := config.Delays.PDUToPowerOn
		if delaySeconds == 0 {
			delaySeconds = 10 // Default 10 seconds
		}
		duration := time.Duration(delaySeconds) * time.Second
		currentTime = currentTime.Add(duration)
		
		description = fmt.Sprintf("Scheduled IPMI power on %s", server.Name)
		ipmiJob := jobQueue.AddScheduledJob(JobIPMI, serverID, "on", "", description, currentTime)
		jobs = append(jobs, ipmiJob)
	} else {
		// No PDU, just IPMI power on scheduled
		description := fmt.Sprintf("Scheduled IPMI power on %s", server.Name)
		ipmiJob := jobQueue.AddScheduledJob(JobIPMI, serverID, "on", "", description, currentTime)
		jobs = append(jobs, ipmiJob)
	}

	return jobs, nil
}