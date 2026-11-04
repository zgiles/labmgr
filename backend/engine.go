package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// Operation kinds
const (
	OpPowerOn          = "power_on"
	OpPowerOff         = "power_off"
	OpGracefulShutdown = "graceful_shutdown"
	OpRestart          = "restart"
	OpGracefulRestart  = "graceful_restart"
	OpPowerCycle       = "power_cycle"
	OpBMC              = "bmc"           // one raw BMC action
	OpOutlet           = "outlet"        // one outlet action
	OpBootOverride     = "boot_override" // set next boot device
	OpReinstall        = "reinstall"     // one-shot network install
)

var opLabels = map[string]string{
	OpPowerOn:          "Power on",
	OpPowerOff:         "Power off",
	OpGracefulShutdown: "Shut down",
	OpRestart:          "Restart",
	OpGracefulRestart:  "Restart (graceful)",
	OpPowerCycle:       "Power cycle",
	OpBMC:              "BMC action",
	OpOutlet:           "Outlet",
	OpBootOverride:     "Set boot device",
	OpReinstall:        "Reinstall",
}

type OpStatus string

const (
	OpScheduled   OpStatus = "scheduled"
	OpPending     OpStatus = "pending"
	OpRunning     OpStatus = "running"
	OpSucceeded   OpStatus = "succeeded"
	OpFailed      OpStatus = "failed"
	OpCancelled   OpStatus = "cancelled"
	OpInterrupted OpStatus = "interrupted" // labmgr restarted mid-operation
)

func (s OpStatus) Finished() bool {
	return s == OpSucceeded || s == OpFailed || s == OpCancelled || s == OpInterrupted
}

type StepKind string

const (
	StepBMC          StepKind = "bmc"
	StepOutlet       StepKind = "outlet"
	StepWait         StepKind = "wait"
	StepWaitPower    StepKind = "wait_power"
	StepWaitBMC      StepKind = "wait_bmc"
	StepBootOverride StepKind = "boot_override"
	StepAssignOS     StepKind = "assign_os"
	StepWaitBoot     StepKind = "wait_boot_fetch"
)

type StepStatus string

const (
	StepPending   StepStatus = "pending"
	StepRunning   StepStatus = "running"
	StepSucceeded StepStatus = "succeeded"
	StepFailed    StepStatus = "failed"
	StepSkipped   StepStatus = "skipped"
)

type Step struct {
	Kind       StepKind   `json:"kind"`
	Label      string     `json:"label"`
	Action     string     `json:"action,omitempty"` // BMC action, outlet action
	Target     string     `json:"target,omitempty"` // outlet ref, boot target, template ID
	Want       PowerState `json:"want,omitempty"`   // wait_power
	Seconds    int        `json:"seconds,omitempty"`
	Persistent bool       `json:"persistent,omitempty"` // boot override
	Optional   bool       `json:"optional,omitempty"`   // failure doesn't fail the operation

	Status     StepStatus `json:"status"`
	Attempts   int        `json:"attempts"`
	Error      string     `json:"error,omitempty"`
	StartedAt  *time.Time `json:"started_at,omitempty"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}

type Operation struct {
	ID         string `json:"id"`
	ServerID   string `json:"server_id,omitempty"`
	Kind       string `json:"kind"`
	Label      string `json:"label"`
	Action     string `json:"action,omitempty"`
	Target     string `json:"target,omitempty"`
	Persistent bool   `json:"persistent,omitempty"`

	Status  OpStatus `json:"status"`
	Steps   []*Step  `json:"steps"`
	Current int      `json:"current"`
	Error   string   `json:"error,omitempty"`
	Note    string   `json:"note,omitempty"`

	ConsoleCapture string `json:"console_capture,omitempty"` // recording started with this operation

	RequestedBy  string     `json:"requested_by,omitempty"`
	Source       string     `json:"source,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
	ScheduledFor *time.Time `json:"scheduled_for,omitempty"`
	StartedAt    *time.Time `json:"started_at,omitempty"`
	FinishedAt   *time.Time `json:"finished_at,omitempty"`
}

func (op *Operation) clone() Operation {
	c := *op
	c.Steps = make([]*Step, len(op.Steps))
	for i, s := range op.Steps {
		step := *s
		c.Steps[i] = &step
	}
	return c
}

func (op *Operation) lane() string {
	if op.ServerID != "" {
		return op.ServerID
	}
	return "outlet:" + op.Target
}

func (op *Operation) summary() *OperationSummary {
	s := &OperationSummary{ID: op.ID, Kind: op.Kind, Label: op.Label, Step: op.Current + 1, Steps: len(op.Steps)}
	if op.Current < len(op.Steps) {
		s.StepLabel = op.Steps[op.Current].Label
	}
	return s
}

// OpRequest asks the engine to do something to a server
type OpRequest struct {
	ServerID     string     `json:"server_id"`
	Kind         string     `json:"kind"`
	Action       string     `json:"action,omitempty"`
	Target       string     `json:"target,omitempty"`
	Persistent   bool       `json:"persistent,omitempty"`
	ScheduledFor *time.Time `json:"scheduled_for,omitempty"`
	RequestedBy  string     `json:"requested_by,omitempty"`
	Source       string     `json:"-"`
}

// OpError carries an HTTP status for API handlers
type OpError struct {
	Status int
	Msg    string
}

func (e *OpError) Error() string { return e.Msg }

func badRequest(format string, args ...any) error {
	return &OpError{Status: 400, Msg: fmt.Sprintf(format, args...)}
}
func notFound(format string, args ...any) error {
	return &OpError{Status: 404, Msg: fmt.Sprintf(format, args...)}
}
func conflict(format string, args ...any) error {
	return &OpError{Status: 409, Msg: fmt.Sprintf(format, args...)}
}

// nothingToDo means the server is already where the operation would put it
type nothingToDo struct{ msg string }

func (n *nothingToDo) Error() string { return n.msg }

// Timing controls retry and polling pace; tests shrink it
type Timing struct {
	PollInterval time.Duration // wait_power / wait_bmc checks
	RetryBase    time.Duration // first retry delay, doubling
	ActionTime   time.Duration // timeout for one BMC/PDU action
	Unit         time.Duration // one "second" in plans (delays, timeouts)
}

var defaultTiming = Timing{PollInterval: 3 * time.Second, RetryBase: 5 * time.Second, ActionTime: 30 * time.Second, Unit: time.Second}

const (
	opRetention = 30 * 24 * time.Hour
	maxOps      = 1000
)

// Engine plans and runs operations, one at a time per server
type Engine struct {
	config *ConfigStore
	hw     Hardware
	state  *StateStore
	poller *Poller
	store  *Store
	hub    *Hub
	os     *OSManager
	timing Timing

	mu      sync.Mutex
	ops     map[string]*Operation
	active  map[string]string // lane -> operation ID
	cancels map[string]context.CancelFunc
	seq     atomic.Uint64
	wg      sync.WaitGroup

	// onStart runs as an operation begins (before its first step); it may start a
	// console recording and return its name
	onStart func(op Operation) string
}

func NewEngine(config *ConfigStore, hw Hardware, state *StateStore, poller *Poller, store *Store, hub *Hub, osm *OSManager) *Engine {
	return &Engine{
		config:  config,
		hw:      hw,
		state:   state,
		poller:  poller,
		store:   store,
		hub:     hub,
		os:      osm,
		timing:  defaultTiming,
		ops:     make(map[string]*Operation),
		active:  make(map[string]string),
		cancels: make(map[string]context.CancelFunc),
	}
}

// Load restores persisted operations. Anything that was mid-flight is marked
// interrupted: power operations are never resumed blindly after a restart.
func (e *Engine) Load() error {
	ops, err := e.store.LoadOps()
	if err != nil {
		return err
	}
	now := time.Now()
	for _, op := range ops {
		if op.Status == OpRunning || op.Status == OpPending {
			op.Status = OpInterrupted
			op.Error = "Lab Manager restarted while this was running"
			op.FinishedAt = &now
			for _, s := range op.Steps {
				if s.Status == StepRunning {
					s.Status = StepFailed
					s.Error = "interrupted"
				}
			}
			e.store.SaveOp(op)
		}
		e.ops[op.ID] = op
	}
	log.Printf("Loaded %d operations from history", len(ops))
	return nil
}

// Run starts due scheduled operations until ctx ends
func (e *Engine) Run(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	lastPrune := time.Time{}
	for {
		e.startDue(ctx)
		if time.Since(lastPrune) > time.Hour {
			e.prune()
			lastPrune = time.Now()
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Wait blocks until running operations finish (used in tests and shutdown)
func (e *Engine) Wait() { e.wg.Wait() }

func (e *Engine) newID() string {
	return fmt.Sprintf("op-%s-%d", time.Now().Format("20060102-150405"), e.seq.Add(1))
}

// Submit validates a request and either schedules it or plans and starts it immediately
func (e *Engine) Submit(ctx context.Context, req OpRequest) (Operation, error) {
	op, err := e.newOperation(req)
	if err != nil {
		return Operation{}, err
	}

	if req.ScheduledFor != nil && req.ScheduledFor.After(time.Now()) {
		op.Status = OpScheduled
		t := *req.ScheduledFor
		op.ScheduledFor = &t
		e.mu.Lock()
		e.ops[op.ID] = op
		snap := op.clone()
		e.mu.Unlock()
		e.persist(snap)
		e.logEvent(op, fmt.Sprintf("%s scheduled for %s", op.Label, t.Local().Format("Mon Jan 2 15:04")), "info")
		return snap, nil
	}

	e.mu.Lock()
	if busy, ok := e.active[op.lane()]; ok {
		running := e.ops[busy]
		e.mu.Unlock()
		return Operation{}, conflict("%s is busy: %s (%s)", e.serverName(op.ServerID), running.Label, running.ID)
	}
	op.Status = OpPending
	e.active[op.lane()] = op.ID
	e.ops[op.ID] = op
	e.mu.Unlock()

	steps, err := e.plan(ctx, op)
	if err != nil {
		e.mu.Lock()
		delete(e.active, op.lane())
		delete(e.ops, op.ID)
		e.mu.Unlock()
		var nothing *nothingToDo
		if errors.As(err, &nothing) {
			return Operation{}, conflict("%s", nothing.msg)
		}
		return Operation{}, err
	}
	e.logEvent(op, fmt.Sprintf("%s started", op.Label), "info")
	return e.start(op, steps), nil
}

func (e *Engine) newOperation(req OpRequest) (*Operation, error) {
	config := e.config.Get()
	op := &Operation{
		ID:          e.newID(),
		ServerID:    req.ServerID,
		Kind:        req.Kind,
		Action:      req.Action,
		Target:      req.Target,
		Persistent:  req.Persistent,
		RequestedBy: req.RequestedBy,
		Source:      req.Source,
		CreatedAt:   time.Now(),
		Steps:       []*Step{},
	}

	label, ok := opLabels[req.Kind]
	if !ok {
		return nil, badRequest("unknown operation kind %q", req.Kind)
	}

	var server *Server
	if req.ServerID != "" {
		if server = config.Server(req.ServerID); server == nil {
			return nil, notFound("server %s not found", req.ServerID)
		}
	} else if req.Kind != OpOutlet {
		return nil, badRequest("server_id is required")
	}

	switch req.Kind {
	case OpBMC:
		if !validIPMIActions[req.Action] {
			return nil, badRequest("invalid BMC action %q", req.Action)
		}
		if !server.HasBMC() {
			return nil, badRequest("%s has no BMC configured", server.Name)
		}
		label = "BMC " + req.Action
	case OpOutlet:
		if _, ok := pduCommands[req.Action]; !ok {
			return nil, badRequest("invalid outlet action %q", req.Action)
		}
		ref, err := parseOutletRef(req.Target)
		if err != nil {
			return nil, badRequest("%v", err)
		}
		if config.PDU(ref.PDUID) == nil {
			return nil, notFound("PDU %s not found", ref.PDUID)
		}
		op.Target = ref.String()
		if server == nil {
			if owner := config.ServerForOutlet(ref); owner != nil {
				server, op.ServerID = owner, owner.ID
			}
		}
		label = fmt.Sprintf("Outlet %s %s", op.Target, req.Action)
	case OpBootOverride:
		if !validBootTargets[req.Target] {
			return nil, badRequest("invalid boot target %q", req.Target)
		}
		if !server.HasBMC() {
			return nil, badRequest("%s has no BMC configured", server.Name)
		}
		label = "Next boot: " + req.Target
		if req.Persistent {
			label = "Always boot: " + req.Target
		}
	case OpReinstall:
		if _, ok := e.os.GetTemplate(req.Target); !ok {
			return nil, notFound("OS template %s not found", req.Target)
		}
		if !server.HasBMC() {
			return nil, badRequest("%s has no BMC configured; reinstall needs one to set the boot device", server.Name)
		}
		if server.MAC() == "" {
			return nil, badRequest("%s has no metadata.mac, so it can't be matched when it network boots", server.Name)
		}
	}

	if server != nil {
		op.Label = fmt.Sprintf("%s %s", label, server.Name)
	} else {
		op.Label = label
	}
	return op, nil
}

func (e *Engine) serverName(id string) string {
	if s := e.config.Get().Server(id); s != nil {
		return s.Name
	}
	return id
}

// start marks a reserved, planned operation running and executes it in the background
func (e *Engine) start(op *Operation, steps []*Step) Operation {
	ctx, cancel := context.WithCancel(context.Background())
	now := time.Now()

	// Start recording before the first step, so the reboot itself is captured
	capture := ""
	if e.onStart != nil && op.ServerID != "" {
		capture = e.onStart(Operation{ID: op.ID, ServerID: op.ServerID, Kind: op.Kind})
	}

	e.mu.Lock()
	op.Steps = steps
	op.Status = OpRunning
	op.StartedAt = &now
	op.ConsoleCapture = capture
	op.Current = 0
	e.cancels[op.ID] = cancel
	snap := op.clone()
	e.mu.Unlock()

	e.persist(snap)
	if op.ServerID != "" {
		e.state.SetOperation(op.ServerID, snap.summary())
		e.poller.Boost(op.ServerID, 10*time.Minute)
	}

	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		e.run(ctx, op)
	}()
	return snap
}

func (e *Engine) run(ctx context.Context, op *Operation) {
	var failure error
	for i := range op.Steps {
		e.mutate(op, func(op *Operation) {
			now := time.Now()
			op.Current = i
			op.Steps[i].Status = StepRunning
			op.Steps[i].StartedAt = &now
		})

		err := e.execStep(ctx, op, i)

		e.mutate(op, func(op *Operation) {
			now := time.Now()
			step := op.Steps[i]
			step.FinishedAt = &now
			if err != nil {
				step.Status = StepFailed
				step.Error = err.Error()
			} else {
				step.Status = StepSucceeded
				step.Error = ""
			}
		})

		if err != nil {
			if ctx.Err() != nil {
				failure = ctx.Err()
				break
			}
			if op.Steps[i].Optional {
				continue
			}
			failure = err
			break
		}
	}
	e.finish(op, failure)
}

func (e *Engine) finish(op *Operation, failure error) {
	// Settle everything else first, so anyone who sees the operation finished
	// also sees the server free (and a failed reinstall already cleaned up)
	e.mu.Lock()
	now := time.Now()
	op.FinishedAt = &now
	switch {
	case failure == nil:
		op.Status = OpSucceeded
	case errors.Is(failure, context.Canceled):
		op.Status = OpCancelled
		op.Error = "Cancelled"
	default:
		op.Status = OpFailed
		op.Error = failure.Error()
	}
	for _, s := range op.Steps {
		if s.Status == StepPending {
			s.Status = StepSkipped
		}
	}
	if e.active[op.lane()] == op.ID {
		delete(e.active, op.lane())
	}
	if cancel, ok := e.cancels[op.ID]; ok {
		cancel()
		delete(e.cancels, op.ID)
	}
	snap := op.clone()
	e.mu.Unlock()

	if op.Kind == OpReinstall && snap.Status != OpSucceeded {
		e.abandonReinstall(op)
	}
	if op.ServerID != "" {
		e.state.SetOperation(op.ServerID, nil)
		e.poller.Boost(op.ServerID, time.Minute)
	}
	e.persist(snap)

	switch snap.Status {
	case OpSucceeded:
		e.logEvent(op, op.Label+" succeeded", "info")
	case OpCancelled:
		e.logEvent(op, op.Label+" cancelled", "warning")
	default:
		e.logEvent(op, fmt.Sprintf("%s failed: %s", op.Label, snap.Error), "error")
	}
}

// mutate changes an operation under the lock, then persists and publishes it
func (e *Engine) mutate(op *Operation, fn func(*Operation)) {
	e.mu.Lock()
	fn(op)
	snap := op.clone()
	e.mu.Unlock()

	e.persist(snap)
	if snap.Status == OpRunning && snap.ServerID != "" {
		e.state.SetOperation(snap.ServerID, snap.summary())
	}
}

func (e *Engine) persist(snap Operation) {
	if err := e.store.SaveOp(&snap); err != nil {
		log.Printf("Warning: failed to save operation %s: %v", snap.ID, err)
	}
	e.hub.Publish("operation", snap)
}

func (e *Engine) logEvent(op *Operation, message, level string) {
	event := &Event{ServerID: op.ServerID, Kind: "operation", Message: message, Actor: op.RequestedBy, Level: level}
	if err := e.store.AppendEvent(event); err != nil {
		log.Printf("Warning: failed to record event: %v", err)
	}
	e.hub.Publish("event", event)
	log.Printf("[%s] %s", op.ID, message)
}

// startDue launches scheduled operations whose time has come and whose server is free
func (e *Engine) startDue(ctx context.Context) {
	now := time.Now()
	e.mu.Lock()
	var due []*Operation
	for _, op := range e.ops {
		if op.Status != OpScheduled || op.ScheduledFor == nil || op.ScheduledFor.After(now) {
			continue
		}
		if _, busy := e.active[op.lane()]; busy {
			continue // waits for the server to be free
		}
		op.Status = OpPending
		e.active[op.lane()] = op.ID
		due = append(due, op)
	}
	e.mu.Unlock()

	for _, op := range due {
		e.wg.Add(1)
		go func(op *Operation) {
			defer e.wg.Done()
			planCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			steps, err := e.plan(planCtx, op)
			cancel()
			if err != nil {
				e.mutate(op, func(op *Operation) {
					var nothing *nothingToDo
					if errors.As(err, &nothing) {
						op.Note = nothing.msg
					}
				})
				var nothing *nothingToDo
				if errors.As(err, &nothing) {
					e.finish(op, nil)
				} else {
					e.finish(op, err)
				}
				return
			}
			e.logEvent(op, fmt.Sprintf("%s started (scheduled)", op.Label), "info")
			e.start(op, steps)
		}(op)
	}
}

// Cancel stops a scheduled or running operation
func (e *Engine) Cancel(id string) error {
	e.mu.Lock()
	op, ok := e.ops[id]
	if !ok {
		e.mu.Unlock()
		return notFound("operation %s not found", id)
	}
	switch op.Status {
	case OpScheduled:
		now := time.Now()
		op.Status = OpCancelled
		op.Error = "Cancelled"
		op.FinishedAt = &now
		snap := op.clone()
		e.mu.Unlock()
		e.persist(snap)
		e.logEvent(op, op.Label+" cancelled", "warning")
		return nil
	case OpRunning, OpPending:
		cancel := e.cancels[id]
		e.mu.Unlock()
		if cancel != nil {
			cancel()
		}
		return nil
	default:
		e.mu.Unlock()
		return conflict("operation %s already %s", id, op.Status)
	}
}

// RunNow moves a scheduled operation's time to now
func (e *Engine) RunNow(id string) error {
	e.mu.Lock()
	op, ok := e.ops[id]
	if !ok {
		e.mu.Unlock()
		return notFound("operation %s not found", id)
	}
	if op.Status != OpScheduled {
		e.mu.Unlock()
		return conflict("operation %s is %s, not scheduled", id, op.Status)
	}
	now := time.Now()
	op.ScheduledFor = &now
	snap := op.clone()
	e.mu.Unlock()
	e.persist(snap)
	return nil
}

// Retry submits a fresh operation with the same request as a finished one
func (e *Engine) Retry(ctx context.Context, id, requestedBy string) (Operation, error) {
	e.mu.Lock()
	op, ok := e.ops[id]
	if !ok {
		e.mu.Unlock()
		return Operation{}, notFound("operation %s not found", id)
	}
	if !op.Status.Finished() || op.Status == OpSucceeded {
		e.mu.Unlock()
		return Operation{}, conflict("only failed, cancelled, or interrupted operations can be retried")
	}
	req := OpRequest{ServerID: op.ServerID, Kind: op.Kind, Action: op.Action, Target: op.Target, Persistent: op.Persistent, RequestedBy: requestedBy, Source: "retry"}
	e.mu.Unlock()
	return e.Submit(ctx, req)
}

func (e *Engine) Get(id string) (Operation, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	op, ok := e.ops[id]
	if !ok {
		return Operation{}, false
	}
	return op.clone(), true
}

// OpFilter narrows List
type OpFilter struct {
	ServerID string
	Active   bool // scheduled, pending, or running only
	Limit    int
}

// List returns operations newest first
func (e *Engine) List(f OpFilter) []Operation {
	e.mu.Lock()
	out := make([]Operation, 0, len(e.ops))
	for _, op := range e.ops {
		if f.ServerID != "" && op.ServerID != f.ServerID {
			continue
		}
		if f.Active && op.Status.Finished() {
			continue
		}
		out = append(out, op.clone())
	}
	e.mu.Unlock()

	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	if f.Limit > 0 && len(out) > f.Limit {
		out = out[:f.Limit]
	}
	return out
}

// prune drops old finished operations
func (e *Engine) prune() {
	e.mu.Lock()
	var finished []*Operation
	for _, op := range e.ops {
		if op.Status.Finished() {
			finished = append(finished, op)
		}
	}
	sort.Slice(finished, func(i, j int) bool { return finished[i].CreatedAt.After(finished[j].CreatedAt) })
	var drop []string
	for i, op := range finished {
		if i >= maxOps || (op.FinishedAt != nil && time.Since(*op.FinishedAt) > opRetention) {
			drop = append(drop, op.ID)
			delete(e.ops, op.ID)
		}
	}
	e.mu.Unlock()
	if len(drop) > 0 {
		if err := e.store.DeleteOps(drop); err != nil {
			log.Printf("Warning: failed to prune operations: %v", err)
		}
	}
}
