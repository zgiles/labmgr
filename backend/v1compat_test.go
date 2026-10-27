package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestV1SkillWorkflow drives /api/v1 exactly the way the lab-manager skill used by
// other agents does (same calls, same fields its jq filters read), so a server
// upgrade can't silently break them.
func TestV1SkillWorkflow(t *testing.T) {
	app, lab := newTestApp(t)
	router := app.router()

	call := func(method, path, body string) (int, map[string]any, []any) {
		t.Helper()
		var req *http.Request
		if body != "" {
			req = httptest.NewRequest(method, path, strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
		} else {
			req = httptest.NewRequest(method, path, nil)
		}
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		var obj map[string]any
		var arr []any
		if json.Unmarshal(rec.Body.Bytes(), &obj) != nil {
			json.Unmarshal(rec.Body.Bytes(), &arr)
		}
		return rec.Code, obj, arr
	}
	jobIDs := func(resp map[string]any) []string {
		var ids []string
		jobs, _ := resp["jobs"].([]any)
		for _, j := range jobs {
			ids = append(ids, j.(map[string]any)["id"].(string))
		}
		return ids
	}
	waitJob := func(id string) string {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			_, job, _ := call("GET", "/api/v1/jobs/"+id, "")
			if s := job["status"]; s == "completed" || s == "failed" {
				return s.(string)
			}
			time.Sleep(5 * time.Millisecond)
		}
		t.Fatalf("job %s never finished", id)
		return ""
	}

	// jq -r '.Servers[] | "\(.id)\t\(.name)\t\(.description)"'
	_, servers, _ := call("GET", "/api/v1/servers", "")
	list, _ := servers["Servers"].([]any)
	if len(list) != 3 || list[0].(map[string]any)["id"] != "both" || list[0].(map[string]any)["name"] == nil {
		t.Fatalf("/servers shape changed: %v", servers)
	}

	// jq -r '.status' (compared case-insensitively)
	_, st, _ := call("GET", "/api/v1/servers/bmconly/status?force=true", "")
	if st["status"] != "off" {
		t.Errorf("status = %v", st["status"])
	}
	// jq -r 'to_entries[] | "\(.key)\t\(.value.status)"'
	_, all, _ := call("GET", "/api/v1/status", "")
	if entry, ok := all["bmconly"].(map[string]any); !ok || entry["status"] == nil {
		t.Errorf("/status shape changed: %v", all)
	}

	// Power on, then poll each returned job id until completed/failed
	code, resp, _ := call("POST", "/api/v1/servers/bmconly/power", `{"action":"on"}`)
	if code != 200 || len(jobIDs(resp)) == 0 {
		t.Fatalf("power on: %d %v", code, resp)
	}
	for _, id := range jobIDs(resp) {
		if s := waitJob(id); s != "completed" {
			t.Errorf("power on job %s", s)
		}
	}
	_, st, _ = call("GET", "/api/v1/servers/bmconly/status?force=true", "")
	if strings.ToLower(st["status"].(string)) != "on" {
		t.Errorf("after power on, status = %v", st["status"])
	}

	// Direct IPMI and reboot still queue jobs
	for _, c := range []struct{ path, body string }{
		{"/api/v1/servers/bmconly/ipmi", `{"action":"reboot"}`},
		{"/api/v1/servers/bmconly/reboot", ""},
	} {
		code, resp, _ = call("POST", c.path, c.body)
		if code != 200 || len(jobIDs(resp)) == 0 {
			t.Fatalf("%s: %d %v", c.path, code, resp)
		}
		waitJob(jobIDs(resp)[0])
	}

	// PDU outlet direct
	code, resp, _ = call("POST", "/api/v1/pdu/pdu-x:3/power", `{"action":"on"}`)
	if code != 200 || len(jobIDs(resp)) == 0 {
		t.Fatalf("pdu: %d %v", code, resp)
	}
	waitJob(jobIDs(resp)[0])
	if lab.outlets["pdu-x:3"] != PowerOn {
		t.Error("outlet not switched")
	}

	// Jobs lists
	if _, _, jobs := call("GET", "/api/v1/jobs", ""); len(jobs) < 4 {
		t.Errorf("/jobs = %d entries", len(jobs))
	}
	if _, _, jobs := call("GET", "/api/v1/servers/bmconly/jobs", ""); len(jobs) < 3 {
		t.Errorf("/servers/:id/jobs = %d entries", len(jobs))
	}

	// Schedule, run-now, cancel, retry
	code, resp, _ = call("POST", "/api/v1/jobs/schedule",
		`{"server_id":"bmconly","action":"off","scheduled_time":"`+time.Now().Add(time.Hour).UTC().Format(time.RFC3339)+`","description":"Scheduled shutdown"}`)
	if code != 200 || len(jobIDs(resp)) != 1 {
		t.Fatalf("schedule: %d %v", code, resp)
	}
	scheduled := jobIDs(resp)[0]
	if _, job, _ := call("GET", "/api/v1/jobs/"+scheduled, ""); job["status"] != "waiting" {
		t.Errorf("scheduled job status = %v", job["status"])
	}
	if code, _, _ = call("DELETE", "/api/v1/jobs/"+scheduled, ""); code != 200 {
		t.Errorf("cancel: %d", code)
	}
	if code, _, _ = call("POST", "/api/v1/jobs/"+scheduled+"/retry", ""); code != 200 {
		t.Errorf("retry cancelled job: %d", code)
	}
	app.engine.Wait()

	// OS assignment round trip
	if code, _, _ = call("PUT", "/api/v1/servers/bmconly/os", `{"template_id":"local-disk","assigned_by":"claude"}`); code != 200 {
		t.Errorf("assign: %d", code)
	}
	if _, os, _ := call("GET", "/api/v1/servers/bmconly/os", ""); os["assignment"] == nil {
		t.Errorf("get assignment: %v", os)
	}
	if code, _, _ = call("POST", "/api/v1/os/bulk-assign", `{"server_ids":["both","bmconly"],"template_id":"local-disk","assigned_by":"claude"}`); code != 200 {
		t.Errorf("bulk assign: %d", code)
	}
	if _, a, _ := call("GET", "/api/v1/os/assignments", ""); len(a["assignments"].([]any)) != 2 {
		t.Errorf("assignments: %v", a)
	}
	if code, _, _ = call("DELETE", "/api/v1/servers/bmconly/os", ""); code != 200 {
		t.Errorf("clear: %d", code)
	}
	if _, tmpl, _ := call("GET", "/api/v1/os/templates", ""); tmpl["templates"] == nil {
		t.Errorf("templates: %v", tmpl)
	}

	// Asking for what's already true is a clear error rather than a pointless job.
	// (The retry above ran the power off immediately, as v1 retry always did.)
	_, resp, _ = call("POST", "/api/v1/servers/bmconly/power", `{"action":"on"}`)
	waitJob(jobIDs(resp)[0])
	app.poller.PollNow(context.Background(), "bmconly")
	if code, resp, _ = call("POST", "/api/v1/servers/bmconly/power", `{"action":"on"}`); code != http.StatusConflict || !strings.Contains(resp["error"].(string), "already on") {
		t.Errorf("power on when on: %d %v", code, resp)
	}
}
