package main

import (
	"context"
	"log"
	"sync"
	"time"
)

const (
	powerSampleEvery = 10 * time.Second // power draw reading interval
	hardwareEvery    = time.Hour        // model/health refresh
	pollReachable    = 30 * time.Second // BMC/PDU answering
	pollUnreachable  = 90 * time.Second // BMC/PDU not answering
	pollFast         = 5 * time.Second  // while an operation is running on the server
	bmcPollTimeout   = 10 * time.Second
	pduPollTimeout   = 10 * time.Second
)

// Poller keeps the state store fresh. Each BMC and each PDU is polled on its own
// schedule, never more than once at a time; a PDU is read with one query for all its outlets.
type Poller struct {
	config *ConfigStore
	hw     Hardware
	state  *StateStore
	store  *Store // power history; may be nil

	mu       sync.Mutex
	inflight map[string]bool
	next     map[string]time.Time // "bmc:<server>" / "pdu:<pdu>" -> next poll
	fast     map[string]time.Time // server -> fast polling until
	sampled  map[string]time.Time // server -> last power reading
	hwRead   map[string]time.Time // server -> last hardware read
}

func NewPoller(config *ConfigStore, hw Hardware, state *StateStore) *Poller {
	return &Poller{
		config:   config,
		hw:       hw,
		state:    state,
		inflight: make(map[string]bool),
		next:     make(map[string]time.Time),
		fast:     make(map[string]time.Time),
		sampled:  make(map[string]time.Time),
		hwRead:   make(map[string]time.Time),
	}
}

func (p *Poller) Run(ctx context.Context) {
	log.Println("Starting status poller")
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	lastPrune := time.Now()
	for {
		p.tick(ctx)
		if p.store != nil && time.Since(lastPrune) > time.Hour {
			p.store.PrunePower()
			lastPrune = time.Now()
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (p *Poller) tick(ctx context.Context) {
	config := p.config.Get()
	now := time.Now()

	pdusInUse := make(map[string]bool)
	for i := range config.Servers {
		server := &config.Servers[i]
		if server.HasBMC() {
			id := server.ID
			p.dispatch(ctx, "bmc:"+id, now, func(ctx context.Context) bool { return p.pollBMC(ctx, config, id) })
			p.samplePowerIfDue(ctx, server)
			p.readHardwareIfDue(ctx, server)
		}
		for _, ref := range server.Outlets() {
			pdusInUse[ref.PDUID] = true
		}
	}
	for pduID := range pdusInUse {
		id := pduID
		p.dispatch(ctx, "pdu:"+id, now, func(ctx context.Context) bool { return p.pollPDU(ctx, config, id) })
	}
}

// dispatch starts a poll if it's due and not already running; poll returns whether the device answered
func (p *Poller) dispatch(ctx context.Context, key string, now time.Time, poll func(context.Context) bool) {
	p.mu.Lock()
	if p.inflight[key] || now.Before(p.next[key]) {
		p.mu.Unlock()
		return
	}
	p.inflight[key] = true
	p.mu.Unlock()

	go func() {
		ok := poll(ctx)
		interval := pollUnreachable
		if ok {
			interval = pollReachable
		}
		if p.isFast(key) {
			interval = pollFast
		}
		p.mu.Lock()
		p.inflight[key] = false
		p.next[key] = time.Now().Add(interval)
		p.mu.Unlock()
	}()
}

// isFast reports whether a poll key belongs to a server (or a PDU feeding a server) in fast mode
func (p *Poller) isFast(key string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	config := p.config.Get()
	for serverID, until := range p.fast {
		if now.After(until) {
			delete(p.fast, serverID)
			continue
		}
		if key == "bmc:"+serverID {
			return true
		}
		if server := config.Server(serverID); server != nil {
			for _, ref := range server.Outlets() {
				if key == "pdu:"+ref.PDUID {
					return true
				}
			}
		}
	}
	return false
}

// Boost polls a server quickly for a while, e.g. during and just after an operation
func (p *Poller) Boost(serverID string, d time.Duration) {
	p.mu.Lock()
	defer p.mu.Unlock()
	until := time.Now().Add(d)
	if until.After(p.fast[serverID]) {
		p.fast[serverID] = until
	}
	// Pull the next polls in
	now := time.Now()
	if t, ok := p.next["bmc:"+serverID]; ok && t.After(now.Add(pollFast)) {
		p.next["bmc:"+serverID] = now
	}
	if server := p.config.Get().Server(serverID); server != nil {
		for _, ref := range server.Outlets() {
			if t, ok := p.next["pdu:"+ref.PDUID]; ok && t.After(now.Add(pollFast)) {
				p.next["pdu:"+ref.PDUID] = now
			}
		}
	}
}

func (p *Poller) pollBMC(ctx context.Context, config *Config, serverID string) bool {
	server := config.Server(serverID)
	if server == nil {
		return false
	}
	bmc := p.hw.BMC(server)
	if bmc == nil {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, bmcPollTimeout)
	defer cancel()
	power, err := bmc.PowerState(ctx)
	if debugMode && err != nil {
		log.Printf("DEBUG: BMC poll %s: %v", serverID, err)
	}
	p.state.RecordBMC(serverID, power, err)
	return err == nil
}

// samplePowerIfDue starts a power reading for a server whose BMC is responding,
// on its own schedule (independent of status polls), never two at once per server
func (p *Poller) samplePowerIfDue(ctx context.Context, server *Server) {
	if st, ok := p.state.Get(server.ID); !ok || st.BMC != LinkOK {
		return
	}
	key := "watts:" + server.ID
	p.mu.Lock()
	if p.inflight[key] || time.Since(p.sampled[server.ID]) < powerSampleEvery {
		p.mu.Unlock()
		return
	}
	p.inflight[key] = true
	p.sampled[server.ID] = time.Now()
	p.mu.Unlock()

	go func() {
		defer func() {
			p.mu.Lock()
			p.inflight[key] = false
			p.mu.Unlock()
		}()
		if bmc := p.hw.BMC(server); bmc != nil {
			p.samplePower(ctx, server.ID, bmc)
		}
	}()
}

// samplePower reads power draw once and records it
func (p *Poller) samplePower(ctx context.Context, serverID string, bmc BMC) {
	ctx, cancel := context.WithTimeout(ctx, bmcPollTimeout)
	defer cancel()
	watts, err := bmc.PowerWatts(ctx)
	p.state.RecordWatts(serverID, watts, err)
	if err == nil && p.store != nil {
		if err := p.store.AppendPower(serverID, time.Now(), watts); err != nil {
			log.Printf("Warning: failed to record power for %s: %v", serverID, err)
		}
	}
}

func (p *Poller) pollPDU(ctx context.Context, config *Config, pduID string) bool {
	pdu := config.PDU(pduID)
	if pdu == nil {
		return false
	}

	// Every outlet on this PDU that belongs to a server
	owners := make(map[int][]string) // outlet -> server IDs
	var outlets []int
	for i := range config.Servers {
		for _, ref := range config.Servers[i].Outlets() {
			if ref.PDUID != pduID {
				continue
			}
			if _, seen := owners[ref.Outlet]; !seen {
				outlets = append(outlets, ref.Outlet)
			}
			owners[ref.Outlet] = append(owners[ref.Outlet], config.Servers[i].ID)
		}
	}

	ctx, cancel := context.WithTimeout(ctx, pduPollTimeout)
	defer cancel()
	states, err := p.hw.PDU(pdu).OutletStates(ctx, outlets)
	if debugMode && err != nil {
		log.Printf("DEBUG: PDU poll %s: %v", pduID, err)
	}

	byServer := make(map[string]map[string]PowerState)
	for outlet, serverIDs := range owners {
		for _, id := range serverIDs {
			if byServer[id] == nil {
				byServer[id] = make(map[string]PowerState)
			}
			if state, ok := states[outlet]; ok {
				byServer[id][OutletRef{PDUID: pduID, Outlet: outlet}.String()] = state
			}
		}
	}
	for id, outletStates := range byServer {
		p.state.RecordOutlets(id, outletStates, err)
	}
	return err == nil
}

// PollNow polls one server's BMC and outlets synchronously and returns the fresh state
func (p *Poller) PollNow(ctx context.Context, serverID string) (ServerState, bool) {
	config := p.config.Get()
	server := config.Server(serverID)
	if server == nil {
		return ServerState{}, false
	}

	var wg sync.WaitGroup
	if server.HasBMC() {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p.pollBMC(ctx, config, serverID)
		}()
	}
	seen := make(map[string]bool)
	for _, ref := range server.Outlets() {
		if seen[ref.PDUID] {
			continue
		}
		seen[ref.PDUID] = true
		wg.Add(1)
		go func(pduID string) {
			defer wg.Done()
			p.pollPDU(ctx, config, pduID)
		}(ref.PDUID)
	}
	wg.Wait()
	return p.state.Get(serverID)
}

// PollAllNow polls every server at once, e.g. for a manual refresh
func (p *Poller) PollAllNow(ctx context.Context) {
	config := p.config.Get()
	var wg sync.WaitGroup
	for i := range config.Servers {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			p.PollNow(ctx, id)
		}(config.Servers[i].ID)
	}
	wg.Wait()
}

// readHardwareIfDue refreshes a responding BMC's model and health once an hour
func (p *Poller) readHardwareIfDue(ctx context.Context, server *Server) {
	if st, ok := p.state.Get(server.ID); !ok || st.BMC != LinkOK {
		return
	}
	key := "hw:" + server.ID
	p.mu.Lock()
	if p.inflight[key] || time.Since(p.hwRead[server.ID]) < hardwareEvery {
		p.mu.Unlock()
		return
	}
	p.inflight[key] = true
	p.hwRead[server.ID] = time.Now()
	p.mu.Unlock()

	go func() {
		defer func() {
			p.mu.Lock()
			p.inflight[key] = false
			p.mu.Unlock()
		}()
		bmc := p.hw.BMC(server)
		if bmc == nil {
			return
		}
		ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		if info, err := bmc.Info(ctx); err == nil {
			p.state.RecordHardware(server.ID, info)
		} else {
			// Try again sooner than an hour if the read failed
			p.mu.Lock()
			p.hwRead[server.ID] = time.Now().Add(-hardwareEvery + 5*time.Minute)
			p.mu.Unlock()
		}
	}()
}
