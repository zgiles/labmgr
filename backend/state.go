package main

import (
	"reflect"
	"sort"
	"sync"
	"time"
)

// Display states, derived from what we observe plus any operation in flight
const (
	StateUnknown      = "unknown"      // not polled yet
	StateOn           = "on"           // system running
	StateOff          = "off"          // system off (outlets may still be live)
	StatePowered      = "powered"      // outlets on, but the BMC isn't answering
	StatePartial      = "partial"      // some outlets on, some off (no BMC)
	StateUnreachable  = "unreachable"  // can't see the BMC or the PDU
	StatePoweringOn   = "powering_on"  // operation in flight...
	StatePoweringOff  = "powering_off" //
	StateShuttingDown = "shutting_down"
	StateRestarting   = "restarting"
	StatePowerCycling = "power_cycling"
	StateBusy         = "busy"
)

// Link states for the BMC and PDU connections
const (
	LinkOK          = "ok"
	LinkUnreachable = "unreachable"
	LinkNone        = "none" // not configured
	LinkPending     = "pending"
)

type OutletStatus struct {
	Ref   string     `json:"ref"`
	State PowerState `json:"state"`
}

// OperationSummary is the slice of an in-flight operation shown with server state
type OperationSummary struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	Label     string `json:"label"`
	Step      int    `json:"step"`
	Steps     int    `json:"steps"`
	StepLabel string `json:"step_label"`
}

// BootFetch records the last time a server pulled its iPXE script
type BootFetch struct {
	At       time.Time `json:"at"`
	ClientIP string    `json:"client_ip"`
	Source   string    `json:"source"` // what was served: template ID, "next-path", "boot-template", "local"
}

type ServerState struct {
	ServerID  string            `json:"server_id"`
	State     string            `json:"state"`
	Power     PowerState        `json:"power"` // system power as the BMC reports it
	BMC       string            `json:"bmc"`
	BMCError  string            `json:"bmc_error,omitempty"`
	PDU       string            `json:"pdu"`
	PDUError  string            `json:"pdu_error,omitempty"`
	Outlets   []OutletStatus    `json:"outlets,omitempty"`
	Operation *OperationSummary `json:"operation,omitempty"`
	LastBoot  *BootFetch        `json:"last_boot,omitempty"`

	BMCCheckedAt *time.Time `json:"bmc_checked_at,omitempty"`
	PDUCheckedAt *time.Time `json:"pdu_checked_at,omitempty"`
}

// outletsAggregate collapses outlet states to on/off/mixed/unknown
func (s *ServerState) outletsAggregate() PowerState {
	if len(s.Outlets) == 0 || s.PDU != LinkOK {
		return PowerUnknown
	}
	on, off := 0, 0
	for _, o := range s.Outlets {
		switch o.State {
		case PowerOn:
			on++
		case PowerOff:
			off++
		}
	}
	switch {
	case on == len(s.Outlets):
		return PowerOn
	case off == len(s.Outlets):
		return PowerOff
	case on > 0 && off > 0:
		return "mixed"
	}
	return PowerUnknown
}

var transitionalStates = map[string]string{
	OpPowerOn:          StatePoweringOn,
	OpPowerOff:         StatePoweringOff,
	OpGracefulShutdown: StateShuttingDown,
	OpRestart:          StateRestarting,
	OpGracefulRestart:  StateRestarting,
	OpPowerCycle:       StatePowerCycling,
	OpReinstall:        StateRestarting,
}

// derive computes the display state from observed facts
func (s *ServerState) derive() {
	if s.Operation != nil {
		if state, ok := transitionalStates[s.Operation.Kind]; ok {
			s.State = state
		} else {
			s.State = StateBusy
		}
		return
	}

	outlets := s.outletsAggregate()
	switch {
	case s.BMC == LinkOK && s.Power == PowerOn:
		s.State = StateOn
	case s.BMC == LinkOK && s.Power == PowerOff:
		s.State = StateOff
	case s.BMC == LinkOK:
		s.State = StateUnknown
	case s.BMC == LinkUnreachable && outlets == PowerOff:
		s.State = StateOff // BMC is dark because its outlets are off
	case s.BMC == LinkUnreachable && (outlets == PowerOn || outlets == "mixed"):
		s.State = StatePowered
	case s.BMC == LinkNone && outlets == PowerOn:
		s.State = StateOn
	case s.BMC == LinkNone && outlets == PowerOff:
		s.State = StateOff
	case s.BMC == LinkNone && outlets == "mixed":
		s.State = StatePartial
	case s.BMC == LinkPending || (s.BMC == LinkNone && s.PDU == LinkPending):
		s.State = StateUnknown
	default:
		s.State = StateUnreachable
	}
}

// StateStore holds observed state for every server and publishes changes
type StateStore struct {
	mu     sync.RWMutex
	states map[string]*ServerState
	hub    *Hub
}

func NewStateStore(hub *Hub) *StateStore {
	return &StateStore{states: make(map[string]*ServerState), hub: hub}
}

// Sync adds entries for new servers and drops removed ones after a config (re)load
func (s *StateStore) Sync(config *Config) {
	s.mu.Lock()
	defer s.mu.Unlock()
	seen := make(map[string]bool)
	for i := range config.Servers {
		server := &config.Servers[i]
		seen[server.ID] = true
		st, ok := s.states[server.ID]
		if !ok {
			st = &ServerState{ServerID: server.ID, Power: PowerUnknown, BMC: LinkPending, PDU: LinkPending}
			s.states[server.ID] = st
		}
		if !server.HasBMC() {
			st.BMC, st.BMCError, st.Power = LinkNone, "", PowerUnknown
		} else if st.BMC == LinkNone {
			st.BMC = LinkPending
		}
		if len(server.Outlets()) == 0 {
			st.PDU, st.PDUError, st.Outlets = LinkNone, "", nil
		} else {
			if st.PDU == LinkNone {
				st.PDU = LinkPending
			}
			st.Outlets = reconcileOutlets(st.Outlets, server.Outlets())
		}
		st.derive()
	}
	for id := range s.states {
		if !seen[id] {
			delete(s.states, id)
		}
	}
}

func reconcileOutlets(existing []OutletStatus, refs []OutletRef) []OutletStatus {
	known := make(map[string]PowerState)
	for _, o := range existing {
		known[o.Ref] = o.State
	}
	out := make([]OutletStatus, len(refs))
	for i, ref := range refs {
		state, ok := known[ref.String()]
		if !ok {
			state = PowerUnknown
		}
		out[i] = OutletStatus{Ref: ref.String(), State: state}
	}
	return out
}

// update applies fn to a server's state, re-derives, and publishes if anything visible changed
func (s *StateStore) update(serverID string, fn func(*ServerState)) {
	s.mu.Lock()
	st, ok := s.states[serverID]
	if !ok {
		s.mu.Unlock()
		return
	}
	before := st.clone()
	fn(st)
	st.derive()
	after := st.clone()
	s.mu.Unlock()

	if !sameVisibleState(before, after) && s.hub != nil {
		s.hub.Publish("state", after)
	}
}

// sameVisibleState compares states ignoring check timestamps, so routine polls don't spam clients
func sameVisibleState(a, b ServerState) bool {
	a.BMCCheckedAt, a.PDUCheckedAt = nil, nil
	b.BMCCheckedAt, b.PDUCheckedAt = nil, nil
	return reflect.DeepEqual(a, b)
}

func (s *StateStore) RecordBMC(serverID string, power PowerState, err error) {
	now := time.Now()
	s.update(serverID, func(st *ServerState) {
		if st.BMC == LinkNone {
			return
		}
		st.BMCCheckedAt = &now
		if err != nil {
			st.BMC, st.BMCError, st.Power = LinkUnreachable, err.Error(), PowerUnknown
			return
		}
		st.BMC, st.BMCError, st.Power = LinkOK, "", power
	})
}

// RecordOutlets records polled outlet states; refs not in states are left alone
func (s *StateStore) RecordOutlets(serverID string, states map[string]PowerState, err error) {
	now := time.Now()
	s.update(serverID, func(st *ServerState) {
		if st.PDU == LinkNone {
			return
		}
		st.PDUCheckedAt = &now
		if err != nil {
			st.PDU, st.PDUError = LinkUnreachable, err.Error()
			for i := range st.Outlets {
				st.Outlets[i].State = PowerUnknown
			}
			return
		}
		st.PDU, st.PDUError = LinkOK, ""
		for i := range st.Outlets {
			if state, ok := states[st.Outlets[i].Ref]; ok {
				st.Outlets[i].State = state
			}
		}
	})
}

func (s *StateStore) SetOperation(serverID string, op *OperationSummary) {
	s.update(serverID, func(st *ServerState) { st.Operation = op })
}

func (s *StateStore) RecordBoot(serverID string, fetch BootFetch) {
	s.update(serverID, func(st *ServerState) { st.LastBoot = &fetch })
}

func (s *StateStore) Get(serverID string) (ServerState, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	st, ok := s.states[serverID]
	if !ok {
		return ServerState{}, false
	}
	return st.clone(), true
}

func (s *StateStore) All() []ServerState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]ServerState, 0, len(s.states))
	for _, st := range s.states {
		out = append(out, st.clone())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ServerID < out[j].ServerID })
	return out
}

func (st *ServerState) clone() ServerState {
	c := *st
	c.Outlets = append([]OutletStatus(nil), st.Outlets...)
	if st.Operation != nil {
		op := *st.Operation
		c.Operation = &op
	}
	if st.LastBoot != nil {
		b := *st.LastBoot
		c.LastBoot = &b
	}
	if st.BMCCheckedAt != nil {
		t := *st.BMCCheckedAt
		c.BMCCheckedAt = &t
	}
	if st.PDUCheckedAt != nil {
		t := *st.PDUCheckedAt
		c.PDUCheckedAt = &t
	}
	return c
}
