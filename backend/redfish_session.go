package main

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/stmcginnis/gofish"
	"github.com/stmcginnis/gofish/schemas"
)

// Lab Manager keeps one Redfish login session per BMC and reuses it for every request,
// instead of sending the password with each one. Some BMCs check a password slowly on
// every request (an iDRAC8 takes 4-5 seconds; with a session token, under one), and one
// session for everything uses a single session slot however often Lab Manager polls.
// A session unused for a few minutes is logged out; one the BMC has dropped (it timed
// out, or the BMC restarted) is replaced on the next request. A BMC that refuses
// sessions is used with HTTP basic auth instead.
//
// Each connection also remembers where the BMC keeps its system and manager resources,
// so a status check is one request rather than a walk from the service root.

const (
	bmcSessionIdle  = 5 * time.Minute
	bmcConcurrency  = 4 // requests in flight to one BMC
	bmcLogoutWithin = 10 * time.Second
)

type bmcConn struct {
	mu         sync.Mutex
	client     *gofish.APIClient // bound to context.Background; callers get WithContext copies
	session    bool              // logged in with a session rather than basic auth
	systemURI  string
	managerURI string
	lastUsed   time.Time
}

var bmcConns = struct {
	sync.Mutex
	byKey   map[string]*bmcConn
	janitor sync.Once
}{byKey: make(map[string]*bmcConn)}

func (b *redfishBMC) endpoint() string {
	endpoint := b.config.Host
	if !strings.Contains(endpoint, "://") {
		endpoint = "https://" + endpoint
	}
	return strings.TrimSuffix(endpoint, "/")
}

// conn returns the shared connection state for this BMC and these credentials
func (b *redfishBMC) conn() *bmcConn {
	key := b.endpoint() + "\x00" + b.config.Username + "\x00" + b.config.Password
	bmcConns.Lock()
	defer bmcConns.Unlock()
	bmcConns.janitor.Do(func() { go logoutIdleSessions() })
	cn, ok := bmcConns.byKey[key]
	if !ok {
		cn = &bmcConn{}
		bmcConns.byKey[key] = cn
	}
	return cn
}

// client returns a client for this call, logging in first if there's no live session
func (b *redfishBMC) client(ctx context.Context, cn *bmcConn) (*gofish.APIClient, error) {
	cn.mu.Lock()
	defer cn.mu.Unlock()
	cn.lastUsed = time.Now()
	if cn.client == nil {
		config := gofish.ClientConfig{
			Endpoint:              b.endpoint(),
			Username:              b.config.Username,
			Password:              b.config.Password,
			Insecure:              true,
			MaxConcurrentRequests: bmcConcurrency,
		}
		c, err := gofish.ConnectContext(ctx, config)
		session := true
		var answered *schemas.Error
		if err != nil && errors.As(err, &answered) && answered.HTTPReturnedStatusCode != http.StatusUnauthorized {
			// The BMC answered but wouldn't create a session: fall back to basic auth
			config.BasicAuth = true
			c, err = gofish.ConnectContext(ctx, config)
			session = false
		}
		if err != nil {
			return nil, friendlyError(err)
		}
		cn.client, cn.session = c.WithContext(context.Background()), session
	}
	return cn.client.WithContext(ctx), nil
}

// do runs fn with this BMC's shared client. If the BMC rejects the session (it expired,
// or the BMC restarted), it logs in again and runs fn once more: a 401 means the BMC
// didn't act on the request, so repeating it is safe.
func (b *redfishBMC) do(ctx context.Context, fn func(c *gofish.APIClient, cn *bmcConn) error) error {
	cn := b.conn()
	for attempt := 0; ; attempt++ {
		c, err := b.client(ctx, cn)
		if err != nil {
			return err
		}
		err = fn(c, cn)
		if attempt == 0 && isUnauthorized(err) && cn.drop() {
			continue
		}
		if err != nil {
			return friendlyError(err)
		}
		return nil
	}
}

// drop forgets a session the BMC rejected. It reports whether there's a fresh login
// to try, which there isn't for basic auth (the password itself was refused).
func (cn *bmcConn) drop() bool {
	cn.mu.Lock()
	defer cn.mu.Unlock()
	if cn.client == nil || !cn.session {
		return false
	}
	cn.client = nil
	return true
}

func isUnauthorized(err error) bool {
	var e *schemas.Error
	return errors.As(err, &e) && e.HTTPReturnedStatusCode == http.StatusUnauthorized
}

// answeredWithError reports whether the BMC responded with an HTTP error (rather than not responding)
func answeredWithError(err error) bool {
	var e *schemas.Error
	return errors.As(err, &e)
}

func isNotFound(err error) bool {
	var e *schemas.Error
	return errors.As(err, &e) && e.HTTPReturnedStatusCode == http.StatusNotFound
}

// system returns the BMC's (first) computer system, from its remembered address when it has one
func (cn *bmcConn) system(c *gofish.APIClient) (*schemas.ComputerSystem, error) {
	cn.mu.Lock()
	uri := cn.systemURI
	cn.mu.Unlock()
	if uri != "" {
		system, err := schemas.GetComputerSystem(c, uri)
		if !isNotFound(err) {
			return system, err
		}
	}
	systems, err := c.Service.Systems()
	if err != nil {
		return nil, err
	}
	if len(systems) == 0 {
		return nil, errors.New("BMC reports no systems")
	}
	cn.mu.Lock()
	cn.systemURI = systems[0].ODataID
	cn.mu.Unlock()
	return systems[0], nil
}

// manager returns the BMC's own manager resource, from its remembered address when it has one
func (cn *bmcConn) manager(c *gofish.APIClient) (*schemas.Manager, error) {
	cn.mu.Lock()
	uri := cn.managerURI
	cn.mu.Unlock()
	if uri != "" {
		manager, err := schemas.GetManager(c, uri)
		if !isNotFound(err) {
			return manager, err
		}
	}
	managers, err := c.Service.Managers()
	if err != nil {
		return nil, err
	}
	if len(managers) == 0 {
		return nil, errors.New("BMC reports no managers")
	}
	cn.mu.Lock()
	cn.managerURI = managers[0].ODataID
	cn.mu.Unlock()
	return managers[0], nil
}

func logoutIdleSessions() {
	for range time.Tick(time.Minute) {
		logoutSessions(func(cn *bmcConn) bool { return time.Since(cn.lastUsed) > bmcSessionIdle })
	}
}

// closeBMCSessions logs out of every BMC, for shutdown
func closeBMCSessions() {
	logoutSessions(func(*bmcConn) bool { return true })
}

func logoutSessions(match func(*bmcConn) bool) {
	bmcConns.Lock()
	conns := make([]*bmcConn, 0, len(bmcConns.byKey))
	for _, cn := range bmcConns.byKey {
		conns = append(conns, cn)
	}
	bmcConns.Unlock()

	var wg sync.WaitGroup
	for _, cn := range conns {
		cn.mu.Lock()
		c, session := cn.client, cn.session
		if c == nil || !match(cn) {
			cn.mu.Unlock()
			continue
		}
		cn.client = nil
		cn.mu.Unlock()
		if !session {
			continue // basic auth: nothing to log out of
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), bmcLogoutWithin)
			defer cancel()
			c.WithContext(ctx).Logout()
		}()
	}
	wg.Wait()
}
