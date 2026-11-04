package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// recordingApp is a test app whose console hub records to a temp dir with fast timings
func recordingApp(t *testing.T) (*App, *fakeDialer) {
	t.Helper()
	app, _ := newTestApp(t)
	dialer := &fakeDialer{}
	app.consoles.dial = dialer.dial
	app.consoles.dir = t.TempDir()
	app.consoles.tick = 5 * time.Millisecond
	app.consoles.retry = 10 * time.Millisecond
	app.consoles.idle = 10 * time.Millisecond
	app.consoles.state = app.state
	app.engine.onStart = app.consoles.captureForOperation
	app.poller.PollNow(context.Background(), "bmconly") // BMC known to answer
	return app, dialer
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func dialed(d *fakeDialer) func() bool {
	return func() bool {
		d.mu.Lock()
		defer d.mu.Unlock()
		return d.dials > 0
	}
}

func closed(c *fakeConsole) func() bool {
	return func() bool {
		c.mu.Lock()
		defer c.mu.Unlock()
		return c.closed
	}
}

func readCapture(t *testing.T, app *App, serverID, name string) string {
	t.Helper()
	path, ok := app.consoles.CapturePath(serverID, name)
	if !ok {
		t.Fatalf("recording %s not found", name)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestRecordingWithoutViewers(t *testing.T) {
	app, dialer := recordingApp(t)
	name, err := app.consoles.Capture("bmconly", "op-test-1", 300*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	eventually(t, "the console session", dialed(dialer))
	bmc := dialer.last()
	bmc.toViewer.Write([]byte("Booting Linux...\r\n"))

	eventually(t, "the recording to finish and the session to close", closed(bmc))
	if got := readCapture(t, app, "bmconly", name); !strings.Contains(got, "Booting Linux") {
		t.Errorf("recording = %q", got)
	}
	list := app.consoles.Captures("bmconly")
	if len(list) != 1 || list[0].OpID != "op-test-1" || list[0].Active || list[0].Bytes == 0 {
		t.Errorf("captures = %+v", list)
	}
}

func TestViewerLeavingDoesntEndRecording(t *testing.T) {
	app, dialer := recordingApp(t)
	s, v, _, err := app.consoles.Attach(context.Background(), "bmconly", 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.consoles.Capture("bmconly", "op-test-2", 400*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	s.Detach(v)
	time.Sleep(100 * time.Millisecond) // well past the 10ms idle close
	if closed(dialer.last())() {
		t.Fatal("session closed while a recording was running")
	}
	eventually(t, "the session to close after the recording", closed(dialer.last()))
}

func TestEmptyRecordingIsRemoved(t *testing.T) {
	app, dialer := recordingApp(t)
	name, _ := app.consoles.Capture("bmconly", "op-test-3", 50*time.Millisecond)
	eventually(t, "the session", dialed(dialer))
	eventually(t, "the recording to finish", closed(dialer.last()))
	if _, ok := app.consoles.CapturePath("bmconly", name); ok {
		t.Error("an empty recording was kept")
	}
}

func TestOperationsStartRecordings(t *testing.T) {
	app, dialer := recordingApp(t)
	lab := app.hw.(*fakeLab)
	lab.set("bmconly", PowerOn, "")

	// restart is in the default list: it records
	op := submit(t, app, OpRequest{ServerID: "bmconly", Kind: OpRestart})
	if op.ConsoleCapture == "" {
		t.Fatal("restart didn't start a recording")
	}
	eventually(t, "the console session", dialed(dialer))
	dialer.last().toViewer.Write([]byte("Ubuntu 24.04 node login:\r\n"))
	eventually(t, "output in the recording", func() bool {
		path, ok := app.consoles.CapturePath("bmconly", op.ConsoleCapture)
		data, _ := os.ReadFile(path)
		return ok && strings.Contains(string(data), "login:")
	})

	// a hard power off isn't: no recording
	app.poller.PollNow(context.Background(), "bmconly")
	op = submit(t, app, OpRequest{ServerID: "bmconly", Kind: OpPowerOff})
	if op.ConsoleCapture != "" {
		t.Errorf("power off started a recording: %s", op.ConsoleCapture)
	}

	// The recording is served over the API
	rec := httptest.NewRecorder()
	app.router().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v2/servers/bmconly/console/captures", nil))
	if !strings.Contains(rec.Body.String(), `"op_id":"op-`) {
		t.Errorf("captures API: %s", rec.Body)
	}
}

func TestContinuousRecordingFollowsConfig(t *testing.T) {
	app, dialer := recordingApp(t)
	rewrite := func(extra string) {
		if err := os.WriteFile(configPath, []byte(testConfigYAML+extra), 0600); err != nil {
			t.Fatal(err)
		}
		if err := app.config.Reload(); err != nil {
			t.Fatal(err)
		}
	}

	rewrite("console_capture:\n  always: true\n")
	app.consoles.syncContinuous()
	eventually(t, "continuous sessions", dialed(dialer))
	list := app.consoles.Captures("bmconly")
	if len(list) != 1 || !list[0].Active || !strings.HasSuffix(list[0].Name, continuousSuffix) {
		t.Fatalf("captures = %+v", list)
	}

	rewrite("") // turned off again
	app.consoles.syncContinuous()
	if app.consoles.recording("bmconly") {
		t.Error("continuous recording kept running after it was turned off")
	}
}

func TestRecordingNamesCantEscape(t *testing.T) {
	app, _ := recordingApp(t)
	for _, name := range []string{"../servers.yaml", "20261104-101500-x/../../a.log", "x.log", ""} {
		if _, ok := app.consoles.CapturePath("bmconly", name); ok {
			t.Errorf("accepted %q", name)
		}
	}
}

func TestConsoleCaptureConfigValidation(t *testing.T) {
	// The README writes it unquoted; YAML 1.1 would read that as false
	off := strings.Replace(testConfigYAML, `metadata: {mac: "aa:bb:cc:dd:ee:02", hostname: "bmc-only"}`,
		`metadata: {mac: "aa:bb:cc:dd:ee:02", hostname: "bmc-only"}`+"\n    console:\n      capture: off", 1)
	if cfg, err := parseConfig([]byte(off)); err != nil || cfg.Server("bmconly").Console.Capture != "off" {
		t.Errorf("unquoted capture: off: %v %+v", err, cfg)
	}

	if _, err := parseConfig([]byte(testConfigYAML + "console_capture:\n  after: [power_on, reboot_everything]\n")); err == nil {
		t.Error("unknown operation in console_capture.after accepted")
	}
	bad := strings.Replace(testConfigYAML, `metadata: {mac: "aa:bb:cc:dd:ee:02", hostname: "bmc-only"}`,
		`metadata: {mac: "aa:bb:cc:dd:ee:02", hostname: "bmc-only"}`+"\n    console: {capture: sometimes}", 1)
	if _, err := parseConfig([]byte(bad)); err == nil {
		t.Error("console.capture: sometimes accepted")
	}
}
