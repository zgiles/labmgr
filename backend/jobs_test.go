package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// testConfig points configPath at a throwaway config whose hardware is unreachable
// (TEST-NET-1 addresses), so nothing here can touch a real machine.
func testConfig(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "servers.yaml")
	yaml := `
servers:
  - id: "fake1"
    name: "fake1"
    ipmi: {host: "192.0.2.1", username: "u", password: "p"}
    pdu_outlets: ["pdu-x:1", "pdu-x:2"]
pdus:
  - {id: "pdu-x", name: "x", host: "192.0.2.2", type: "apc", snmp_community: "c"}
delays:
  pdu_to_power_on_seconds: 5
  power_off_to_pdu_seconds: 2
  between_pdu_seconds: 3
`
	if err := os.WriteFile(path, []byte(yaml), 0600); err != nil {
		t.Fatal(err)
	}
	configPath = path
	jobQueue = &JobQueue{jobs: make(map[string]*Job), nextID: 1, running: true}
}

func inQueue(job *Job) bool {
	jobQueue.mu.RLock()
	defer jobQueue.mu.RUnlock()
	for _, j := range jobQueue.queue {
		if j == job {
			return true
		}
	}
	return false
}

func TestScheduledJobsAreQueuedAndChained(t *testing.T) {
	testConfig(t)
	at := time.Now().Add(time.Hour)

	jobs, err := createPowerOffSequenceScheduled("fake1", at)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 3 {
		t.Fatalf("want IPMI off + 2 outlet jobs, got %d", len(jobs))
	}
	if jobs[0].Type != JobIPMI || jobs[0].Action != "off" {
		t.Errorf("first step should be IPMI off, got %s %s", jobs[0].Type, jobs[0].Action)
	}
	for i, job := range jobs {
		if !inQueue(job) {
			t.Errorf("job %d (%s) not in run queue", i, job.Description)
		}
		if job.Status != JobWaiting || job.NotBefore == nil || job.NotBefore.Before(at) {
			t.Errorf("job %d should wait until the scheduled time", i)
		}
		if job.MaxRetries == 0 {
			t.Errorf("job %d has no retries", i)
		}
		if i > 0 && job.DependsOn != jobs[i-1].ID {
			t.Errorf("job %d should depend on job %d", i, i-1)
		}
		if job.Type == JobPDU && (job.Target != "pdu-x:1" && job.Target != "pdu-x:2") {
			t.Errorf("PDU job target should be a single outlet, got %q", job.Target)
		}
	}
}

func TestPlanPowerOnChainsEveryStep(t *testing.T) {
	testConfig(t)
	config, _ := loadConfig()
	server := findServer(config, "fake1")

	jobs, err := planPowerOn(config, server, "off", "job-before")
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 3 {
		t.Fatalf("want 2 outlet jobs + IPMI on, got %d", len(jobs))
	}
	if jobs[0].DependsOn != "job-before" {
		t.Errorf("first power-on step must wait for the power-off sequence")
	}
	for i := 1; i < len(jobs); i++ {
		if jobs[i].DependsOn != jobs[i-1].ID {
			t.Errorf("step %d runs without waiting for step %d", i, i-1)
		}
	}
}

func TestCancelCascadesToDependents(t *testing.T) {
	testConfig(t)
	jobs, err := createPowerOnSequenceScheduled("fake1", time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if err := jobQueue.CancelJob(jobs[0].ID); err != nil {
		t.Fatal(err)
	}
	for i, job := range jobs {
		if job.Status != JobFailed {
			t.Errorf("job %d should be cancelled, is %s", i, job.Status)
		}
	}
}

func TestDueScheduledJobStartsAndFinishedJobsLeaveQueue(t *testing.T) {
	testConfig(t)
	job := jobQueue.AddScheduledJob(JobIPMI, "fake1", "bogus-action", "", "test", time.Now().Add(-time.Second))
	jobQueue.mu.Lock()
	job.MaxRetries = 0
	jobQueue.mu.Unlock()

	jobQueue.processJobs()
	deadline := time.Now().Add(40 * time.Second)
	for {
		jobQueue.mu.RLock()
		status := job.Status
		jobQueue.mu.RUnlock()
		if status == JobFailed {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("scheduled job never ran (status %s)", status)
		}
		time.Sleep(100 * time.Millisecond)
	}
	jobQueue.processJobs()
	if inQueue(job) {
		t.Errorf("finished job still in run queue")
	}
}
