package main

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// The v1 API predates operations. These handlers keep it working for scripts
// (and the lab-manager skill) by translating to and from the engine.

type legacyJob struct {
	ID          string     `json:"id"`
	Type        string     `json:"type"`
	ServerID    string     `json:"server_id,omitempty"`
	Action      string     `json:"action"`
	Target      string     `json:"target,omitempty"`
	Status      string     `json:"status"`
	CreatedAt   time.Time  `json:"created_at"`
	StartedAt   *time.Time `json:"started_at,omitempty"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
	NotBefore   *time.Time `json:"not_before,omitempty"`
	Error       string     `json:"error,omitempty"`
	Description string     `json:"description"`
	Steps       []*Step    `json:"steps,omitempty"`
}

var legacyJobStatus = map[OpStatus]string{
	OpScheduled:   "waiting",
	OpPending:     "pending",
	OpRunning:     "running",
	OpSucceeded:   "completed",
	OpFailed:      "failed",
	OpCancelled:   "failed",
	OpInterrupted: "failed",
}

func toLegacyJob(op Operation) legacyJob {
	job := legacyJob{
		ID:          op.ID,
		Type:        "ipmi",
		ServerID:    op.ServerID,
		Action:      op.Kind,
		Target:      op.Target,
		Status:      legacyJobStatus[op.Status],
		CreatedAt:   op.CreatedAt,
		StartedAt:   op.StartedAt,
		CompletedAt: op.FinishedAt,
		NotBefore:   op.ScheduledFor,
		Error:       op.Error,
		Description: op.Label,
		Steps:       op.Steps,
	}
	if op.Kind == OpOutlet {
		job.Type = "pdu"
		job.Action = op.Action
	}
	if op.Kind == OpBMC {
		job.Action = op.Action
	}
	return job
}

func toLegacyJobs(ops []Operation) []legacyJob {
	jobs := make([]legacyJob, len(ops))
	for i, op := range ops {
		jobs[i] = toLegacyJob(op)
	}
	return jobs
}

type legacyStatus struct {
	ServerID       string     `json:"server_id"`
	Status         string     `json:"status"`
	SystemStatus   string     `json:"system_status"`
	PowerStatus    string     `json:"power_status"`
	LastChecked    *time.Time `json:"last_checked,omitempty"`
	IsReachable    bool       `json:"is_reachable"`
	IsTransitional bool       `json:"is_transitional"`
	IPMIReachable  bool       `json:"ipmi_reachable"`
	PDUReachable   bool       `json:"pdu_reachable"`
	IPMIError      string     `json:"ipmi_error,omitempty"`
	PDUError       string     `json:"pdu_error,omitempty"`
}

func toLegacyStatus(st ServerState) legacyStatus {
	ls := legacyStatus{
		ServerID:       st.ServerID,
		Status:         st.State,
		IsTransitional: st.Operation != nil,
		IPMIReachable:  st.BMC == LinkOK,
		PDUReachable:   st.PDU == LinkOK,
		IPMIError:      st.BMCError,
		PDUError:       st.PDUError,
		LastChecked:    st.BMCCheckedAt,
	}
	if ls.LastChecked == nil {
		ls.LastChecked = st.PDUCheckedAt
	}
	switch st.BMC {
	case LinkNone:
		ls.SystemStatus = "no_ipmi"
	case LinkOK:
		ls.SystemStatus = map[PowerState]string{PowerOn: "On", PowerOff: "Off"}[st.Power]
		if ls.SystemStatus == "" {
			ls.SystemStatus = "unknown"
		}
	default:
		ls.SystemStatus = "unreachable"
	}
	switch st.PDU {
	case LinkNone:
		ls.PowerStatus = "no_pdu"
	case LinkOK:
		ls.PowerStatus = string(st.outletsAggregate())
	default:
		ls.PowerStatus = "unreachable"
	}
	ls.IsReachable = ls.IPMIReachable || ls.PDUReachable
	return ls
}

func (a *App) registerV1(api *gin.RouterGroup) {
	api.GET("/servers", func(c *gin.Context) { c.JSON(http.StatusOK, a.config.Get().Public()) })
	api.GET("/servers/:id", func(c *gin.Context) {
		for _, server := range a.config.Get().Public().Servers {
			if server.ID == c.Param("id") {
				c.JSON(http.StatusOK, server)
				return
			}
		}
		c.JSON(http.StatusNotFound, gin.H{"error": "Server not found"})
	})
	api.POST("/servers/:id/power", a.v1Power)
	api.POST("/servers/:id/reboot", func(c *gin.Context) {
		a.v1Submit(c, OpRequest{ServerID: c.Param("id"), Kind: OpPowerCycle})
	})
	api.POST("/servers/:id/ipmi", func(c *gin.Context) {
		var req struct {
			Action string `json:"action"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		a.v1Submit(c, OpRequest{ServerID: c.Param("id"), Kind: OpBMC, Action: req.Action})
	})
	api.GET("/servers/:id/status", a.v1ServerStatus)
	api.GET("/status", a.v1AllStatus)
	api.POST("/pdu/:outlet/power", func(c *gin.Context) {
		var req struct {
			Action string `json:"action"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		a.v1Submit(c, OpRequest{Kind: OpOutlet, Target: c.Param("outlet"), Action: req.Action})
	})
	api.GET("/boot/:mac", a.getBootScript)
	api.GET("/autoexec.ipxe", a.getAutoExecScript)

	api.GET("/jobs", func(c *gin.Context) { c.JSON(http.StatusOK, toLegacyJobs(a.engine.List(OpFilter{}))) })
	api.GET("/jobs/:id", func(c *gin.Context) {
		op, ok := a.engine.Get(c.Param("id"))
		if !ok {
			c.JSON(http.StatusNotFound, gin.H{"error": "Job not found"})
			return
		}
		c.JSON(http.StatusOK, toLegacyJob(op))
	})
	api.GET("/servers/:id/jobs", func(c *gin.Context) {
		c.JSON(http.StatusOK, toLegacyJobs(a.engine.List(OpFilter{ServerID: c.Param("id")})))
	})
	api.DELETE("/jobs/:id", func(c *gin.Context) {
		if err := a.engine.Cancel(c.Param("id")); err != nil {
			respondError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "cancelled"})
	})
	api.POST("/jobs/:id/run-now", func(c *gin.Context) {
		if err := a.engine.RunNow(c.Param("id")); err != nil {
			respondError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "marked for immediate execution"})
	})
	api.POST("/jobs/:id/retry", func(c *gin.Context) {
		op, err := a.engine.Retry(c.Request.Context(), c.Param("id"), requester(c, ""))
		if err != nil {
			respondError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "job retry initiated", "job": toLegacyJob(op)})
	})
	api.POST("/jobs/schedule", a.v1Schedule)

	registerOSRoutes(api, a)
}

// v1PowerKinds maps the old power endpoint's actions to operations
var v1PowerKinds = map[string]string{
	"on":                OpPowerOn,
	"off":               OpPowerOff,
	"reboot":            OpPowerCycle,
	"graceful_shutdown": OpGracefulShutdown,
	"graceful_restart":  OpGracefulRestart,
}

func (a *App) v1Power(c *gin.Context) {
	var req struct {
		Action string `json:"action"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	kind, ok := v1PowerKinds[req.Action]
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("invalid action %q", req.Action)})
		return
	}
	a.v1Submit(c, OpRequest{ServerID: c.Param("id"), Kind: kind})
}

func (a *App) v1Submit(c *gin.Context, req OpRequest) {
	req.RequestedBy = requester(c, "")
	req.Source = "api-v1"
	op, err := a.engine.Submit(c.Request.Context(), req)
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"status":    "queued",
		"jobs":      []legacyJob{toLegacyJob(op)},
		"job":       toLegacyJob(op),
		"operation": op,
		"message":   fmt.Sprintf("Queued: %s (%d steps)", op.Label, len(op.Steps)),
	})
}

func (a *App) v1Schedule(c *gin.Context) {
	var req struct {
		ServerID      string `json:"server_id"`
		Action        string `json:"action"`
		ScheduledTime string `json:"scheduled_time"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	at, err := time.Parse(time.RFC3339, req.ScheduledTime)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid scheduled_time format (want RFC3339)"})
		return
	}
	opReq := OpRequest{ServerID: req.ServerID, ScheduledFor: &at, RequestedBy: requester(c, ""), Source: "api-v1"}
	if kind, ok := v1PowerKinds[req.Action]; ok {
		opReq.Kind = kind
	} else {
		opReq.Kind, opReq.Action = OpBMC, req.Action
	}
	op, err := a.engine.Submit(c.Request.Context(), opReq)
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"status":    "scheduled",
		"jobs":      []legacyJob{toLegacyJob(op)},
		"operation": op,
		"message":   fmt.Sprintf("Scheduled %s for %s", op.Label, at.Format("2006-01-02 15:04:05")),
	})
}

func (a *App) v1ServerStatus(c *gin.Context) {
	id := c.Param("id")
	var st ServerState
	var ok bool
	if c.Query("force") == "true" {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
		defer cancel()
		st, ok = a.poller.PollNow(ctx, id)
	} else {
		st, ok = a.state.Get(id)
	}
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "Server not found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": st.State, "detail": toLegacyStatus(st)})
}

func (a *App) v1AllStatus(c *gin.Context) {
	if c.Query("force") == "true" {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 20*time.Second)
		defer cancel()
		a.poller.PollAllNow(ctx)
	}
	statuses := make(map[string]legacyStatus)
	for _, st := range a.state.All() {
		statuses[st.ServerID] = toLegacyStatus(st)
	}
	c.JSON(http.StatusOK, statuses)
}
