package main

import (
	"fmt"
	"time"
)

// findServer looks up a server by ID in the given config
func findServer(config *Config, serverID string) *Server {
	for i := range config.Servers {
		if config.Servers[i].ID == serverID {
			return &config.Servers[i]
		}
	}
	return nil
}

// pduInterval returns the delay between PDU outlet actions (minimum 3 seconds to avoid overwhelming the PDU)
func pduInterval(config *Config) time.Duration {
	delaySeconds := config.Delays.BetweenPDU
	if delaySeconds < 3 {
		delaySeconds = 3
	}
	return time.Duration(delaySeconds) * time.Second
}

// chainJob makes job wait for dependsOn and not start before notBefore
func chainJob(job *Job, dependsOn string, notBefore time.Time) {
	jobQueue.mu.Lock()
	defer jobQueue.mu.Unlock()
	if dependsOn != "" {
		job.DependsOn = dependsOn
	}
	if notBefore.After(time.Now()) && (job.NotBefore == nil || notBefore.After(*job.NotBefore)) {
		job.NotBefore = &notBefore
	}
	if job.DependsOn != "" || job.NotBefore != nil {
		job.Status = JobWaiting
	}
}

// createPowerOnSequence creates a sequence of jobs for powering on a server
// State-aware: Only attempts operations that make sense based on current state
func createPowerOnSequence(serverID string) ([]*Job, error) {
	config, err := loadConfig()
	if err != nil {
		return nil, err
	}

	server := findServer(config, serverID)
	if server == nil {
		return nil, fmt.Errorf("server %s not found", serverID)
	}

	// Get current device status to make intelligent decisions
	systemStatus, powerStatus, _ := getDeviceStatusDual(serverID)

	// Check if device is already on
	if systemStatus == "On" || (powerStatus == "on" && systemStatus == "unreachable") {
		return nil, fmt.Errorf("server %s is already powered on (system: %s, power: %s)", serverID, systemStatus, powerStatus)
	}

	return planPowerOn(config, server, powerStatus, "")
}

// planPowerOn queues the power-on jobs for a server given its (expected) PDU power status.
// If after is set, the first job waits for that job ID; each later job waits for the one before it.
func planPowerOn(config *Config, server *Server, powerStatus string, after string) ([]*Job, error) {
	var jobs []*Job
	prev := after

	if len(server.PDUOutlets) > 0 && powerStatus == "off" {
		// Power on PDU outlets first, spaced apart
		for i, outlet := range server.PDUOutlets {
			description := fmt.Sprintf("Power on PDU outlet %s for %s", outlet, server.Name)
			job := jobQueue.AddJob(JobPDU, server.ID, "on", outlet, description)
			var notBefore time.Time
			if i > 0 {
				notBefore = time.Now().Add(time.Duration(i) * pduInterval(config))
			}
			chainJob(job, prev, notBefore)
			prev = job.ID
			jobs = append(jobs, job)
		}

		// Then IPMI power on after the PDU-to-power-on delay
		if server.IPMI.Host != "" {
			description := fmt.Sprintf("IPMI power on %s (after PDU delay)", server.Name)
			ipmiJob := jobQueue.AddJob(JobIPMI, server.ID, "on", "", description)
			chainJob(ipmiJob, prev, time.Now().Add(time.Duration(config.Delays.PDUToPowerOn)*time.Second))
			jobs = append(jobs, ipmiJob)
		}
	} else if server.IPMI.Host != "" && (powerStatus == "on" || len(server.PDUOutlets) == 0) {
		// PDU already on (or no PDU) - just IPMI power on
		description := fmt.Sprintf("IPMI power on %s", server.Name)
		if powerStatus == "on" {
			description += " (PDU already on)"
		}
		job := jobQueue.AddJob(JobIPMI, server.ID, "on", "", description)
		chainJob(job, prev, time.Time{})
		jobs = append(jobs, job)
	} else {
		if powerStatus == "on" {
			return nil, fmt.Errorf("server %s PDU is already on, but IPMI is unavailable for system power", server.ID)
		}
		return nil, fmt.Errorf("server %s has no reachable control method available", server.ID)
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

	server := findServer(config, serverID)
	if server == nil {
		return nil, fmt.Errorf("server %s not found", serverID)
	}

	var jobs []*Job

	// Get current device status to make intelligent decisions
	systemStatus, powerStatus, _ := getDeviceStatusDual(serverID)

	// Check if server has IPMI configured AND is currently reachable
	ipmiAvailable := server.IPMI.Host != "" && systemStatus != "unreachable" && systemStatus != "no_ipmi"

	prev := ""
	var pduStart time.Time
	if ipmiAvailable {
		// IPMI power off first
		description := fmt.Sprintf("IPMI power off %s", server.Name)
		ipmiJob := jobQueue.AddJob(JobIPMI, serverID, "off", "", description)
		jobs = append(jobs, ipmiJob)
		prev = ipmiJob.ID
		pduStart = time.Now().Add(time.Duration(config.Delays.PowerOffToPDU) * time.Second)
	} else if !(len(server.PDUOutlets) > 0 && powerStatus == "on") {
		// Device is already off or no viable control method available
		if powerStatus == "off" {
			return nil, fmt.Errorf("server %s is already powered off (PDU status: %s)", serverID, powerStatus)
		}
		return nil, fmt.Errorf("server %s has no reachable control method available (IPMI: %s, PDU: %s)", serverID, systemStatus, powerStatus)
	}

	// Then cut PDU outlets, spaced apart
	for i, outlet := range server.PDUOutlets {
		description := fmt.Sprintf("Power off PDU outlet %s for %s", outlet, server.Name)
		pduJob := jobQueue.AddJob(JobPDU, serverID, "off", outlet, description)
		notBefore := pduStart
		if i > 0 {
			if notBefore.IsZero() {
				notBefore = time.Now()
			}
			notBefore = notBefore.Add(time.Duration(i) * pduInterval(config))
		}
		chainJob(pduJob, prev, notBefore)
		prev = pduJob.ID
		jobs = append(jobs, pduJob)
	}

	return jobs, nil
}

// createRebootSequence creates a power off followed by power on sequence
func createRebootSequence(serverID string) ([]*Job, error) {
	config, err := loadConfig()
	if err != nil {
		return nil, err
	}

	server := findServer(config, serverID)
	if server == nil {
		return nil, fmt.Errorf("server %s not found", serverID)
	}

	powerOffJobs, err := createPowerOffSequence(serverID)
	if err != nil {
		return nil, err
	}

	// Plan the power-on half for the state the server will be in after power off,
	// not its current state (which is still "on")
	expectedPower := "no_pdu"
	if len(server.PDUOutlets) > 0 {
		expectedPower = "off"
	}
	lastPowerOffJob := powerOffJobs[len(powerOffJobs)-1]
	powerOnJobs, err := planPowerOn(config, server, expectedPower, lastPowerOffJob.ID)
	if err != nil {
		// Don't leave a dangling power off behind
		for _, job := range powerOffJobs {
			jobQueue.CancelJob(job.ID)
		}
		return nil, err
	}

	// Pause between off and on
	if config.Delays.BetweenIPMI > 0 {
		delay := time.Duration(config.Delays.BetweenIPMI*2) * time.Second // Double delay for reboot
		chainJob(powerOnJobs[0], "", time.Now().Add(delay))
	}

	return append(powerOffJobs, powerOnJobs...), nil
}

func createPowerOffSequenceScheduled(serverID string, scheduledTime time.Time) ([]*Job, error) {
	config, err := loadConfig()
	if err != nil {
		return nil, err
	}

	server := findServer(config, serverID)
	if server == nil {
		return nil, fmt.Errorf("server %s not found", serverID)
	}

	var jobs []*Job
	prev := ""
	pduStart := scheduledTime

	// Shut the system down over IPMI first so the PDU never cuts a running machine
	if server.IPMI.Host != "" {
		description := fmt.Sprintf("Scheduled IPMI power off %s", server.Name)
		ipmiJob := jobQueue.AddScheduledJob(JobIPMI, serverID, "off", "", description, scheduledTime)
		jobs = append(jobs, ipmiJob)
		prev = ipmiJob.ID
		pduStart = scheduledTime.Add(time.Duration(config.Delays.PowerOffToPDU) * time.Second)
	}

	for i, outlet := range server.PDUOutlets {
		description := fmt.Sprintf("Scheduled PDU power off %s outlet %s", server.Name, outlet)
		pduJob := jobQueue.AddScheduledJob(JobPDU, serverID, "off", outlet, description, pduStart.Add(time.Duration(i)*pduInterval(config)))
		chainJob(pduJob, prev, time.Time{})
		prev = pduJob.ID
		jobs = append(jobs, pduJob)
	}

	return jobs, nil
}

func createPowerOnSequenceScheduled(serverID string, scheduledTime time.Time) ([]*Job, error) {
	config, err := loadConfig()
	if err != nil {
		return nil, err
	}

	server := findServer(config, serverID)
	if server == nil {
		return nil, fmt.Errorf("server %s not found", serverID)
	}

	var jobs []*Job
	prev := ""
	ipmiStart := scheduledTime

	// PDU outlets first, spaced apart
	for i, outlet := range server.PDUOutlets {
		description := fmt.Sprintf("Scheduled PDU power on %s outlet %s", server.Name, outlet)
		pduJob := jobQueue.AddScheduledJob(JobPDU, serverID, "on", outlet, description, scheduledTime.Add(time.Duration(i)*pduInterval(config)))
		chainJob(pduJob, prev, time.Time{})
		prev = pduJob.ID
		jobs = append(jobs, pduJob)
	}

	if len(server.PDUOutlets) > 0 {
		delaySeconds := config.Delays.PDUToPowerOn
		if delaySeconds == 0 {
			delaySeconds = 10 // Default 10 seconds
		}
		ipmiStart = scheduledTime.Add(time.Duration(delaySeconds) * time.Second)
	}

	if server.IPMI.Host != "" {
		description := fmt.Sprintf("Scheduled IPMI power on %s", server.Name)
		ipmiJob := jobQueue.AddScheduledJob(JobIPMI, serverID, "on", "", description, ipmiStart)
		chainJob(ipmiJob, prev, time.Time{})
		jobs = append(jobs, ipmiJob)
	}

	return jobs, nil
}
