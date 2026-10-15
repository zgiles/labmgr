package main

import (
	"context"
	"fmt"
	"time"
)

const (
	waitBMCStartSeconds   = 300 // BMC boot after its outlets come on
	waitBMCRespondSeconds = 60  // BMC that should already be up
	waitPowerOnSeconds    = 180
	waitPowerOffSeconds   = 60
	waitShutdownSeconds   = 300 // graceful shutdown
	waitBootFetchSeconds  = 900 // reinstall: from reboot to iPXE fetching its script
	stateFreshness        = 20 * time.Second
)

// plan builds an operation's steps from the server's current state
func (e *Engine) plan(ctx context.Context, op *Operation) ([]*Step, error) {
	config := e.config.Get()
	server := config.Server(op.ServerID)

	switch op.Kind {
	case OpOutlet:
		return []*Step{outletStep(op.Target, op.Action)}, nil
	case OpBMC:
		return []*Step{bmcStep(op.Action)}, nil
	case OpBootOverride:
		return []*Step{bootStep(op.Target, op.Persistent)}, nil
	}

	if server == nil {
		return nil, notFound("server %s not found", op.ServerID)
	}
	st := e.freshState(ctx, server.ID)

	switch op.Kind {
	case OpPowerOn:
		return planPowerOn(config, server, st)
	case OpPowerOff:
		return planPowerOff(config, server, st)
	case OpGracefulShutdown:
		return planGracefulShutdown(server, st)
	case OpRestart:
		return planRestart(config, server, st, ActionReboot)
	case OpGracefulRestart:
		return planRestart(config, server, st, ActionGracefulRestart)
	case OpPowerCycle:
		return planPowerCycle(config, server, st)
	case OpReinstall:
		return planReinstall(config, server, st, op.Target)
	}
	return nil, badRequest("unknown operation kind %q", op.Kind)
}

// freshState returns the server's state, polling first if it's stale
func (e *Engine) freshState(ctx context.Context, serverID string) ServerState {
	st, _ := e.state.Get(serverID)
	stale := func(t *time.Time) bool { return t == nil || time.Since(*t) > stateFreshness }
	if (st.BMC != LinkNone && stale(st.BMCCheckedAt)) || (st.PDU != LinkNone && stale(st.PDUCheckedAt)) {
		pollCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		st, _ = e.poller.PollNow(pollCtx, serverID)
	}
	return st
}

func bmcStep(action string) *Step {
	labels := map[string]string{
		ActionOn:               "Power on through the BMC",
		ActionOff:              "Force power off through the BMC",
		ActionReboot:           "Hard reset through the BMC",
		ActionGracefulShutdown: "Ask the OS to shut down",
		ActionGracefulRestart:  "Ask the OS to restart",
		ActionPowerCycle:       "Power cycle through the BMC",
		ActionNMI:              "Send NMI (diagnostic interrupt)",
	}
	return &Step{Kind: StepBMC, Action: action, Label: labels[action], Status: StepPending}
}

func outletStep(ref, action string) *Step {
	return &Step{Kind: StepOutlet, Target: ref, Action: action, Label: fmt.Sprintf("Turn %s outlet %s", action, ref), Status: StepPending}
}

func waitStep(seconds int, why string) *Step {
	return &Step{Kind: StepWait, Seconds: seconds, Label: fmt.Sprintf("Wait %ds %s", seconds, why), Status: StepPending}
}

func waitPowerStep(want PowerState, seconds int) *Step {
	return &Step{Kind: StepWaitPower, Want: want, Seconds: seconds, Label: fmt.Sprintf("Wait for the system to report %s", want), Status: StepPending}
}

func waitBMCStep(seconds int) *Step {
	return &Step{Kind: StepWaitBMC, Seconds: seconds, Label: "Wait for the BMC to respond", Status: StepPending}
}

func bootStep(target string, persistent bool) *Step {
	label := "Boot from " + target + " next time"
	if persistent {
		label = "Always boot from " + target
	}
	if target == BootNone {
		label = "Clear the boot override"
	}
	return &Step{Kind: StepBootOverride, Target: target, Persistent: persistent, Label: label, Status: StepPending}
}

// pduSpacing is the gap between consecutive outlet actions (at least 3s; these PDUs dislike bursts)
func pduSpacing(config *Config) int {
	return max(config.Delays.BetweenPDU, 3)
}

// outletSteps switches each outlet not already in the wanted state, spaced apart
func outletSteps(config *Config, st ServerState, action string) []*Step {
	want := PowerState(action)
	var steps []*Step
	for _, o := range st.Outlets {
		if o.State == want {
			continue
		}
		if len(steps) > 0 {
			steps = append(steps, waitStep(pduSpacing(config), "between outlets"))
		}
		steps = append(steps, outletStep(o.Ref, action))
	}
	return steps
}

func planPowerOn(config *Config, server *Server, st ServerState) ([]*Step, error) {
	var steps []*Step
	hasOutlets := len(server.Outlets()) > 0

	outletsSwitched := false
	if hasOutlets {
		if st.PDU == LinkOK {
			steps = outletSteps(config, st, "on")
			outletsSwitched = len(steps) > 0
		} else if !server.HasBMC() {
			return nil, conflict("can't reach the PDU for %s (%s)", server.Name, st.PDUError)
		}
	}

	if server.HasBMC() {
		switch {
		case outletsSwitched:
			if config.Delays.PDUToPowerOn > 0 {
				steps = append(steps, waitStep(config.Delays.PDUToPowerOn, "for the BMC to start"))
			}
			steps = append(steps, waitBMCStep(waitBMCStartSeconds))
		case st.BMC != LinkOK:
			if hasOutlets && st.PDU != LinkOK {
				return nil, conflict("can't reach the BMC or the PDU for %s", server.Name)
			}
			steps = append(steps, waitBMCStep(waitBMCRespondSeconds))
		}
		if outletsSwitched || st.BMC != LinkOK || st.Power != PowerOn {
			steps = append(steps, bmcStep(ActionOn), waitPowerStep(PowerOn, waitPowerOnSeconds))
		}
	}

	if len(steps) == 0 {
		return nil, &nothingToDo{fmt.Sprintf("%s is already on", server.Name)}
	}
	return steps, nil
}

func planPowerOff(config *Config, server *Server, st ServerState) ([]*Step, error) {
	var steps []*Step
	hasOutlets := len(server.Outlets()) > 0

	bmcOff := false
	if server.HasBMC() && st.BMC == LinkOK && st.Power != PowerOff {
		steps = append(steps, bmcStep(ActionOff), waitPowerStep(PowerOff, waitPowerOffSeconds))
		bmcOff = true
	}

	if hasOutlets {
		if st.PDU == LinkOK {
			outlets := outletSteps(config, st, "off")
			if len(outlets) > 0 && bmcOff && config.Delays.PowerOffToPDU > 0 {
				steps = append(steps, waitStep(config.Delays.PowerOffToPDU, "before cutting outlets"))
			}
			steps = append(steps, outlets...)
		} else if !bmcOff && !(server.HasBMC() && st.BMC == LinkOK) {
			return nil, conflict("can't reach the BMC or the PDU for %s", server.Name)
		}
	} else if server.HasBMC() && st.BMC != LinkOK {
		return nil, conflict("can't reach the BMC for %s (%s)", server.Name, st.BMCError)
	}

	if len(steps) == 0 {
		return nil, &nothingToDo{fmt.Sprintf("%s is already off", server.Name)}
	}
	return steps, nil
}

func planGracefulShutdown(server *Server, st ServerState) ([]*Step, error) {
	if !server.HasBMC() {
		return nil, badRequest("%s has no BMC, so it can't be asked to shut down; use power off", server.Name)
	}
	if st.BMC != LinkOK {
		return nil, conflict("can't reach the BMC for %s (%s)", server.Name, st.BMCError)
	}
	if st.Power == PowerOff {
		return nil, &nothingToDo{fmt.Sprintf("%s is already off", server.Name)}
	}
	return []*Step{bmcStep(ActionGracefulShutdown), waitPowerStep(PowerOff, waitShutdownSeconds)}, nil
}

// planRestart restarts through the BMC when it can, otherwise power cycles the outlets
func planRestart(config *Config, server *Server, st ServerState, action string) ([]*Step, error) {
	if server.HasBMC() && st.BMC == LinkOK {
		if st.Power == PowerOff {
			return nil, conflict("%s is off; power it on instead", server.Name)
		}
		return []*Step{bmcStep(action)}, nil
	}
	if len(server.Outlets()) > 0 && st.PDU == LinkOK {
		return planPowerCycle(config, server, st)
	}
	return nil, conflict("can't reach the BMC or the PDU for %s", server.Name)
}

// planPowerCycle powers fully off (outlets included), pauses, and powers back on
func planPowerCycle(config *Config, server *Server, st ServerState) ([]*Step, error) {
	off, err := planPowerOff(config, server, st)
	if err != nil {
		if _, nothing := err.(*nothingToDo); !nothing {
			return nil, err
		}
	}

	// The state the server will be in once the off half finishes
	after := st
	after.Power = PowerOff
	after.Outlets = append([]OutletStatus(nil), st.Outlets...)
	for i := range after.Outlets {
		after.Outlets[i].State = PowerOff
	}
	if len(after.Outlets) > 0 {
		after.PDU = LinkOK
		after.BMC = LinkUnreachable // its outlets are off, so the BMC is too
	} else if server.HasBMC() {
		after.BMC = LinkOK
	}

	on, err := planPowerOn(config, server, after)
	if err != nil {
		return nil, err
	}

	steps := off
	if len(off) > 0 {
		steps = append(steps, waitStep(max(config.Delays.BetweenIPMI*2, 5), "with power off"))
	}
	return append(steps, on...), nil
}
