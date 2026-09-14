package daemon

import (
	"context"
	"errors"
	"os"
	"sync"
	"time"

	"github.com/matthewlu070111/smart-srun/core/internal/application"
	"github.com/matthewlu070111/smart-srun/core/internal/config"
	"github.com/matthewlu070111/smart-srun/core/internal/control"
	"github.com/matthewlu070111/smart-srun/core/internal/domain"
	"github.com/matthewlu070111/smart-srun/core/internal/observe"
	"github.com/matthewlu070111/smart-srun/core/internal/openwrt"
	"github.com/matthewlu070111/smart-srun/core/internal/policy"
)

// readBudget bounds an internal read of the coordinator's state.
//
// Every such read is a channel round-trip to a loop that is not doing anything
// slow, so this is a fault detector rather than a timeout anybody should reach.
const readBudget = 3 * time.Second

// Options configures the service.
type Options struct {
	Paths   Paths
	Clock   policy.Clock
	Version string

	// Runner performs actions. Without one the service still runs, still
	// answers, still queues -- and fails every action saying what is missing.
	Runner application.Runner

	// Capabilities is what this device can actually do, detected once by the
	// caller. Detected once because it does not change while the process runs,
	// and because status polling must not shell out.
	Capabilities openwrt.Capabilities

	// OnError receives faults that have nobody to return them to: a snapshot
	// that could not be written, a connection that failed mid-answer. It is the
	// seam the structured log replaces in M11. Nil discards them, which is a
	// choice a caller has to make on purpose.
	OnError func(error)

	// Ready is called once, when the socket is listening and every method this
	// build answers is bound. Before it, a caller that dialled would get a
	// connection refused; after it, the service is answering.
	Ready func()

	// Observer, if set, is called after each action change has been recorded.
	// It is how a supervisor watches the lifecycle without polling, and it is
	// where the structured event log attaches in M11.
	Observer func(application.Action)
}

// Daemon is the assembled service.
type Daemon struct {
	paths        Paths
	version      string
	clock        policy.Clock
	capabilities openwrt.Capabilities
	onError      func(error)

	config   *config.Repository
	store    *observe.Store
	actions  *application.Coordinator
	observer func(application.Action)

	// dirty is a one-slot signal, so a burst of changes coalesces into one
	// write instead of one write per change.
	dirty chan struct{}
}

// Run starts the service and returns when ctx is done.
//
// The order matters at both ends. The lock is taken before anything is opened,
// so a second copy fails before it can touch the first one's files. The socket
// is removed and the stopped snapshot written after the loops have stopped, so
// nothing is still answering when a reader is told the service is gone.
func Run(ctx context.Context, options Options) error {
	paths := options.Paths
	if paths.Runtime == "" || paths.Config == "" {
		defaults := DefaultPaths()
		if paths.Runtime == "" {
			paths.Runtime = defaults.Runtime
		}
		if paths.Config == "" {
			paths.Config = defaults.Config
		}
	}
	clock := options.Clock
	if clock == nil {
		clock = policy.SystemClock{}
	}
	runner := options.Runner
	if runner == nil {
		runner = UnavailableRunner{}
	}
	onError := options.OnError
	if onError == nil {
		onError = func(error) {}
	}

	if err := EnsureRuntimeDir(paths); err != nil {
		return err
	}
	lock, err := Acquire(paths.Lock())
	if err != nil {
		return err
	}
	defer lock.Release()

	repository, err := config.Open(paths.ConfigFile())
	if err != nil {
		return err
	}

	observer := options.Observer
	if observer == nil {
		observer = func(application.Action) {}
	}

	service := &Daemon{
		paths:        paths,
		version:      options.Version,
		clock:        clock,
		capabilities: options.Capabilities,
		onError:      onError,
		config:       repository,
		store:        observe.New(),
		observer:     observer,
		dirty:        make(chan struct{}, 1),
	}
	service.store.SetRevision(repository.Revision())
	service.actions = application.New(application.Options{
		Clock:    clock,
		Runner:   runner,
		Lines:    service.lineOf,
		Observer: service.onAction,
	})

	// The coordinator and the snapshot writer outlive the listener on purpose:
	// an RPC that is still being answered when the stop arrives can still read
	// the coordinator, and the last snapshot is written after both have stopped.
	background, stopBackground := context.WithCancel(context.Background())
	defer stopBackground()

	var loops sync.WaitGroup
	var coordinatorErr error
	loops.Go(func() { coordinatorErr = service.actions.Run(background) })
	loops.Go(func() { service.writeSnapshots(background) })

	listener, err := control.Listen(paths.Socket())
	if err != nil {
		stopBackground()
		loops.Wait()
		return err
	}

	registry := control.NewRegistry()
	service.register(registry)
	service.markDirty()
	if options.Ready != nil {
		options.Ready()
	}

	serveErr := control.Serve(ctx, listener, registry)

	stopBackground()
	loops.Wait()

	// The socket is already gone: closing a Unix listener unlinks the path it
	// created, and Serve closes its listener on every return. An explicit
	// removal here looked like belt and braces and was in fact unreachable --
	// a mutation of it changed nothing, which is how it was noticed. The case
	// it appeared to cover, a socket outliving a daemon that was killed, is
	// real; it is handled by the lifecycle helper, which is the only thing
	// running at that point.
	current := repository.Snapshot()
	if err := MarkStopped(paths, current.Enabled, repository.Revision(),
		options.Version); err != nil {
		onError(err)
	}
	return errors.Join(serveErr, coordinatorErr)
}

// wirelessLine is the scheduling key every wireless account shares.
//
// Spec 04 gives wireless a single global transaction: two accounts cannot both
// be re-associating a radio. Wired accounts get one key per interface. The keys
// are prefixed so an interface literally named "wireless" cannot collide with
// this one.
const wirelessLine = "wireless"

// lineOf is the coordinator's line resolver: a cheap lookup in the current
// configuration, never a probe. It runs while a submission waits.
func (d *Daemon) lineOf(request application.Request) string {
	cfg := d.config.Snapshot()
	account, known := cfg.CampusAccountByID(request.AccountID)
	switch {
	case !known:
		// An action for an account that is gone still gets a key of its own, so
		// it cannot serialise against an unrelated one on its way to failing.
		return "account:" + request.AccountID
	case account.IsWired():
		return "iface:" + account.WiredIface
	default:
		return wirelessLine
	}
}

// onAction is the coordinator's observer. It runs on the coordinator's
// goroutine, so it does no I/O: it updates the in-memory projection and rings a
// bell for the writer.
func (d *Daemon) onAction(action application.Action) {
	switch {
	case action.State == application.StateRunning:
		d.store.ActionStarted(action.Request.AccountID, action.ID)
	case action.State.Terminal():
		d.store.ActionFinished(action.Request.AccountID, observe.Note{
			ActionID: action.ID,
			Kind:     string(action.Request.Kind),
			State:    string(action.State),
			Message:  action.Message,
			Code:     action.Code,
			At:       action.EndedAt,
		})
	}
	d.markDirty()
	// After the projection, never before: a watcher told an action had finished
	// and then reading a status that did not say so would be watching a state
	// that briefly disagreed with itself.
	d.observer(action)
}

// markDirty never blocks. The signal is a bell, not a queue: one pending ring
// means "something changed", however many things changed.
func (d *Daemon) markDirty() {
	select {
	case d.dirty <- struct{}{}:
	default:
	}
}

func (d *Daemon) writeSnapshots(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-d.dirty:
			if err := WriteSnapshot(d.paths, d.Snapshot()); err != nil {
				d.onError(err)
			}
		}
	}
}

// Snapshot is the combined picture, built from memory alone. Nothing here
// touches the network: spec 03 requires a status read to be answerable while
// the line is down, and a poll that probed would turn an open browser tab into
// continuous authentication traffic.
func (d *Daemon) Snapshot() Snapshot {
	cfg := d.config.Snapshot()
	projection := d.store.Read()

	snapshot := Snapshot{
		Service:        ServiceRunning,
		PID:            os.Getpid(),
		Enabled:        cfg.Enabled,
		ConfigRevision: d.config.Revision(),
		Version:        d.version,
		Accounts:       projection.Accounts,
	}

	ctx, cancel := context.WithTimeout(context.Background(), readBudget)
	defer cancel()
	actions, err := d.actions.Actions(ctx)
	if err != nil {
		// The coordinator has stopped. The rest of the picture is still true.
		return snapshot
	}
	snapshot.Actions = make([]ActionView, 0, len(actions))
	for _, action := range actions {
		snapshot.Actions = append(snapshot.Actions, ViewOf(action))
	}
	return snapshot
}

// UnavailableRunner fails every action, saying why.
//
// This build has the service, the queue and the action state machine but not
// the authentication worker behind them; that is M09. So an action queues,
// runs, and fails with UnsupportedCapability. Reporting success without doing
// anything would be worse than the command not existing at all.
type UnavailableRunner struct{}

func (UnavailableRunner) Run(context.Context, application.Action,
	func(application.Phase)) application.Outcome {
	return application.Outcome{
		State:   application.StateFailed,
		Code:    domain.CodeUnsupportedCapability,
		Message: "本次构建尚未包含认证执行组件，动作无法完成",
	}
}
