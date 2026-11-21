package main

import (
	"bytes"
	"context"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/gin-gonic/gin"
)

// Lenovo XCC graphical console, hosted by Lab Manager.
//
// The XCC's web console only accepts a logged-in web session, but its viewer library
// (RPViewer, under /SDK_Pilot4/) accepts a one-time login from the Redfish action
// GetRemoteConsoleToken. So Lab Manager serves a small page that runs that viewer with a
// fresh token, relays the viewer's scripts (so its workers load same-origin), and relays
// its WebSocket to the XCC's remote presence port. The browser only ever talks to
// Lab Manager and only ever holds the one-time token; the BMC password never leaves.

const (
	xccRPPort     = 3900 // XCC remote presence port (default)
	xccSDKPath    = "/SDK_Pilot4/"
	xccRPProtocol = "lws-dvc-protocol"
)

var sdkFileName = regexp.MustCompile(`^[A-Za-z0-9_.-]+\.js$`)

// xccSDKCache keeps the viewer scripts per BMC host; they only change with firmware
var xccSDKCache = struct {
	sync.Mutex
	files map[string]cachedFile
}{files: make(map[string]cachedFile)}

type cachedFile struct {
	data    []byte
	fetched time.Time
}

func (a *App) xccServer(c *gin.Context) (*Server, *redfishBMC, bool) {
	server := a.config.Get().Server(c.Param("id"))
	if server == nil || !server.HasBMC() {
		c.String(http.StatusNotFound, "no such server")
		return nil, nil, false
	}
	bmc, ok := a.hw.BMC(server).(*redfishBMC)
	if !ok {
		c.String(http.StatusNotImplemented, "console not available")
		return nil, nil, false
	}
	return server, bmc, true
}

func xccPort(server *Server) int {
	if server.Console.KVMPort > 0 {
		return server.Console.KVMPort
	}
	return xccRPPort
}

// kvmPage serves the viewer page; the token is fetched by the page, never put in a URL
func (a *App) kvmPage(c *gin.Context) {
	server := a.config.Get().Server(c.Param("id"))
	if server == nil || !server.HasBMC() {
		c.String(http.StatusNotFound, "no such server")
		return
	}
	c.Header("Cache-Control", "no-store")
	c.Header("Content-Type", "text/html; charset=utf-8")
	kvmPageTemplate.Execute(c.Writer, map[string]string{"ID": server.ID, "Name": server.Name})
}

// kvmSDK relays one of the XCC viewer's scripts
func (a *App) kvmSDK(c *gin.Context) {
	_, bmc, ok := a.xccServer(c)
	if !ok {
		return
	}
	name := c.Param("file")
	if !sdkFileName.MatchString(name) {
		c.String(http.StatusNotFound, "not found")
		return
	}
	key := bmc.config.Host + "|" + name
	xccSDKCache.Lock()
	cached, hit := xccSDKCache.files[key]
	xccSDKCache.Unlock()
	if !hit || time.Since(cached.fetched) > 24*time.Hour {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
		defer cancel()
		data, _, err := bmc.fetchStatic(ctx, xccSDKPath+name)
		if err != nil {
			c.String(http.StatusBadGateway, err.Error())
			return
		}
		cached = cachedFile{data: data, fetched: time.Now()}
		xccSDKCache.Lock()
		xccSDKCache.files[key] = cached
		xccSDKCache.Unlock()
	}
	plainHTTP := c.Request.TLS == nil && c.GetHeader("X-Forwarded-Proto") != "https"
	c.Header("Cache-Control", "private, no-cache")
	c.Data(http.StatusOK, "application/javascript", rewriteViewerScript(cached.data, plainHTTP))
}

// rewriteViewerScript adapts the XCC viewer to run behind Lab Manager on a plain-HTTP page.
// The XCC makes the viewer reconnect "securely", which it does with a hard-coded wss://, and
// its certificate domain is a hard-coded https://. Pointing both at this page's own scheme
// keeps every connection going through the relay; the relay's link to the XCC is TLS either
// way, so the BMC traffic stays encrypted.
func rewriteViewerScript(data []byte, plainHTTP bool) []byte {
	if !plainHTTP {
		return data
	}
	data = bytes.ReplaceAll(data, []byte(`"wss://"`), []byte(`"ws://"`))
	data = bytes.ReplaceAll(data, []byte(`'wss://'`), []byte(`'ws://'`))
	return bytes.ReplaceAll(data, []byte(`"https://"`), []byte(`"http://"`))
}

// v2KVMToken hands the viewer page a one-time XCC login. Never logged or cached.
func (a *App) v2KVMToken(c *gin.Context) {
	server := a.config.Get().Server(c.Param("id"))
	if server == nil || !server.HasBMC() {
		c.JSON(http.StatusNotFound, gin.H{"error": "server not found"})
		return
	}
	bmc := a.hw.BMC(server)
	ctx, cancel := context.WithTimeout(c.Request.Context(), 20*time.Second)
	defer cancel()
	token, err := bmc.ConsoleToken(ctx)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	a.recordEvent(&Event{ServerID: server.ID, Kind: "console", Message: "Opened the graphical console", Actor: requester(c, ""), Level: "info"})
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, token)
}

// kvmRelay pipes the viewer's WebSocket to the XCC's remote presence port
func (a *App) kvmRelay(c *gin.Context) {
	server, bmc, ok := a.xccServer(c)
	if !ok {
		return
	}
	host := bmc.config.Host
	if i := strings.Index(host, "://"); i >= 0 {
		host = host[i+3:]
	}
	host = strings.Split(strings.TrimSuffix(host, "/"), ":")[0]
	target := fmt.Sprintf("wss://%s:%d/%s", host, xccPort(server), strings.TrimPrefix(c.Param("rest"), "/"))

	browser, err := websocket.Accept(c.Writer, c.Request, &websocket.AcceptOptions{Subprotocols: []string{xccRPProtocol}})
	if err != nil {
		return
	}
	defer browser.CloseNow()
	browser.SetReadLimit(64 << 20)

	ctx, cancel := context.WithCancel(c.Request.Context())
	defer cancel()
	dialCtx, dialCancel := context.WithTimeout(ctx, 20*time.Second)
	bmcConn, _, err := websocket.Dial(dialCtx, target, &websocket.DialOptions{
		HTTPClient:   insecureHTTP,
		Subprotocols: []string{xccRPProtocol},
		HTTPHeader:   http.Header{"Origin": {"https://" + host}},
	})
	dialCancel()
	if err != nil {
		log.Printf("KVM relay for %s: %v", server.ID, err)
		browser.Close(websocket.StatusTryAgainLater, "couldn't reach the BMC console")
		return
	}
	defer bmcConn.CloseNow()
	bmcConn.SetReadLimit(64 << 20)

	pipe := func(name string, from, to *websocket.Conn) {
		defer cancel()
		frames := 0
		for {
			kind, data, err := from.Read(ctx)
			if err != nil {
				if ctx.Err() == nil {
					log.Printf("KVM relay for %s: %s ended after %d frames: %v", server.ID, name, frames, err)
				}
				// Pass the close on so the other side sees why
				if status := websocket.CloseStatus(err); status != -1 {
					to.Close(status, "")
				}
				return
			}
			frames++
			if err := to.Write(ctx, kind, data); err != nil {
				log.Printf("KVM relay for %s: writing %s failed: %v", server.ID, name, err)
				return
			}
		}
	}
	log.Printf("KVM relay for %s: connected to %s", server.ID, target)
	go pipe("browser->BMC", browser, bmcConn)
	pipe("BMC->browser", bmcConn, browser)
}

var kvmPageTemplate = template.Must(template.New("kvm").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Name}} console</title>
<style>
  html, body { margin: 0; height: 100%; background: #0f0f0e; color: #ecebe7;
    font: 13px/1.4 'Inter', ui-sans-serif, system-ui, sans-serif; }
  header { display: flex; align-items: center; gap: 10px; height: 36px; padding: 0 14px;
    border-bottom: 1px solid rgba(255,255,255,0.08); }
  .dot { width: 8px; height: 8px; border-radius: 50%; background: #5b8cff; }
  .ok .dot { background: #3fbf75; } .bad .dot { background: #ef5b61; }
  #status { color: #a3a29d; }
  #screen { height: calc(100% - 37px); display: grid; place-items: center; overflow: hidden; }
</style>
</head>
<body>
<header id="bar"><span class="dot"></span><strong>{{.Name}}</strong><span id="status">Connecting…</span></header>
<div id="screen"><canvas id="kvmCanvas"></canvas></div>
<script src="sdk/rpviewer.js"></script>
<script>
(async () => {
  const id = {{.ID}};
  const bar = document.getElementById('bar');
  const status = (text, tone) => {
    document.getElementById('status').textContent = text;
    bar.className = tone || '';
  };
  try {
    const res = await fetch('/api/v2/servers/' + encodeURIComponent(id) + '/kvm/token', {
      method: 'POST', headers: { 'X-Labmgr-Client': 'web' } });
    const token = await res.json();
    if (!res.ok) throw new Error(token.error || res.statusText);

    // Before its secure reconnect the viewer opens a pop-up so the user can accept the
    // console host's certificate, and waits for that window to message back. Its console host
    // is this page's own origin (the relay), so there's nothing to accept: answer for it
    // instead of opening a window.
    const realOpen = window.open;
    window.open = function (target) {
      if (typeof target === 'string' && target.startsWith(location.origin + '/')) {
        setTimeout(() => window.postMessage('certificate accepted', location.origin), 0);
        return { closed: false, close() { this.closed = true; }, postMessage() {} };
      }
      return realOpen.apply(window, arguments);
    };

    const viewer = new RPViewer('kvmCanvas', (fn, code) => status('Viewer error: ' + fn + ' (' + code + ')', 'bad'));
    const port = Number(location.port) || (location.protocol === 'https:' ? 443 : 80);
    if (new URLSearchParams(location.search).has('debug')) {
      viewer.setRPDebugMode(true);
      viewer.setRPDebugLevel(2);
    }
    viewer.setRPWebSocketTimeout(30);
    viewer.setRPServerConfiguration(location.hostname, port);
    viewer.setRPServerPath('kvm/' + encodeURIComponent(id) + '/ws');
    viewer.setRPEmbeddedViewerSize(window.innerWidth, window.innerHeight - 37);
    viewer.setRPExclusiveLogin(false);
    viewer.setRPAllowSharingRequests(true);
    viewer.setRPMouseInputSupport(true);
    viewer.setRPTouchInputSupport(true);
    viewer.setRPKeyboardInputSupport(true);
    viewer.setRPMaintainAspectRatio(true);
    viewer.setRPInitialBackgroundColor('black');
    viewer.setRPInitialMessageColor('white');
    viewer.setRPInitialMessage('Connecting to the console…');
    // The token's user/password pair, not setRPTokenCredential: that mode leaves the
    // password empty and this firmware's login packet can't handle it
    viewer.setRPCredential(token.user, token.password);
    viewer.registerRPLoginResponseCallback((result) => {
      if (result === 0) status('Connected', 'ok');
      else status('The BMC refused the console login (code ' + result + ')', 'bad');
    });
    viewer.registerRPSessionTerminationCallback((reason) => status('Session ended (' + reason + ')', 'bad'));
    window.rpViewer = viewer;
    viewer.connectRPViewer();
  } catch (e) {
    status("Couldn't open the console: " + e.message, 'bad');
  }
})();
</script>
</body>
</html>
`))
