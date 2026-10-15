package main

import (
	"context"
	"fmt"
	"time"
)

// planReinstall serves a template once, boots the server from the network, and
// waits until it fetches its boot script. Afterwards it boots as before.
func planReinstall(config *Config, server *Server, st ServerState, templateID string) ([]*Step, error) {
	if st.BMC != LinkOK && !(len(server.Outlets()) > 0 && st.PDU == LinkOK) {
		return nil, conflict("can't reach the BMC for %s (%s)", server.Name, st.BMCError)
	}

	steps := []*Step{
		{Kind: StepAssignOS, Target: templateID, Label: "Serve " + templateID + " on the next network boot", Status: StepPending},
	}

	if st.BMC == LinkOK && st.Power == PowerOn {
		steps = append(steps, bootStep(BootPXE, false), bmcStep(ActionReboot))
	} else {
		// Off (or BMC dark behind its outlets): bring it up first, then set the boot device
		on, err := planPowerOn(config, server, st)
		if err != nil {
			return nil, err
		}
		// Set PXE once the BMC is answering but before the system powers on
		var withBoot []*Step
		inserted := false
		for _, step := range on {
			if !inserted && step.Kind == StepBMC && step.Action == ActionOn {
				withBoot = append(withBoot, bootStep(BootPXE, false))
				inserted = true
			}
			withBoot = append(withBoot, step)
		}
		if !inserted {
			return nil, conflict("%s can't be powered on through its BMC", server.Name)
		}
		steps = append(steps, withBoot...)
	}

	steps = append(steps, &Step{Kind: StepWaitBoot, Seconds: waitBootFetchSeconds, Label: "Wait for the server to fetch its boot script", Status: StepPending})
	return steps, nil
}

func (e *Engine) assignOS(op *Operation, step Step) error {
	if err := e.os.AssignOneShot(op.ServerID, step.Target, fmt.Sprintf("%s (%s)", op.RequestedBy, op.ID)); err != nil {
		return err
	}
	saveOSData()
	e.hub.Publish("os", nil)
	return nil
}

// waitForBootFetch waits until the server pulls its iPXE script after this operation started
func (e *Engine) waitForBootFetch(ctx context.Context, op *Operation, step Step) error {
	e.mu.Lock()
	started := *op.StartedAt
	e.mu.Unlock()

	err := e.waitUntil(ctx, time.Duration(step.Seconds)*e.timing.Unit, func(context.Context) bool {
		st, _ := e.state.Get(op.ServerID)
		return st.LastBoot != nil && st.LastBoot.At.After(started)
	})
	if err != nil && ctx.Err() == nil {
		return fmt.Errorf("server didn't fetch a boot script within %ds; check that it network boots and that its MAC matches", step.Seconds)
	}
	return err
}

// abandonReinstall removes a reinstall's one-shot assignment if the server never used it,
// so a failed or cancelled reinstall can't surprise anyone on a later network boot
func (e *Engine) abandonReinstall(op *Operation) {
	assignment, ok := e.os.GetServerOSAssignment(op.ServerID)
	if !ok || !assignment.OneShot || assignment.TemplateID != op.Target {
		return
	}
	if e.os.ConsumeOneShot(op.ServerID) {
		saveOSData()
		e.hub.Publish("os", nil)
	}
}
