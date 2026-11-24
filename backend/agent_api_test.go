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

func TestServersResolveByNameOrHostname(t *testing.T) {
	app, _ := newTestApp(t)
	router := app.router()
	for _, ref := range []string{"bmconly", "BMCONLY", "bmc-only"} { // ID, any case, metadata hostname
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v2/servers/"+ref, nil))
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"id":"bmconly"`) {
			t.Errorf("%s: %d %s", ref, rec.Code, rec.Body)
		}
	}

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v2/servers/nosuchserver/bmc", nil))
	var body struct {
		Error   string   `json:"error"`
		Servers []string `json:"servers"`
	}
	json.Unmarshal(rec.Body.Bytes(), &body)
	if rec.Code != http.StatusNotFound || len(body.Servers) != 3 || !strings.Contains(body.Error, "nosuchserver") {
		t.Errorf("unknown server: %d %s", rec.Code, rec.Body)
	}

	// Operation routes share the :id name but aren't servers
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v2/operations/op-nope", nil))
	if rec.Code != http.StatusNotFound || strings.Contains(rec.Body.String(), "servers") {
		t.Errorf("operation lookup went through server resolution: %s", rec.Body)
	}
}

func TestSubmitWithWaitReturnsTheOutcome(t *testing.T) {
	app, _ := newTestApp(t)
	app.poller.PollNow(context.Background(), "bmconly")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v2/servers/bmc-only/operations?wait=10", strings.NewReader(`{"kind":"power_on"}`))
	req.Header.Set("Content-Type", "application/json")
	app.router().ServeHTTP(rec, req)
	var op Operation
	json.Unmarshal(rec.Body.Bytes(), &op)
	if rec.Code != http.StatusOK || op.Status != OpSucceeded || op.ServerID != "bmconly" {
		t.Fatalf("got %d %s / %s", rec.Code, op.Status, rec.Body)
	}

	// Without wait it returns while still running
	app.poller.PollNow(context.Background(), "bmconly")
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/v2/servers/bmconly/operations", strings.NewReader(`{"kind":"restart"}`))
	req.Header.Set("Content-Type", "application/json")
	app.router().ServeHTTP(rec, req)
	json.Unmarshal(rec.Body.Bytes(), &op)
	rec = httptest.NewRecorder()
	app.router().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v2/operations/"+op.ID+"?wait=10", nil))
	json.Unmarshal(rec.Body.Bytes(), &op)
	if rec.Code != http.StatusOK || op.Status != OpSucceeded {
		t.Errorf("GET with wait: %d %s", rec.Code, op.Status)
	}
}

func TestTextOverviewAndHardwareFacts(t *testing.T) {
	app, lab := newTestApp(t)
	lab.set("bmconly", PowerOn, "")
	app.poller.PollNow(context.Background(), "bmconly")
	app.poller.readHardwareIfDue(context.Background(), app.config.Get().Server("bmconly"))
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		if st, _ := app.state.Get("bmconly"); st.Hardware != nil {
			break
		}
	}
	op := submit(t, app, OpRequest{ServerID: "bmconly", Kind: OpRestart})
	if op.Status != OpSucceeded {
		t.Fatal(op.Error)
	}

	rec := httptest.NewRecorder()
	app.router().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v2/servers?format=text", nil))
	text := rec.Body.String()
	for _, want := range []string{"ID", "STATE", "WATTS", "bmconly", "on", "Dell Inc. Fake", "Restart bmconly succeeded"} {
		if !strings.Contains(text, want) {
			t.Errorf("text overview missing %q:\n%s", want, text)
		}
	}

	rec = httptest.NewRecorder()
	app.router().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v2/servers/bmconly", nil))
	if !strings.Contains(rec.Body.String(), `"last_operation":{"id"`) || !strings.Contains(rec.Body.String(), `"manufacturer":"Dell Inc."`) {
		t.Errorf("server view lacks last_operation/hardware: %s", rec.Body)
	}
}
