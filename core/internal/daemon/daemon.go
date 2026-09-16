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
	"github.com/matthewlu070111/smart-srun/core/internal/presets"
	"github.com/matthewlu070111/smart-srun/core/internal/transport"
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

	// Runner performs actions. Nil means the real one: an authenticator wired
	// to this device's adapter and connection pool. A test supplies its own so
	// that the lifecycle can be exercised without a router.
	Runner application.Runner

	// PublicPresets reads the current merged public catalogue, including drafts.
	// Nil uses the installed built-in file and the tmpfs cache. Reading this
	// never triggers a remote refresh or an authentication request.
	PublicPresets func() ([]presets.School, error)

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

	config        *config.Repository
	store         *observe.Store
	actions       *application.Coordinator
	observer      func(application.Action)
	users         *presets.UserStore
	publicPresets func() ([]presets.School, error)

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

	// One pool for the process, closed when the service stops. Its clients hold
	// sockets bound to particular addresses, and leaving them open past the
	// stop would leave a socket bound to an address the next start may not
	// have.
	pool := transport.NewPool()
	defer pool.Close()

	// The worker is built here rather than by the caller: the CLI has no
	// business knowing which adapter or which pool the service authenticates
	// through, and a test supplies its own through Options.
	runner := options.Runner
	if runner == nil {
		// Written out rather than passed straight through: a nil
		// *deviceWireless in an interface is not a nil interface, and the check
		// for "this build cannot change a radio" is exactly where that
		// distinction would be lost.
		var radio application.Wireless
		device, err := newDeviceWirelessFor(paths, repository, clock)
		if err != nil {
			// Reported, not fatal. A service that refused to start because it
			// could not prepare a wireless staging directory would stop
			// authenticating a wired line for a reason that has nothing to do
			// with it. Without a radio, a switch is refused outright.
			onError(err)
		} else {
			radio = device
			// Before anything is scheduled. A change the last run applied and
			// never confirmed is either still meaningful or has to be undone,
			// and spec 04 will not have that decided while a switch is already
			// running against the same radio.
			if err := device.RecoverInterrupted(ctx); err != nil {
				onError(err)
			}
		}
		runner = newDeviceRunner(repository, pool, clock, radio)
	}

	observer := options.Observer
	if observer == nil {
		observer = func(application.Action) {}
	}

	service := &Daemon{
		paths:         paths,
		version:       options.Version,
		clock:         clock,
		capabilities:  options.Capabilities,
		onError:       onError,
		config:        repository,
		store:         observe.New(),
		observer:      observer,
		users:         presets.NewUserStore(paths.UserPresets()),
		publicPresets: options.PublicPresets,
		dirty:         make(chan struct{}, 1),
	}
	service.store.SetRevision(repository.Revision())

	// The maintenance loop is built before the coordinator and submits through
	// a closure, because each needs the other: the loop submits actions, and
	// the coordinator publishes their results back to it. Resolving
	// service.actions at call time rather than at construction is what breaks
	// the knot without an initialisation order nobody can see.
	maintainer := application.NewMaintainer(application.MaintainerOptions{
		Clock:    clock,
		Settings: repository,
		Submit: func(ctx context.Context, request application.Request) (
			application.Receipt, error) {
			return service.actions.Submit(ctx, request)
		},
		Line:    service.lineOf,
		OnEvent: service.onMaintenanceEvent,
	})

	service.actions = application.New(application.Options{
		Clock:  clock,
		Runner: runner,
		Lines:  service.lineOf,
		Observer: func(action application.Action) {
			service.onAction(action)
			// The loop learns what happened from the same publication the
			// status projection does, rather than polling for it.
			maintainer.Observe(action)
		},
		// The store decides for itself whether an arriving observation is still
		// current -- it holds the revision, generation and sequence rules -- so
		// the answer is handed over rather than filtered first.
		Record: func(observation observe.Observation) {
			service.store.Accept(observation)
		},
	})

	// The coordinator and the snapshot writer outlive the listener on purpose:
	// an RPC that is still being answered when the stop arrives can still read
	// the coordinator, and the last snapshot is written after both have stopped.
	background, stopBackground := context.WithCancel(context.Background())
	defer stopBackground()

	var loops sync.WaitGroup
	var coordinatorErr, maintainerErr error
	loops.Go(func() { coordinatorErr = service.actions.Run(background) })
	loops.Go(func() { maintainerErr = maintainer.Run(background) })
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
	return errors.Join(serveErr, coordinatorErr, maintainerErr)
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
//
// The action's kind is asked first, and the account only afterwards. A hotspot
// switch carries a HotspotID and usually no AccountID at all, so resolving the
// account first sent it to "account:" while a campus wireless switch went to
// "wireless" -- two keys for one radio, which lets the coordinator run both at
// once. Nothing has been observed corrupting a real configuration, because the
// wireless transaction does not exist yet; that is exactly why this has to be
// right before M10 attaches the side effects to it.
//
// The scheduling key is not a substitute for the global wireless transaction
// lock spec 04 requires. It keeps this process from dispatching two wireless
// actions at once; the lock is what protects the radio from everything else.
func (d *Daemon) lineOf(request application.Request) string {
	if wirelessKind(request.Kind) {
		return wirelessLine
	}

	cfg := d.config.Snapshot()
	account, known := cfg.CampusAccountByID(request.AccountID)
	switch {
	case !known:
		// An action for an account that is gone still gets a key of its own, so
		// it cannot serialise against an unrelated one on its way to failing.
		return "account:" + request.AccountID
	case account.IsWired():
		// Wired accounts keep one key per interface. Serialising them onto the
		// wireless key as well would throw away the parallelism that lets four
		// lines authenticate at once, and they share no resource with the radio.
		return "iface:" + account.WiredIface
	default:
		return wirelessLine
	}
}

// wirelessKind reports that an action touches the radio whatever account it
// names.
func wirelessKind(kind application.Kind) bool {
	return kind == application.KindSwitchHotspot
}

// onMaintenanceEvent is where the maintenance loop's narration goes until M11
// gives it a structured log.
//
// Only the line conflict reaches onError, because it is the one event somebody
// has to act on: two accounts resolving to one line will never both be online,
// and the loop has stopped trying rather than letting them knock each other off
// every interval. Backoffs, pauses and queueings are ordinary progress and would
// be noise on a stderr that procd captures.
func (d *Daemon) onMaintenanceEvent(event application.MaintenanceEvent) {
	if event.Kind == application.EventLineConflict {
		d.onError(domain.Errorf(domain.CodeConflict, "%s", event.Message))
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

// UnavailableRunner is gone. It existed because M08 had the service, the queue
// and the action state machine but no authentication worker behind them, so an
// action queued, ran, and failed with UnsupportedCapability -- which was the
// honest answer at the time. M09 supplies the worker, newDeviceRunner builds
// it, and nothing referred to the placeholder any more.
