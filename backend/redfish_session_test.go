package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// fakeRedfish is a minimal Redfish service: a service root, one system, and sessions
type fakeRedfish struct {
	*httptest.Server
	mu         sync.Mutex
	sessions   bool   // offers login sessions
	token      string // the one valid session token
	logins     int
	logouts    int
	requests   map[string]int
	collection int
}

func newFakeRedfish(t *testing.T, sessions bool) *fakeRedfish {
	f := &fakeRedfish{sessions: sessions, requests: make(map[string]int)}
	f.Server = httptest.NewTLSServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.Close)
	return f
}

func (f *fakeRedfish) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests[r.Method+" "+r.URL.Path]++
	reply := func(v any) { w.Header().Set("Content-Type", "application/json"); json.NewEncoder(w).Encode(v) }
	deny := func(code int) {
		w.WriteHeader(code)
		reply(map[string]any{"error": map[string]any{"code": "Base.1.0.GeneralError", "message": http.StatusText(code)}})
	}

	if r.URL.Path == "/redfish/v1" || r.URL.Path == "/redfish/v1/" {
		reply(map[string]any{
			"@odata.id": "/redfish/v1",
			"Systems":   map[string]string{"@odata.id": "/redfish/v1/Systems"},
			"Links":     map[string]any{"Sessions": map[string]string{"@odata.id": "/redfish/v1/Sessions"}},
		})
		return
	}
	if r.URL.Path == "/redfish/v1/Sessions" && r.Method == http.MethodPost {
		if !f.sessions {
			deny(http.StatusMethodNotAllowed)
			return
		}
		var body struct{ UserName, Password string }
		json.NewDecoder(r.Body).Decode(&body)
		if body.UserName != "admin" || body.Password != "secret" {
			deny(http.StatusUnauthorized)
			return
		}
		f.logins++
		f.token = "token-" + string(rune('0'+f.logins))
		w.Header().Set("X-Auth-Token", f.token)
		w.Header().Set("Location", "/redfish/v1/Sessions/1")
		w.WriteHeader(http.StatusCreated)
		reply(map[string]string{"@odata.id": "/redfish/v1/Sessions/1"})
		return
	}

	// Everything else needs a login
	user, pass, basic := r.BasicAuth()
	authorized := (f.token != "" && r.Header.Get("X-Auth-Token") == f.token) ||
		(!f.sessions && basic && user == "admin" && pass == "secret")
	if !authorized {
		deny(http.StatusUnauthorized)
		return
	}
	switch {
	case r.URL.Path == "/redfish/v1/Sessions/1" && r.Method == http.MethodDelete:
		f.logouts++
		f.token = ""
		w.WriteHeader(http.StatusNoContent)
	case r.URL.Path == "/redfish/v1/Systems":
		f.collection++
		reply(map[string]any{"Members": []map[string]string{{"@odata.id": "/redfish/v1/Systems/1"}}})
	case r.URL.Path == "/redfish/v1/Systems/1":
		reply(map[string]any{"@odata.id": "/redfish/v1/Systems/1", "Id": "1", "PowerState": "On"})
	default:
		deny(http.StatusNotFound)
	}
}

func (f *fakeRedfish) bmc() *redfishBMC {
	return &redfishBMC{config: IPMIConfig{Host: strings.TrimPrefix(f.URL, "https://"), Username: "admin", Password: "secret"}}
}

func (f *fakeRedfish) count(key string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.requests[key]
}

func powerOn(t *testing.T, b *redfishBMC) {
	t.Helper()
	state, err := b.PowerState(context.Background())
	if err != nil || state != PowerOn {
		t.Fatalf("PowerState = %v, %v; want on", state, err)
	}
}

func TestRedfishReusesOneSession(t *testing.T) {
	f := newFakeRedfish(t, true)
	for range 3 {
		powerOn(t, f.bmc()) // a new redfishBMC each time, as the hardware layer hands them out
	}
	if f.logins != 1 {
		t.Errorf("logged in %d times, want 1", f.logins)
	}
	if f.collection != 1 {
		t.Errorf("listed systems %d times, want 1 (the system's address should be remembered)", f.collection)
	}
	if got := f.count("GET /redfish/v1/Systems/1"); got != 3 {
		t.Errorf("read the system %d times, want 3", got)
	}

	closeBMCSessions()
	if f.logouts != 1 {
		t.Errorf("logged out %d times on shutdown, want 1", f.logouts)
	}
}

func TestRedfishLogsInAgainWhenSessionDropped(t *testing.T) {
	f := newFakeRedfish(t, true)
	powerOn(t, f.bmc())
	f.mu.Lock()
	f.token = "" // the BMC restarted, or the session timed out
	f.mu.Unlock()
	powerOn(t, f.bmc())
	if f.logins != 2 {
		t.Errorf("logged in %d times, want 2", f.logins)
	}
}

func TestRedfishFallsBackToBasicAuth(t *testing.T) {
	f := newFakeRedfish(t, false)
	powerOn(t, f.bmc())
	powerOn(t, f.bmc())
	if f.logins != 0 || f.count("POST /redfish/v1/Sessions") != 1 {
		t.Errorf("tried sessions %d times, want once", f.count("POST /redfish/v1/Sessions"))
	}
}

func TestRedfishWrongPassword(t *testing.T) {
	f := newFakeRedfish(t, true)
	b := f.bmc()
	b.config.Password = "wrong"
	_, err := b.PowerState(context.Background())
	if err == nil || !strings.Contains(err.Error(), "login rejected") {
		t.Fatalf("err = %v, want a login rejected error", err)
	}
}
