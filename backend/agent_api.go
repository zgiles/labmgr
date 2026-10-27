package main

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/gin-gonic/gin"
)

// Conveniences for scripts and agents: servers by name, waiting for results in one
// call, and a plain-text overview that reads without jq.

const maxWait = 10 * time.Minute

// resolveServer matches what someone called a server (its ID, name, or hostname,
// any case) to a server ID. Ambiguous names don't resolve.
func resolveServer(config *Config, raw string) (string, bool) {
	if config.Server(raw) != nil {
		return raw, true
	}
	want := strings.ToLower(strings.TrimSpace(raw))
	match := ""
	for i := range config.Servers {
		s := &config.Servers[i]
		if strings.ToLower(s.ID) == want || strings.ToLower(s.Name) == want || strings.ToLower(s.Hostname()) == want {
			if match != "" && match != s.ID {
				return "", false
			}
			match = s.ID
		}
	}
	return match, match != ""
}

// resolveServerParam lets every /servers/:id route take a name or hostname too,
// and answers unknown servers with the list of real ones so the caller can correct itself
func (a *App) resolveServerParam() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !strings.Contains(c.FullPath(), "/servers/:id") {
			c.Next()
			return
		}
		config := a.config.Get()
		raw := c.Param("id")
		id, ok := resolveServer(config, raw)
		if !ok {
			ids := make([]string, len(config.Servers))
			for i, s := range config.Servers {
				ids[i] = s.ID
			}
			c.AbortWithStatusJSON(http.StatusNotFound, gin.H{
				"error":   fmt.Sprintf("no server called %q", raw),
				"servers": ids,
			})
			return
		}
		for i := range c.Params {
			if c.Params[i].Key == "id" {
				c.Params[i].Value = id
			}
		}
		c.Next()
	}
}

// waitParam reads ?wait=<seconds>, capped at maxWait
func waitParam(c *gin.Context) time.Duration {
	seconds := queryInt(c, "wait", 0)
	d := time.Duration(seconds) * time.Second
	if d > maxWait {
		d = maxWait
	}
	return d
}

// WaitFor blocks until an operation finishes (or is scheduled for later), the timeout passes, or ctx ends
func (e *Engine) WaitFor(ctx context.Context, id string, timeout time.Duration) (Operation, bool) {
	deadline := time.Now().Add(timeout)
	for {
		op, ok := e.Get(id)
		if !ok || op.Status.Finished() || op.Status == OpScheduled || time.Now().After(deadline) {
			return op, ok
		}
		select {
		case <-ctx.Done():
			return op, true
		case <-time.After(250 * time.Millisecond):
		}
	}
}

// respondOperation answers with the operation, waiting first if asked to.
// 200 once finished (or scheduled), 202 while it's still running.
func (a *App) respondOperation(c *gin.Context, op Operation) {
	if wait := waitParam(c); wait > 0 {
		op, _ = a.engine.WaitFor(c.Request.Context(), op.ID, wait)
	}
	status := http.StatusAccepted
	if op.Status.Finished() || op.Status == OpScheduled {
		status = http.StatusOK
	}
	c.JSON(status, op)
}

// OperationBrief is a server's most recent finished operation
type OperationBrief struct {
	ID         string     `json:"id"`
	Kind       string     `json:"kind"`
	Label      string     `json:"label"`
	Status     OpStatus   `json:"status"`
	Error      string     `json:"error,omitempty"`
	Note       string     `json:"note,omitempty"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}

func (a *App) lastOperation(serverID string) *OperationBrief {
	for _, op := range a.engine.List(OpFilter{ServerID: serverID}) {
		if op.Status.Finished() {
			return &OperationBrief{ID: op.ID, Kind: op.Kind, Label: op.Label, Status: op.Status, Error: op.Error, Note: op.Note, FinishedAt: op.FinishedAt}
		}
	}
	return nil
}

// serversText renders the overview as an aligned plain-text table
func (a *App) serversText(views []ServerView) string {
	var b strings.Builder
	w := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tNAME\tSTATE\tWATTS\tHARDWARE\tNETWORK BOOT\tACTIVITY")
	sort.SliceStable(views, func(i, j int) bool { return views[i].ID < views[j].ID })
	for _, v := range views {
		watts := "-"
		if v.State.Watts != nil {
			watts = fmt.Sprintf("%.0f", *v.State.Watts)
		}
		hw := "-"
		if h := v.State.Hardware; h != nil {
			hw = strings.TrimSpace(h.Manufacturer + " " + h.Model)
			if h.Health != "" && h.Health != "OK" {
				hw += " (" + h.Health + ")"
			}
		}
		boot := "local disk"
		if v.OS != nil {
			boot = v.OS.TemplateName
			if v.OS.OneShot {
				boot += " (next boot only)"
			}
		}
		activity := "-"
		if op := v.State.Operation; op != nil {
			activity = fmt.Sprintf("%s: step %d/%d %s", op.Label, op.Step, op.Steps, op.StepLabel)
		} else if last := v.LastOperation; last != nil {
			activity = fmt.Sprintf("%s %s", last.Label, last.Status)
			if last.FinishedAt != nil {
				activity += " " + agoText(*last.FinishedAt)
			}
			if last.Error != "" {
				activity += ": " + last.Error
			}
		}
		state := v.State.State
		if v.State.State == StateUnreachable && v.State.BMCError != "" {
			state += " (" + v.State.BMCError + ")"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", v.ID, v.Name, state, watts, hw, boot, activity)
	}
	w.Flush()
	return b.String()
}

func agoText(t time.Time) string {
	d := time.Since(t).Round(time.Minute)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
	return fmt.Sprintf("%dd ago", int(d.Hours()/24))
}
