package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLenovoKVMPageAndToken(t *testing.T) {
	app, _ := newTestApp(t)
	router := app.router()

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/kvm/bmconly/", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "sdk/rpviewer.js") || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("viewer page: %d %q", rec.Code, rec.Header().Get("Cache-Control"))
	}
	if strings.Contains(rec.Body.String(), "0987654321") {
		t.Error("the page itself must not carry the token")
	}

	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v2/servers/bmconly/kvm/token", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"user":"1234567890"`) || rec.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("token: %d %s", rec.Code, rec.Body)
	}
	events, _ := app.store.Events("bmconly", 5)
	if len(events) == 0 || strings.Contains(events[0].Message, "0987654321") {
		t.Errorf("console open not recorded, or the token leaked into the log: %+v", events)
	}

	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/kvm/pduonly/", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("a server without a BMC has no console: %d", rec.Code)
	}
}

func TestViewerScriptRewrite(t *testing.T) {
	src := []byte(`a="wss://";b='wss://';c="https://"+host;d="https://example";`)
	if got := string(rewriteViewerScript(src, false)); got != string(src) {
		t.Errorf("HTTPS pages get the script unchanged, got %s", got)
	}
	got := string(rewriteViewerScript(src, true))
	want := `a="ws://";b='ws://';c="http://"+host;d="https://example";`
	if got != want {
		t.Errorf("plain HTTP:\n got  %s\n want %s", got, want)
	}
}
