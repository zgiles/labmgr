package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

func assertCalls(t *testing.T, lab *fakeLab, want ...string) {
	t.Helper()
	if got := lab.Calls(); !reflect.DeepEqual(got, want) {
		t.Errorf("hardware calls:\n got  %q\n want %q", got, want)
	}
}

func assertState(t *testing.T, app *App, serverID, want string) {
	t.Helper()
	st, _ := app.poller.PollNow(context.Background(), serverID)
	if st.State != want {
		t.Errorf("%s state = %s, want %s (%+v)", serverID, st.State, want, st)
	}
}

func TestPowerOnFromDark(t *testing.T) {
	app, lab := newTestApp(t)
	assertState(t, app, "both", StateOff) // outlets off, BMC dark

	op := submit(t, app, OpRequest{ServerID: "both", Kind: OpPowerOn})
	if op.Status != OpSucceeded {
		t.Fatalf("power on %s: %s", op.Status, op.Error)
	}
	assertCalls(t, lab, "outlet pdu-x:1 on", "outlet pdu-x:2 on", "bmc both on")
	assertState(t, app, "both", StateOn)
}

func TestPowerOffStopsSystemBeforeCuttingOutlets(t *testing.T) {
	app, lab := newTestApp(t)
	lab.set("both", PowerOn, PowerOn)

	op := submit(t, app, OpRequest{ServerID: "both", Kind: OpPowerOff})
	if op.Status != OpSucceeded {
		t.Fatalf("power off %s: %s", op.Status, op.Error)
	}
	assertCalls(t, lab, "bmc both off", "outlet pdu-x:1 off", "outlet pdu-x:2 off")
	assertState(t, app, "both", StateOff)
}

func TestPowerCycleRunsOffThenOn(t *testing.T) {
	app, lab := newTestApp(t)
	lab.set("both", PowerOn, PowerOn)

	op := submit(t, app, OpRequest{ServerID: "both", Kind: OpPowerCycle})
	if op.Status != OpSucceeded {
		t.Fatalf("power cycle %s: %s", op.Status, op.Error)
	}
	assertCalls(t, lab,
		"bmc both off", "outlet pdu-x:1 off", "outlet pdu-x:2 off",
		"outlet pdu-x:1 on", "outlet pdu-x:2 on", "bmc both on")
	assertState(t, app, "both", StateOn)
}

func TestRestartUsesBMCReset(t *testing.T) {
	app, lab := newTestApp(t)
	lab.set("bmconly", PowerOn, "")

	op := submit(t, app, OpRequest{ServerID: "bmconly", Kind: OpRestart})
	if op.Status != OpSucceeded {
		t.Fatalf("restart %s: %s", op.Status, op.Error)
	}
	assertCalls(t, lab, "bmc bmconly reboot")
}

func TestPDUOnlyServer(t *testing.T) {
	app, lab := newTestApp(t)
	op := submit(t, app, OpRequest{ServerID: "pduonly", Kind: OpPowerOn})
	if op.Status != OpSucceeded {
		t.Fatalf("power on %s: %s", op.Status, op.Error)
	}
	assertCalls(t, lab, "outlet pdu-x:3 on")
	assertState(t, app, "pduonly", StateOn)
}

func TestAlreadyOnIsAConflict(t *testing.T) {
	app, lab := newTestApp(t)
	lab.set("bmconly", PowerOn, "")
	app.poller.PollNow(context.Background(), "bmconly")

	_, err := app.engine.Submit(context.Background(), OpRequest{ServerID: "bmconly", Kind: OpPowerOn})
	var opErr *OpError
	if !errors.As(err, &opErr) || opErr.Status != 409 || !strings.Contains(opErr.Msg, "already on") {
		t.Fatalf("want 409 already on, got %v", err)
	}
	assertCalls(t, lab)
}

func TestOneOperationPerServer(t *testing.T) {
	app, lab := newTestApp(t)
	lab.gate = make(chan struct{})
	app.poller.PollNow(context.Background(), "bmconly")

	first, err := app.engine.Submit(context.Background(), OpRequest{ServerID: "bmconly", Kind: OpPowerOn})
	if err != nil {
		t.Fatal(err)
	}
	_, err = app.engine.Submit(context.Background(), OpRequest{ServerID: "bmconly", Kind: OpPowerOff})
	var opErr *OpError
	if !errors.As(err, &opErr) || opErr.Status != 409 || !strings.Contains(opErr.Msg, "busy") {
		t.Fatalf("want 409 busy, got %v", err)
	}

	st, _ := app.state.Get("bmconly")
	if st.State != StatePoweringOn {
		t.Errorf("state during power on = %s", st.State)
	}

	close(lab.gate)
	if op := waitOp(t, app, first.ID); op.Status != OpSucceeded {
		t.Fatalf("first op %s: %s", op.Status, op.Error)
	}
}

func TestBMCActionRetries(t *testing.T) {
	app, lab := newTestApp(t)
	lab.failResets["bmconly"] = 2

	op := submit(t, app, OpRequest{ServerID: "bmconly", Kind: OpPowerOn})
	if op.Status != OpSucceeded {
		t.Fatalf("power on %s: %s", op.Status, op.Error)
	}
	if op.Steps[0].Attempts != 3 {
		t.Errorf("attempts = %d, want 3", op.Steps[0].Attempts)
	}
}

func TestBMCActionGivesUp(t *testing.T) {
	app, lab := newTestApp(t)
	lab.failResets["bmconly"] = 100

	op := submit(t, app, OpRequest{ServerID: "bmconly", Kind: OpPowerOn})
	if op.Status != OpFailed || !strings.Contains(op.Error, "BMC busy") {
		t.Fatalf("want failure mentioning the BMC error, got %s: %s", op.Status, op.Error)
	}
	if op.Steps[1].Status != StepSkipped {
		t.Errorf("later step should be skipped, is %s", op.Steps[1].Status)
	}
}

func TestScheduledOperationRunsWhenDue(t *testing.T) {
	app, lab := newTestApp(t)
	at := time.Now().Add(1500 * time.Millisecond)

	op := submit(t, app, OpRequest{ServerID: "bmconly", Kind: OpPowerOn, ScheduledFor: &at})
	if op.Status != OpScheduled {
		t.Fatalf("status = %s", op.Status)
	}
	if len(lab.Calls()) != 0 {
		t.Fatal("ran before its time")
	}
	if done := waitOp(t, app, op.ID); done.Status != OpSucceeded {
		t.Fatalf("scheduled op %s: %s", done.Status, done.Error)
	}
	assertCalls(t, lab, "bmc bmconly on")
}

func TestScheduledNothingToDoSucceedsWithNote(t *testing.T) {
	app, lab := newTestApp(t)
	lab.set("bmconly", PowerOn, "")
	at := time.Now().Add(time.Hour)

	op := submit(t, app, OpRequest{ServerID: "bmconly", Kind: OpPowerOn, ScheduledFor: &at})
	if err := app.engine.RunNow(op.ID); err != nil {
		t.Fatal(err)
	}
	done := waitOp(t, app, op.ID)
	if done.Status != OpSucceeded || !strings.Contains(done.Note, "already on") {
		t.Fatalf("got %s note=%q err=%q", done.Status, done.Note, done.Error)
	}
	assertCalls(t, lab)
}

func TestCancelScheduled(t *testing.T) {
	app, lab := newTestApp(t)
	at := time.Now().Add(time.Hour)
	op := submit(t, app, OpRequest{ServerID: "bmconly", Kind: OpPowerOn, ScheduledFor: &at})

	if err := app.engine.Cancel(op.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := app.engine.Get(op.ID); got.Status != OpCancelled {
		t.Fatalf("status = %s", got.Status)
	}
	assertCalls(t, lab)
}

func TestCancelRunningSkipsRemainingSteps(t *testing.T) {
	app, lab := newTestApp(t)
	app.engine.timing.Unit = time.Second // make the wait steps long
	lab.set("both", PowerOn, PowerOn)
	app.poller.PollNow(context.Background(), "both")

	op, err := app.engine.Submit(context.Background(), OpRequest{ServerID: "both", Kind: OpPowerOff})
	if err != nil {
		t.Fatal(err)
	}
	// Wait until it's into the "wait before cutting outlets" step
	deadline := time.Now().Add(5 * time.Second)
	for {
		cur, _ := app.engine.Get(op.ID)
		if cur.Current >= 2 || time.Now().After(deadline) {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if err := app.engine.Cancel(op.ID); err != nil {
		t.Fatal(err)
	}
	done := waitOp(t, app, op.ID)
	if done.Status != OpCancelled {
		t.Fatalf("status = %s", done.Status)
	}
	assertCalls(t, lab, "bmc both off") // outlets never cut
	if st, _ := app.state.Get("both"); st.Operation != nil {
		t.Error("server still shows an operation in flight")
	}
}

func TestInterruptedAfterRestart(t *testing.T) {
	app, _ := newTestApp(t)
	now := time.Now()
	running := &Operation{ID: "op-x", ServerID: "bmconly", Kind: OpPowerOn, Status: OpRunning, CreatedAt: now, StartedAt: &now,
		Steps: []*Step{{Kind: StepBMC, Action: ActionOn, Status: StepRunning}}}
	if err := app.store.SaveOp(running); err != nil {
		t.Fatal(err)
	}

	fresh := NewEngine(app.config, app.hw, app.state, app.poller, app.store, app.hub, osManager)
	if err := fresh.Load(); err != nil {
		t.Fatal(err)
	}
	op, _ := fresh.Get("op-x")
	if op.Status != OpInterrupted || op.Steps[0].Status != StepFailed {
		t.Fatalf("got %s / step %s", op.Status, op.Steps[0].Status)
	}
}

func TestReinstallServesTemplateOnceThenReverts(t *testing.T) {
	app, lab := newTestApp(t)
	router := app.router()
	lab.set("bmconly", PowerOn, "")

	if _, err := osManager.AddTemplate(OSTemplate{ID: "installer", Name: "Installer", BootMethod: "chain", NextPath: "http://install/{{.Hostname}}"}); err != nil {
		t.Fatal(err)
	}
	if err := osManager.AssignOSToServer("bmconly", "local-disk", "test"); err != nil {
		t.Fatal(err)
	}

	// When the BMC resets the machine, it network boots and fetches its script
	var served string
	lab.onReset = func(id, action string) {
		if action != ActionReboot {
			return
		}
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/autoexec.ipxe?mac=AA-BB-CC-DD-EE-02", nil))
		served = rec.Body.String()
	}

	op := submit(t, app, OpRequest{ServerID: "bmconly", Kind: OpReinstall, Target: "installer", RequestedBy: "test"})
	if op.Status != OpSucceeded {
		t.Fatalf("reinstall %s: %s", op.Status, op.Error)
	}
	assertCalls(t, lab, "boot bmconly pxe", "bmc bmconly reboot")
	if !strings.Contains(served, "chain http://install/bmc-only") {
		t.Errorf("served script didn't chain to the installer:\n%s", served)
	}
	assignment, _ := osManager.GetServerOSAssignment("bmconly")
	if assignment.TemplateID != "local-disk" || assignment.OneShot {
		t.Errorf("after the one-shot, assignment should revert to local-disk, got %+v", assignment)
	}
}

func TestFailedReinstallLeavesNoOneShotBehind(t *testing.T) {
	app, lab := newTestApp(t)
	lab.set("bmconly", PowerOn, "")
	osManager.AddTemplate(OSTemplate{ID: "installer", Name: "Installer", BootMethod: "local"})

	op := submit(t, app, OpRequest{ServerID: "bmconly", Kind: OpReinstall, Target: "installer"})
	if op.Status != OpFailed { // nothing ever fetches the script
		t.Fatalf("status = %s", op.Status)
	}
	if _, ok := osManager.GetServerOSAssignment("bmconly"); ok {
		t.Error("one-shot assignment left behind after failed reinstall")
	}
}

func TestReinstallFromOffSetsBootBeforePowerOn(t *testing.T) {
	app, _ := newTestApp(t)
	config := app.config.Get()
	server := config.Server("both")
	st := ServerState{BMC: LinkUnreachable, PDU: LinkOK, Power: PowerUnknown,
		Outlets: []OutletStatus{{Ref: "pdu-x:1", State: PowerOff}, {Ref: "pdu-x:2", State: PowerOff}}}
	steps, err := planReinstall(config, server, st, "installer")
	if err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for _, s := range steps {
		kinds = append(kinds, string(s.Kind)+":"+s.Action+s.Target)
	}
	got := strings.Join(kinds, " ")
	want := "assign_os:installer outlet:onpdu-x:1 wait: outlet:onpdu-x:2 wait: wait_bmc: boot_override:pxe bmc:on wait_power: wait_boot_fetch:"
	if got != want {
		t.Errorf("plan:\n got  %s\n want %s", got, want)
	}
}

func TestDeriveState(t *testing.T) {
	on := []OutletStatus{{Ref: "a:1", State: PowerOn}}
	off := []OutletStatus{{Ref: "a:1", State: PowerOff}}
	mixed := []OutletStatus{{Ref: "a:1", State: PowerOn}, {Ref: "a:2", State: PowerOff}}
	cases := []struct {
		name string
		st   ServerState
		want string
	}{
		{"bmc on", ServerState{BMC: LinkOK, Power: PowerOn, PDU: LinkNone}, StateOn},
		{"bmc off, outlets live", ServerState{BMC: LinkOK, Power: PowerOff, PDU: LinkOK, Outlets: on}, StateOff},
		{"bmc dark, outlets off", ServerState{BMC: LinkUnreachable, PDU: LinkOK, Outlets: off}, StateOff},
		{"bmc dark, outlets on", ServerState{BMC: LinkUnreachable, PDU: LinkOK, Outlets: on}, StatePowered},
		{"bmc dark, no pdu", ServerState{BMC: LinkUnreachable, PDU: LinkNone}, StateUnreachable},
		{"both dark", ServerState{BMC: LinkUnreachable, PDU: LinkUnreachable, Outlets: on}, StateUnreachable},
		{"pdu only on", ServerState{BMC: LinkNone, PDU: LinkOK, Outlets: on}, StateOn},
		{"pdu only mixed", ServerState{BMC: LinkNone, PDU: LinkOK, Outlets: mixed}, StatePartial},
		{"not polled", ServerState{BMC: LinkPending, PDU: LinkNone}, StateUnknown},
		{"op in flight", ServerState{BMC: LinkOK, Power: PowerOff, Operation: &OperationSummary{Kind: OpPowerOn}}, StatePoweringOn},
	}
	for _, tc := range cases {
		tc.st.derive()
		if tc.st.State != tc.want {
			t.Errorf("%s: got %s, want %s", tc.name, tc.st.State, tc.want)
		}
	}
}

func TestAPIsNeverExposeSecrets(t *testing.T) {
	app, _ := newTestApp(t)
	router := app.router()
	for _, path := range []string{"/api/v1/servers", "/api/v1/servers/both", "/api/v2/servers", "/api/v2/servers/both"} {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK {
			t.Errorf("%s: status %d", path, rec.Code)
		}
		body := rec.Body.String()
		if strings.Contains(body, "secret-pw") || strings.Contains(body, "secret-community") {
			t.Errorf("%s leaks credentials", path)
		}
	}
}

func TestV1PowerEndpointQueuesOperation(t *testing.T) {
	app, lab := newTestApp(t)
	router := app.router()
	app.poller.PollNow(context.Background(), "bmconly")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/servers/bmconly/power", strings.NewReader(`{"action":"on"}`))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var resp struct {
		Jobs []legacyJob `json:"jobs"`
	}
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if len(resp.Jobs) != 1 {
		t.Fatalf("jobs: %s", rec.Body)
	}
	waitOp(t, app, resp.Jobs[0].ID)
	assertCalls(t, lab, "bmc bmconly on")
}

func TestBootScriptMatchesMACCaseInsensitively(t *testing.T) {
	app, _ := newTestApp(t)
	router := app.router()
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/boot/aa:bb:cc:dd:ee:01", nil))
	if !strings.Contains(rec.Body.String(), "Booting both") {
		t.Errorf("config MAC is uppercase, request lowercase; got:\n%s", rec.Body)
	}
	if st, _ := app.state.Get("both"); st.LastBoot == nil {
		t.Error("boot fetch not recorded")
	}
}

func TestBootstrapScriptChainsWithAbsoluteURL(t *testing.T) {
	app, _ := newTestApp(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/static/boot.ipxe", nil)
	req.Host = "labmgr.lab:8098"
	app.router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "chain http://labmgr.lab:8098/api/v1/autoexec.ipxe?mac=${net0/mac}") {
		t.Errorf("bootstrap script:\n%s", rec.Body)
	}
}
