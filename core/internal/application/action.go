// Package application is the coordinator: the single writer of account state,
// the action index and the queue.
//
// Everything that changes those happens on one goroutine. Callers send it a
// message and wait for the answer, and the workers that touch the network send
// their results back the same way. That is not a style preference: spec 02
// requires a single writer, and the alternative -- a mutex around a state
// struct that several goroutines update -- is what makes "the old worker's
// result overwrote the new one" possible in the first place. Here a late result
// arrives as a message, is compared against the current sequence, and is
// dropped.
//
// The scheduling arithmetic lives next door in policy, where it can be tested
// without starting anything.
package application

import (
	"context"
	"slices"
	"strconv"
	"time"

	"github.com/matthewlu070111/smart-srun/core/internal/domain"
	"github.com/matthewlu070111/smart-srun/core/internal/policy"
)

// Kind is the closed set of things the coordinator can be asked to do.
//
// A closed set, not a string handed to a dispatcher: spec 03 requires unknown
// actions to be refused as InvalidArgument and forbids ever resolving a command
// name dynamically. The account and hotspot CRUD verbs the LuCI page also calls
// "actions" are not here -- those are configuration transactions and go through
// the config repository, which is a different kind of write with a different
// kind of failure.
type Kind string

const (
	// KindLogin is an explicit manual login.
	KindLogin Kind = "manual_login"
	// KindLogout is an explicit manual logout.
	KindLogout Kind = "manual_logout"
	// KindRelogin is logout followed by login on the same line.
	KindRelogin Kind = "relogin"
	// KindSwitchCampus changes the active campus account and authenticates it.
	KindSwitchCampus Kind = "switch_campus"
	// KindSwitchHotspot moves the uplink to a hotspot profile.
	KindSwitchHotspot Kind = "switch_hotspot"
	// KindMaintain is one round of the automatic authentication loop. It is
	// scheduler-only: no RPC submits it.
	KindMaintain Kind = "maintain"
	// KindForcedLogout is one account's share of the quiet-hours sweep.
	KindForcedLogout Kind = "forced_logout"
)

var kinds = []Kind{KindLogin, KindLogout, KindRelogin, KindSwitchCampus,
	KindSwitchHotspot, KindMaintain, KindForcedLogout}

func (k Kind) Valid() bool { return slices.Contains(kinds, k) }

// Priority is where this kind sits in spec 04's order.
func (k Kind) Priority() policy.Priority {
	switch k {
	case KindMaintain:
		return policy.PriorityMaintenance
	case KindForcedLogout:
		return policy.PriorityQuietBoundary
	default:
		return policy.PriorityUserAction
	}
}

// Manual reports whether a user asked for this directly. Manual actions still
// run while automatic authentication is switched off.
func (k Kind) Manual() bool {
	return k != KindMaintain && k != KindForcedLogout
}

// State is the action lifecycle from spec 04. The four terminal states are
// irreversible.
type State string

const (
	StateQueued  State = "queued"
	StateRunning State = "running"

	StateSucceeded State = "succeeded"
	StateFailed    State = "failed"
	StateCancelled State = "cancelled"
	// StateInterrupted is an action that was still going when the service
	// stopped. It is not "failed": spec 02 forbids replaying an action the user
	// force-stopped, and the two need to be distinguishable to obey that.
	StateInterrupted State = "interrupted"
)

func (s State) Terminal() bool {
	switch s {
	case StateSucceeded, StateFailed, StateCancelled, StateInterrupted:
		return true
	default:
		return false
	}
}

// Phase is progress within a running action.
//
// Spec 04 requires it separately from State because the last log line is not a
// completion signal: an action stuck in `challenge` and an action stuck in
// `verify` are different problems, and neither is visible from "running".
type Phase string

const (
	PhaseWaitingLink Phase = "waiting_link"
	PhaseChallenge   Phase = "challenge"
	PhaseLogin       Phase = "login"
	PhaseVerify      Phase = "verify"
	PhaseLogout      Phase = "logout"
	PhaseSwitch      Phase = "switch"
)

// Request is one submission.
type Request struct {
	Kind      Kind
	AccountID string
	// HotspotID is only meaningful for KindSwitchHotspot.
	HotspotID string
	// IdempotencyKey is supplied by the caller -- the LuCI click id, or the CLI
	// invocation. Resubmitting the same key returns the same action instead of
	// starting a second one, which is what makes a double-click harmless.
	IdempotencyKey string
	// IgnoreQuiet is the single-action quiet-hours override from spec 04. It
	// applies to this action and nothing else: the maintenance loop behind it
	// stays suspended.
	IgnoreQuiet bool
}

// fingerprint is everything about a request except its key.
//
// Two submissions sharing a key must be the same request. If they are not, the
// caller has reused a key for different work, and returning the first action's
// id would report the wrong thing as finished -- so it is a Conflict. The
// fields are joined with a separator that cannot appear in an id, so no pair of
// different requests can collide.
func (r Request) fingerprint() string {
	quiet := "0"
	if r.IgnoreQuiet {
		quiet = "1"
	}
	return string(r.Kind) + "\x00" + r.AccountID + "\x00" + r.HotspotID + "\x00" + quiet
}

// Validate refuses a request the coordinator could not act on.
func (r Request) Validate() error {
	if !r.Kind.Valid() {
		return domain.Errorf(domain.CodeInvalidArgument, "未知的动作类型：%q", string(r.Kind))
	}
	if r.AccountID == "" && r.Kind != KindSwitchHotspot {
		return domain.Errorf(domain.CodeInvalidArgument, "动作 %s 需要指定账号", string(r.Kind))
	}
	if r.Kind == KindSwitchHotspot && r.HotspotID == "" {
		return domain.Errorf(domain.CodeInvalidArgument, "切换热点需要指定热点")
	}
	if r.IdempotencyKey == "" {
		return domain.Errorf(domain.CodeInvalidArgument, "动作需要一个幂等键")
	}
	return nil
}

// Action is the coordinator's record of one submission.
//
// It is copied out to callers, never handed out by pointer: a reader holding a
// pointer into the coordinator's state would be a second writer's worth of
// races away from the single-writer rule.
type Action struct {
	ID      string
	Request Request
	// Line is the resolved line this action occupies while it runs. Two actions
	// on one line never overlap.
	Line string

	State   State
	Phase   Phase
	Message string
	Code    domain.ErrorCode

	QueuedAt  time.Time
	StartedAt time.Time
	EndedAt   time.Time

	// Sequence is assigned at dispatch and increases forever. A result carrying
	// an older sequence belongs to a worker that was cancelled and finished
	// anyway; it is dropped rather than applied.
	Sequence uint64

	// ordinal is submission order. It breaks ties in the queue, where the id
	// string cannot: "a10" sorts before "a2".
	ordinal uint64
	// retired records that this action has already been filed in the bounded
	// terminal history, so the two paths that can finish one -- a cancellation
	// and a worker's result -- cannot file it twice.
	retired bool
}

// transition moves to the next state, refusing to leave a terminal one.
func (a *Action) transition(next State, at time.Time) bool {
	if a.State.Terminal() {
		return false
	}
	a.State = next
	if next == StateRunning {
		a.StartedAt = at
	}
	if next.Terminal() {
		a.EndedAt = at
	}
	return true
}

// Outcome is what a Runner reports.
type Outcome struct {
	// State must be StateSucceeded or StateFailed. Anything else is a Runner
	// that decided the action's fate on its own; the coordinator owns that.
	State   State
	Message string
	Code    domain.ErrorCode
}

// Runner performs one action.
//
// This is the seam between the coordinator, which is scheduling and nothing
// else, and the packages that touch the network. It is defined here because
// this is the consumer: spec 02 asks for interfaces where they are used, and
// the alternative -- auth exporting a Coordinator-shaped interface -- would
// point the dependency the wrong way.
//
// report is how progress reaches the state a user polls. It may be called any
// number of times and never blocks the coordinator's loop.
type Runner interface {
	Run(ctx context.Context, action Action, report func(Phase)) Outcome
}

// Receipt is what a submitter gets back. Accepting an action is not performing
// it: spec 03 is explicit that queued does not mean succeeded, so the receipt
// carries an id to poll rather than a result.
type Receipt struct {
	ActionID string
	State    State
	// Duplicate says the key matched an action that already existed, so nothing
	// new was started.
	Duplicate bool
}

func formatID(n uint64) string { return "a" + strconv.FormatUint(n, 10) }
