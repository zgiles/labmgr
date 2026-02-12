package main

import (
	"fmt"
	"sync"
	"time"
	"log"
)

// getMaxRetriesForJobType returns the maximum number of retries for different job types
func getMaxRetriesForJobType(jobType JobType) int {
	switch jobType {
	case JobIPMI:
		return 3  // IPMI operations get 3 retries (4 total attempts)
	case JobPDU:
		return 2  // PDU operations get 2 retries (3 total attempts)
	default:
		return 1  // Default 1 retry
	}
}

type JobStatus string

const (
	JobPending    JobStatus = "pending"
	JobRunning    JobStatus = "running"
	JobCompleted  JobStatus = "completed"
	JobFailed     JobStatus = "failed"
	JobWaiting    JobStatus = "waiting" // Waiting for dependency or delay
)

type JobType string

const (
	JobIPMI JobType = "ipmi"
	JobPDU  JobType = "pdu"
)

type Job struct {
	ID           string        `json:"id"`
	Type         JobType       `json:"type"`
	ServerID     string        `json:"server_id,omitempty"`
	Action       string        `json:"action"`
	Target       string        `json:"target,omitempty"` // For PDU: outlet ID
	Status       JobStatus     `json:"status"`
	CreatedAt    time.Time     `json:"created_at"`
	StartedAt    *time.Time    `json:"started_at,omitempty"`
	CompletedAt  *time.Time    `json:"completed_at,omitempty"`
	NotBefore    *time.Time    `json:"not_before,omitempty"` // Delay until this time
	DependsOn    string        `json:"depends_on,omitempty"` // Wait for this job ID to complete
	Error        string        `json:"error,omitempty"`
	Description  string        `json:"description"`
	RetryCount   int           `json:"retry_count"`       // Current number of retries
	MaxRetries   int           `json:"max_retries"`       // Maximum number of retries allowed
	LastError    string        `json:"last_error,omitempty"` // Last error encountered
}

type JobQueue struct {
	mu       sync.RWMutex
	jobs     map[string]*Job
	queue    []*Job
	running  bool
	nextID   int
}

var jobQueue *JobQueue

func init() {
	jobQueue = &JobQueue{
		jobs:   make(map[string]*Job),
		queue:  make([]*Job, 0),
		nextID: 1,
	}
}

func (jq *JobQueue) AddScheduledJob(jobType JobType, serverID, action, target, description string, notBefore time.Time) *Job {
	jq.mu.Lock()
	defer jq.mu.Unlock()
	
	job := &Job{
		ID:          fmt.Sprintf("job-%d", jq.nextID),
		Type:        jobType,
		ServerID:    serverID,
		Action:      action,
		Status:      "pending",
		CreatedAt:   time.Now(),
		NotBefore:   &notBefore,
		Target:      target,
		Description: description,
	}
	
	jq.nextID++
	jq.jobs[job.ID] = job
	return job
}

func (jq *JobQueue) AddJob(jobType JobType, serverID, action, target, description string) *Job {
	jq.mu.Lock()
	defer jq.mu.Unlock()
	
	job := &Job{
		ID:          fmt.Sprintf("job-%d", jq.nextID),
		Type:        jobType,
		ServerID:    serverID,
		Action:      action,
		Target:      target,
		Status:      JobPending,
		CreatedAt:   time.Now(),
		Description: description,
		RetryCount:  0,
		MaxRetries:  getMaxRetriesForJobType(jobType),
	}
	
	jq.nextID++
	jq.jobs[job.ID] = job
	jq.queue = append(jq.queue, job)
	
	return job
}

func (jq *JobQueue) AddJobWithDelay(jobType JobType, serverID, action, target, description string, delay time.Duration) *Job {
	job := jq.AddJob(jobType, serverID, action, target, description)
	
	jq.mu.Lock()
	notBefore := time.Now().Add(delay)
	job.NotBefore = &notBefore
	job.Status = JobWaiting
	jq.mu.Unlock()
	
	return job
}

func (jq *JobQueue) AddJobWithDependency(jobType JobType, serverID, action, target, description string, dependsOn string) *Job {
	job := jq.AddJob(jobType, serverID, action, target, description)
	
	jq.mu.Lock()
	job.DependsOn = dependsOn
	job.Status = JobWaiting
	jq.mu.Unlock()
	
	return job
}

func (jq *JobQueue) GetJob(id string) (*Job, bool) {
	jq.mu.RLock()
	defer jq.mu.RUnlock()
	job, exists := jq.jobs[id]
	return job, exists
}

func (jq *JobQueue) GetAllJobs() []*Job {
	jq.mu.RLock()
	defer jq.mu.RUnlock()
	
	jobs := make([]*Job, 0, len(jq.jobs))
	for _, job := range jq.jobs {
		jobs = append(jobs, job)
	}
	return jobs
}

func (jq *JobQueue) GetJobsForServer(serverID string) []*Job {
	jq.mu.RLock()
	defer jq.mu.RUnlock()
	
	jobs := make([]*Job, 0)
	for _, job := range jq.jobs {
		if job.ServerID == serverID {
			jobs = append(jobs, job)
		}
	}
	return jobs
}

func (jq *JobQueue) updateJobStatus(jobID string, status JobStatus, err error) {
	jq.mu.Lock()
	defer jq.mu.Unlock()
	
	if job, exists := jq.jobs[jobID]; exists {
		job.Status = status
		now := time.Now()
		
		switch status {
		case JobRunning:
			job.StartedAt = &now
		case JobCompleted, JobFailed:
			job.CompletedAt = &now
			if err != nil {
				job.Error = err.Error()
			}
		}
	}
}

func (jq *JobQueue) CancelJob(jobID string) error {
	jq.mu.Lock()
	defer jq.mu.Unlock()
	
	job, exists := jq.jobs[jobID]
	if !exists {
		return fmt.Errorf("job %s not found", jobID)
	}
	
	if job.Status == JobRunning {
		return fmt.Errorf("cannot cancel running job")
	}
	
	if job.Status == JobCompleted || job.Status == JobFailed {
		return fmt.Errorf("job already completed")
	}
	
	// Remove from queue
	for i, queuedJob := range jq.queue {
		if queuedJob.ID == jobID {
			jq.queue = append(jq.queue[:i], jq.queue[i+1:]...)
			break
		}
	}
	
	// Mark as failed with cancellation message
	job.Status = JobFailed
	job.Error = "Cancelled by user"
	now := time.Now()
	job.CompletedAt = &now
	
	// Reset server status to pending if job was changing server state
	if job.ServerID != "" && (job.Action == "on" || job.Action == "off" || job.Action == "reboot") {
		cache.ClearTransitionalStatus(job.ServerID)
		log.Printf("Reset server %s status to pending after job %s cancellation", job.ServerID, job.ID)
	}
	
	return nil
}

func (jq *JobQueue) RunJobNow(jobID string) error {
	jq.mu.Lock()
	defer jq.mu.Unlock()
	
	job, exists := jq.jobs[jobID]
	if !exists {
		return fmt.Errorf("job %s not found", jobID)
	}
	
	if job.Status == JobRunning {
		return fmt.Errorf("job is already running")
	}
	
	if job.Status == JobCompleted || job.Status == JobFailed {
		return fmt.Errorf("job already completed")
	}
	
	// Clear dependencies and delays
	job.DependsOn = ""
	job.NotBefore = nil
	job.Status = JobPending
	
	// Make sure job is in the queue for immediate processing
	jobInQueue := false
	for _, queuedJob := range jq.queue {
		if queuedJob.ID == jobID {
			jobInQueue = true
			break
		}
	}
	
	if !jobInQueue {
		jq.queue = append(jq.queue, job)
	}
	
	log.Printf("Job %s marked for immediate execution", jobID)
	return nil
}

func (jq *JobQueue) RetryJob(jobID string) error {
	jq.mu.Lock()
	defer jq.mu.Unlock()
	
	job, exists := jq.jobs[jobID]
	if !exists {
		return fmt.Errorf("job %s not found", jobID)
	}
	
	if job.Status != JobFailed {
		return fmt.Errorf("only failed jobs can be retried (current status: %s)", job.Status)
	}
	
	// Reset the job for retry
	job.Status = JobPending
	job.Error = ""
	job.RetryCount = 0  // Reset retry count for manual retry
	job.StartedAt = nil
	job.CompletedAt = nil
	job.NotBefore = nil  // Clear any delay
	job.DependsOn = ""   // Clear dependencies for immediate retry
	
	// Make sure job is in the queue for immediate processing
	jobInQueue := false
	for _, queuedJob := range jq.queue {
		if queuedJob.ID == jobID {
			jobInQueue = true
			break
		}
	}
	
	if !jobInQueue {
		jq.queue = append(jq.queue, job)
	}
	
	log.Printf("Job %s manually retried and queued for execution", jobID)
	return nil
}

func (jq *JobQueue) canRunJob(job *Job) bool {
	// Check if job has a delay
	if job.NotBefore != nil && time.Now().Before(*job.NotBefore) {
		return false
	}
	
	// Check if job has dependency
	if job.DependsOn != "" {
		if depJob, exists := jq.jobs[job.DependsOn]; exists {
			// Allow dependent jobs to run when dependency is completed OR failed
			// This is important for PDU jobs that should run even when IPMI fails
			return depJob.Status == JobCompleted || depJob.Status == JobFailed
		}
		// Dependency doesn't exist, assume it's completed
		return true
	}
	
	return true
}

func startJobProcessor() {
	log.Println("Starting job processor...")
	
	jobQueue.mu.Lock()
	jobQueue.running = true
	jobQueue.mu.Unlock()
	
	go func() {
		ticker := time.NewTicker(1 * time.Second) // Check every second
		defer ticker.Stop()
		
		for range ticker.C {
			jobQueue.processJobs()
		}
	}()
}

func (jq *JobQueue) processJobs() {
	jq.mu.RLock()
	if !jq.running {
		jq.mu.RUnlock()
		return
	}
	
	// Find next job to run
	var jobToRun *Job
	for _, job := range jq.queue {
		if (job.Status == JobPending || job.Status == JobWaiting) && jq.canRunJob(job) {
			jobToRun = job
			break
		}
	}
	jq.mu.RUnlock()
	
	if jobToRun != nil {
		jq.executeJob(jobToRun)
	}
}

func (jq *JobQueue) executeJob(job *Job) {
	log.Printf("Executing job %s: %s", job.ID, job.Description)
	
	jq.updateJobStatus(job.ID, JobRunning, nil)
	
	var err error
	switch job.Type {
	case JobIPMI:
		err = performIPMIAction(job.ServerID, job.Action)
	case JobPDU:
		err = performPDUAction(job.Target, job.Action)
	default:
		err = fmt.Errorf("unknown job type: %s", job.Type)
	}
	
	if err != nil {
		log.Printf("Job %s attempt %d failed: %v", job.ID, job.RetryCount+1, err)
		jq.handleJobFailure(job, err)
	} else {
		log.Printf("Job %s completed successfully", job.ID)
		jq.updateJobStatus(job.ID, JobCompleted, nil)
		
		// Trigger immediate status poll for the affected server after a short delay
		// to allow IPMI changes to take effect
		if job.ServerID != "" {
			go func(serverID string) {
				time.Sleep(2 * time.Second) // Wait for IPMI state to change
				pollServer(serverID)
			}(job.ServerID)
		}
	}
}

// handleJobFailure handles job failure with retry logic and exponential backoff
func (jq *JobQueue) handleJobFailure(job *Job, err error) {
	jq.mu.Lock()
	defer jq.mu.Unlock()
	
	// Store the error
	job.LastError = err.Error()
	job.RetryCount++
	
	// Check if we should retry
	if job.RetryCount <= job.MaxRetries {
		// Schedule retry with exponential backoff
		// Base delay: 5 seconds, exponentially increasing
		baseDelay := 5 * time.Second
		retryDelay := time.Duration(baseDelay.Nanoseconds() * (1 << (job.RetryCount - 1)))
		
		// Cap maximum delay at 60 seconds
		if retryDelay > 60*time.Second {
			retryDelay = 60 * time.Second
		}
		
		notBefore := time.Now().Add(retryDelay)
		job.NotBefore = &notBefore
		job.Status = JobWaiting
		
		log.Printf("Job %s scheduled for retry %d/%d in %v", job.ID, job.RetryCount, job.MaxRetries, retryDelay)
	} else {
		// Max retries exceeded, mark as failed
		job.Status = JobFailed
		job.Error = fmt.Sprintf("Failed after %d attempts. Last error: %s", job.RetryCount, job.LastError)
		now := time.Now()
		job.CompletedAt = &now
		
		log.Printf("Job %s failed permanently after %d attempts", job.ID, job.RetryCount)
	}
}