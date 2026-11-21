package main

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// ServerView is a server as clients see it: config without credentials, plus live state
type ServerView struct {
	ID            string            `json:"id"`
	Name          string            `json:"name"`
	Description   string            `json:"description"`
	Metadata      map[string]string `json:"metadata"`
	BMCHost       string            `json:"bmc_host,omitempty"`
	HasBMC        bool              `json:"has_bmc"`
	Outlets       []string          `json:"outlets"`
	MAC           string            `json:"mac,omitempty"`
	State         ServerState       `json:"state"`
	OS            *OSView           `json:"os,omitempty"`
	Console       ConsoleView       `json:"console"`
	LastOperation *OperationBrief   `json:"last_operation,omitempty"`
}

type OSView struct {
	TemplateID   string    `json:"template_id"`
	TemplateName string    `json:"template_name"`
	BootMethod   string    `json:"boot_method"`
	OneShot      bool      `json:"one_shot"`
	AssignedAt   time.Time `json:"assigned_at"`
	AssignedBy   string    `json:"assigned_by,omitempty"`
	ThenTemplate string    `json:"then_template,omitempty"` // after a one-shot: what comes back
}

type ConsoleView struct {
	WebURL string `json:"web_url,omitempty"` // the BMC's own web UI / KVM
	Serial bool   `json:"serial"`            // serial console available through labmgr
}

func (a *App) serverView(server *Server) ServerView {
	st, _ := a.state.Get(server.ID)
	view := ServerView{
		ID:          server.ID,
		Name:        server.Name,
		Description: server.Description,
		Metadata:    server.Metadata,
		BMCHost:     server.IPMI.Host,
		HasBMC:      server.HasBMC(),
		Outlets:     append([]string{}, server.PDUOutlets...),
		MAC:         server.MAC(),
		State:       st,
		Console:     consoleView(server),
	}
	if a.engine != nil {
		view.LastOperation = a.lastOperation(server.ID)
	}
	if view.Metadata == nil {
		view.Metadata = map[string]string{}
	}
	if assignment, ok := osManager.GetServerOSAssignment(server.ID); ok {
		tmpl, _ := osManager.GetTemplate(assignment.TemplateID)
		view.OS = &OSView{
			TemplateID:   assignment.TemplateID,
			TemplateName: tmpl.Name,
			BootMethod:   tmpl.BootMethod,
			OneShot:      assignment.OneShot,
			AssignedAt:   assignment.AssignedAt,
			AssignedBy:   assignment.AssignedBy,
		}
		if assignment.OneShot && assignment.Previous != nil {
			view.OS.ThenTemplate = assignment.Previous.TemplateID
		}
	}
	return view
}

func consoleView(server *Server) ConsoleView {
	view := ConsoleView{Serial: server.HasBMC()}
	if server.Console.WebURL != "" {
		view.WebURL = server.Console.WebURL
	} else if server.HasBMC() {
		host := server.IPMI.Host
		if !strings.Contains(host, "://") {
			host = "https://" + host
		}
		view.WebURL = host
	}
	return view
}

func (a *App) serverViews() []ServerView {
	config := a.config.Get()
	views := make([]ServerView, 0, len(config.Servers))
	for i := range config.Servers {
		views = append(views, a.serverView(&config.Servers[i]))
	}
	return views
}

// respondError maps engine errors to HTTP statuses
func respondError(c *gin.Context, err error) {
	var opErr *OpError
	if errors.As(err, &opErr) {
		c.JSON(opErr.Status, gin.H{"error": opErr.Msg})
		return
	}
	c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
}

// requester identifies who asked for something, for the activity log
func requester(c *gin.Context, given string) string {
	who := given
	if who == "" {
		who = c.GetHeader("X-Labmgr-User")
	}
	if who == "" {
		who = "api"
		if c.GetHeader("X-Labmgr-Client") == "web" {
			who = "web"
		}
	}
	return who + "@" + c.ClientIP()
}

func (a *App) recordEvent(e *Event) {
	if err := a.store.AppendEvent(e); err != nil {
		return
	}
	a.hub.Publish("event", e)
}

func (a *App) registerV2(api *gin.RouterGroup) {
	api.GET("/info", a.v2Info)
	api.GET("/stream", a.v2Stream)
	api.GET("/servers", func(c *gin.Context) {
		if c.Query("format") == "text" {
			c.String(http.StatusOK, a.serversText(a.serverViews()))
			return
		}
		c.JSON(http.StatusOK, a.serverViews())
	})
	api.GET("/servers/:id", a.v2Server)
	api.POST("/servers/:id/operations", a.v2SubmitOperation)
	api.GET("/servers/:id/operations", func(c *gin.Context) {
		c.JSON(http.StatusOK, a.engine.List(OpFilter{ServerID: c.Param("id"), Limit: queryInt(c, "limit", 50)}))
	})
	api.POST("/servers/:id/refresh", a.v2Refresh)
	api.GET("/servers/:id/events", func(c *gin.Context) {
		events, err := a.store.Events(c.Param("id"), queryInt(c, "limit", 100))
		if err != nil {
			respondError(c, err)
			return
		}
		c.JSON(http.StatusOK, events)
	})
	api.GET("/servers/:id/boot-script", a.v2BootScript)
	api.GET("/servers/:id/power", a.v2PowerHistory)
	api.GET("/power", a.v2PowerAll)
	api.GET("/servers/:id/bmc", a.v2BMCInfo)
	api.GET("/servers/:id/bmc/logs", a.v2BMCLogs)
	api.GET("/servers/:id/console", a.v2Console)
	api.GET("/servers/:id/console/log", a.v2ConsoleLog)
	api.GET("/servers/:id/console/captures", a.v2ConsoleCaptures)
	api.GET("/servers/:id/console/captures/:name", a.v2ConsoleCapture)
	api.POST("/servers/:id/kvm", a.v2KVM)
	api.POST("/servers/:id/kvm/token", a.v2KVMToken)

	api.POST("/refresh", func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 20*time.Second)
		defer cancel()
		a.poller.PollAllNow(ctx)
		c.JSON(http.StatusOK, a.serverViews())
	})

	api.GET("/operations", func(c *gin.Context) {
		c.JSON(http.StatusOK, a.engine.List(OpFilter{
			ServerID: c.Query("server"),
			Active:   c.Query("active") == "true" || c.Query("active") == "1",
			Limit:    queryInt(c, "limit", 100),
		}))
	})
	api.GET("/operations/:id", func(c *gin.Context) {
		op, ok := a.engine.Get(c.Param("id"))
		if !ok {
			c.JSON(http.StatusNotFound, gin.H{"error": "operation not found"})
			return
		}
		a.respondOperation(c, op)
	})
	api.POST("/operations/:id/cancel", func(c *gin.Context) {
		if err := a.engine.Cancel(c.Param("id")); err != nil {
			respondError(c, err)
			return
		}
		op, _ := a.engine.Get(c.Param("id"))
		c.JSON(http.StatusOK, op)
	})
	api.POST("/operations/:id/run-now", func(c *gin.Context) {
		if err := a.engine.RunNow(c.Param("id")); err != nil {
			respondError(c, err)
			return
		}
		op, _ := a.engine.Get(c.Param("id"))
		c.JSON(http.StatusOK, op)
	})
	api.POST("/operations/:id/retry", func(c *gin.Context) {
		op, err := a.engine.Retry(c.Request.Context(), c.Param("id"), requester(c, ""))
		if err != nil {
			respondError(c, err)
			return
		}
		c.JSON(http.StatusAccepted, op)
	})

	api.GET("/events", func(c *gin.Context) {
		events, err := a.store.Events("", queryInt(c, "limit", 200))
		if err != nil {
			respondError(c, err)
			return
		}
		c.JSON(http.StatusOK, events)
	})

	registerOSRoutes(api, a)
}

func queryInt(c *gin.Context, name string, def int) int {
	if n, err := strconv.Atoi(c.Query(name)); err == nil && n > 0 {
		return n
	}
	return def
}

func (a *App) v2Info(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"version": version,
		"delays":  a.config.Get().Delays,
		"time":    time.Now(),
	})
}

func (a *App) v2Stream(c *gin.Context) {
	serveStream(c, a.hub, func() any {
		return gin.H{
			"servers":    a.serverViews(),
			"operations": a.engine.List(OpFilter{Limit: 200}),
		}
	})
}

func (a *App) v2Server(c *gin.Context) {
	server := a.config.Get().Server(c.Param("id"))
	if server == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "server not found"})
		return
	}
	c.JSON(http.StatusOK, a.serverView(server))
}

func (a *App) v2SubmitOperation(c *gin.Context) {
	var req OpRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	req.ServerID = c.Param("id")
	req.RequestedBy = requester(c, req.RequestedBy)
	req.Source = "api"
	op, err := a.engine.Submit(c.Request.Context(), req)
	if err != nil {
		respondError(c, err)
		return
	}
	a.respondOperation(c, op)
}

func (a *App) v2Refresh(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()
	if _, ok := a.poller.PollNow(ctx, c.Param("id")); !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "server not found"})
		return
	}
	a.v2Server(c)
}

// powerRange reads ?hours= (default 1, at most a week)
func powerRange(c *gin.Context) time.Time {
	hours := queryInt(c, "hours", 1)
	if hours > 24*7 {
		hours = 24 * 7
	}
	return time.Now().Add(-time.Duration(hours) * time.Hour)
}

// v2PowerHistory returns one server's power readings, at most 600 points
func (a *App) v2PowerHistory(c *gin.Context) {
	if a.config.Get().Server(c.Param("id")) == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "server not found"})
		return
	}
	samples, err := a.store.PowerHistory(c.Param("id"), powerRange(c), 600)
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"samples": samples})
}

// v2PowerAll returns every server's recent readings, sized for table sparklines
func (a *App) v2PowerAll(c *gin.Context) {
	since := powerRange(c)
	out := make(map[string][]PowerSample)
	for _, server := range a.config.Get().Servers {
		samples, err := a.store.PowerHistory(server.ID, since, 60)
		if err != nil {
			respondError(c, err)
			return
		}
		if len(samples) > 0 {
			out[server.ID] = samples
		}
	}
	c.JSON(http.StatusOK, out)
}

func (a *App) v2BootScript(c *gin.Context) {
	server := a.config.Get().Server(c.Param("id"))
	if server == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "server not found"})
		return
	}
	mac := server.MAC()
	if mac == "" {
		mac = "00:00:00:00:00:00"
	}
	c.JSON(http.StatusOK, renderBootScript(server, mac, "<server-ip>"))
}

func (a *App) bmcForRequest(c *gin.Context) (BMC, bool) {
	server := a.config.Get().Server(c.Param("id"))
	if server == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "server not found"})
		return nil, false
	}
	bmc := a.hw.BMC(server)
	if bmc == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": server.Name + " has no BMC configured"})
		return nil, false
	}
	return bmc, true
}

// v2BMCInfo returns hardware details and the current boot override
func (a *App) v2BMCInfo(c *gin.Context) {
	bmc, ok := a.bmcForRequest(c)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 20*time.Second)
	defer cancel()
	info, err := bmc.Info(ctx)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	boot, err := bmc.BootOverride(ctx)
	resp := gin.H{"info": info, "boot": boot}
	if err != nil {
		resp["boot_error"] = err.Error()
	}
	c.JSON(http.StatusOK, resp)
}

func (a *App) v2BMCLogs(c *gin.Context) {
	bmc, ok := a.bmcForRequest(c)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	logs, err := bmc.Logs(ctx, queryInt(c, "limit", 50))
	if err != nil && len(logs) == 0 {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, logs)
}
