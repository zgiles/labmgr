package main

import (
	"context"
	"fmt"
	"time"
)

// execStep runs one step of an operation
func (e *Engine) execStep(ctx context.Context, op *Operation, i int) error {
	step := *op.Steps[i] // steps are immutable apart from status fields
	config := e.config.Get()
	server := config.Server(op.ServerID)

	switch step.Kind {
	case StepBMC:
		bmc, err := e.bmcFor(server)
		if err != nil {
			return err
		}
		err = e.withRetries(ctx, op, i, 4, func(ctx context.Context) error { return bmc.Reset(ctx, step.Action) })
		e.poller.Boost(op.ServerID, 2*time.Minute)
		return err

	case StepOutlet:
		ref, err := parseOutletRef(step.Target)
		if err != nil {
			return err
		}
		pdu := config.PDU(ref.PDUID)
		if pdu == nil {
			return fmt.Errorf("PDU %s not found", ref.PDUID)
		}
		driver := e.hw.PDU(pdu)
		err = e.withRetries(ctx, op, i, 3, func(ctx context.Context) error { return driver.SetOutlet(ctx, ref.Outlet, step.Action) })
		if err == nil && op.ServerID != "" {
			go e.poller.PollNow(context.Background(), op.ServerID)
		}
		return err

	case StepWait:
		return sleepCtx(ctx, time.Duration(step.Seconds)*e.timing.Unit)

	case StepWaitPower:
		bmc, err := e.bmcFor(server)
		if err != nil {
			return err
		}
		var last PowerState
		var lastErr error
		err = e.waitUntil(ctx, time.Duration(step.Seconds)*e.timing.Unit, func(ctx context.Context) bool {
			last, lastErr = bmc.PowerState(ctx)
			e.state.RecordBMC(op.ServerID, last, lastErr)
			return lastErr == nil && last == step.Want
		})
		if err != nil {
			if lastErr != nil {
				return fmt.Errorf("system didn't report %s within %ds (last error: %v)", step.Want, step.Seconds, lastErr)
			}
			return fmt.Errorf("system still %s after %ds", last, step.Seconds)
		}
		return nil

	case StepWaitBMC:
		bmc, err := e.bmcFor(server)
		if err != nil {
			return err
		}
		var lastErr error
		err = e.waitUntil(ctx, time.Duration(step.Seconds)*e.timing.Unit, func(ctx context.Context) bool {
			var power PowerState
			power, lastErr = bmc.PowerState(ctx)
			e.state.RecordBMC(op.ServerID, power, lastErr)
			return lastErr == nil
		})
		if err != nil {
			return fmt.Errorf("BMC didn't respond within %ds: %v", step.Seconds, lastErr)
		}
		return nil

	case StepBootOverride:
		bmc, err := e.bmcFor(server)
		if err != nil {
			return err
		}
		return e.withRetries(ctx, op, i, 3, func(ctx context.Context) error {
			return bmc.SetBootOverride(ctx, step.Target, step.Persistent)
		})

	case StepAssignOS:
		return e.assignOS(op, step)

	case StepWaitBoot:
		return e.waitForBootFetch(ctx, op, step)
	}
	return fmt.Errorf("unknown step kind %q", step.Kind)
}

func (e *Engine) bmcFor(server *Server) (BMC, error) {
	if server == nil {
		return nil, fmt.Errorf("server no longer in config")
	}
	bmc := e.hw.BMC(server)
	if bmc == nil {
		return nil, fmt.Errorf("%s has no BMC configured", server.Name)
	}
	return bmc, nil
}

// withRetries runs fn up to attempts times with doubling backoff, recording attempts on the step
func (e *Engine) withRetries(ctx context.Context, op *Operation, i, attempts int, fn func(context.Context) error) error {
	var err error
	delay := e.timing.RetryBase
	for attempt := 1; attempt <= attempts; attempt++ {
		e.mutate(op, func(op *Operation) { op.Steps[i].Attempts = attempt })

		actionCtx, cancel := context.WithTimeout(ctx, e.timing.ActionTime)
		err = fn(actionCtx)
		cancel()
		if err == nil || ctx.Err() != nil {
			return err
		}

		if attempt < attempts {
			msg := fmt.Sprintf("attempt %d failed: %v; retrying in %s", attempt, err, delay)
			e.mutate(op, func(op *Operation) { op.Steps[i].Error = msg })
			if serr := sleepCtx(ctx, delay); serr != nil {
				return serr
			}
			delay *= 2
		}
	}
	return fmt.Errorf("failed after %d attempts: %w", attempts, err)
}

// waitUntil checks cond every poll interval until it's true or timeout passes
func (e *Engine) waitUntil(ctx context.Context, timeout time.Duration, cond func(context.Context) bool) error {
	deadline := time.Now().Add(timeout)
	for {
		checkCtx, cancel := context.WithTimeout(ctx, bmcPollTimeout)
		ok := cond(checkCtx)
		cancel()
		if ok {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out")
		}
		if err := sleepCtx(ctx, e.timing.PollInterval); err != nil {
			return err
		}
	}
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
