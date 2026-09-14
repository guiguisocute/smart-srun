package application

import (
	"context"
	"errors"
	"sync/atomic"

	"github.com/matthewlu070111/smart-srun/core/internal/auth"
	"github.com/matthewlu070111/smart-srun/core/internal/config"
	"github.com/matthewlu070111/smart-srun/core/internal/domain"
	"github.com/matthewlu070111/smart-srun/core/internal/observe"
	"github.com/matthewlu070111/smart-srun/core/internal/policy"
)

// Binder answers where a line is and whether it can carry traffic.
//
// Consumer-defined, as spec 02 requires: implementing it needs a router, and
// this package has to be testable without one. The adapter that does implement
// it reads the interface; deciding which interface to read is this layer's job,
// and the dependency points that way round so that neither can drift into the
// other's.
type Binder interface {
	// ResolveBinding produces one observation of how a line reaches the
	// network. The generation is supplied by the caller, so a reply that
	// arrives from a line that no longer exists can be recognised.
	ResolveBinding(ctx context.Context, logicalIface string,
		generation uint64) (domain.Binding, error)
	// LinkState is what to tell a user when the binding could not be made.
	// "BindingUnavailable" is this program's word; "the cable is unplugged" is
	// the one that gets the problem fixed, and only the adapter can tell the
	// difference between that, a missing interface and DHCP still in flight.
	LinkState(ctx context.Context, logicalIface string) (domain.LinkState, error)
}

// Lines hands out the bound transport an attempt sends over.
//
// It returns auth.Line -- two methods -- rather than a transport client,
// because that is all an attempt needs and because a test can then supply one
// without opening a socket. The connection pool behind it is assembled in the
// daemon's wiring, which is the only place that knows both halves.
type Lines interface {
	Line(accountID string, binding domain.Binding, gateway string) (auth.Line, error)
	// Retire closes the clients belonging to generations older than the
	// current one, so a reply cannot arrive over a socket bound to an address
	// this account no longer has.
	Retire(accountID string, currentGeneration uint64) int
}

// Settings is the configuration an attempt reads, once, at its start.
//
// Once: spec 03 resolves an account into an immutable effective form, and an
// attempt that re-read the configuration halfway through could send a password
// from before a save with a username from after it.
type Settings interface {
	Snapshot() domain.Config
	Revision() uint64
}

// Authenticator performs the actions the coordinator schedules.
//
// It is the Runner the coordinator was built around, and it holds no state
// between actions except the binding generation counter -- which only ever goes
// up, so two observations can always be ordered.
type Authenticator struct {
	binder   Binder
	lines    Lines
	settings Settings
	wireless Wireless
	clock    policy.Clock

	generation atomic.Uint64
}

// AuthenticatorOptions wires one worker.
//
// A struct rather than five positional parameters, and Wireless is allowed to
// be nil: a build without the wireless transaction still authenticates over a
// client somebody configured by hand, it just cannot move the radio itself.
type AuthenticatorOptions struct {
	Binder   Binder
	Lines    Lines
	Settings Settings
	// Wireless moves the managed client. Nil until M10 supplies the
	// transaction, and a switch is refused rather than half-performed while it
	// is nil.
	Wireless Wireless
	Clock    policy.Clock
}

// NewAuthenticator wires one.
func NewAuthenticator(options AuthenticatorOptions) *Authenticator {
	clock := options.Clock
	if clock == nil {
		clock = policy.SystemClock{}
	}
	return &Authenticator{
		binder:   options.Binder,
		lines:    options.Lines,
		settings: options.Settings,
		wireless: options.Wireless,
		clock:    clock,
	}
}

// Run performs one action.
func (a *Authenticator) Run(ctx context.Context, action Action,
	report func(Phase)) Outcome {
	switch action.Request.Kind {
	case KindLogin, KindRelogin, KindMaintain, KindSwitchCampus:
		return a.authenticate(ctx, action, report)
	case KindLogout, KindForcedLogout:
		return a.logout(ctx, action, report)
	case KindSwitchHotspot:
		return a.switchHotspot(ctx, action, report)
	default:
		return Outcome{State: StateFailed, Code: domain.CodeUnsupportedCapability,
			Message: "动作 " + string(action.Request.Kind) + " 尚未实现"}
	}
}

// attempt is everything one action needs, resolved once at its start.
type attempt struct {
	account  domain.CampusAccount
	username string
	shape    auth.Shape
	intent   auth.Intent
	revision uint64
	binding  domain.Binding
	line     auth.Line
	gateway  auth.Gateway
	sequence uint64
}

// authenticate is the login path: challenge, login, and then ask whose session
// is actually on the line.
func (a *Authenticator) authenticate(ctx context.Context, action Action,
	report func(Phase)) Outcome {

	// The radio has to be on the right network before there is a line to
	// authenticate over. This is a no-op for a wired account, for a build with
	// no wireless transaction, and -- the case that matters on every
	// maintenance tick -- for a client that is already associated with an
	// address, which is checked without scanning.
	if outcome, stop := a.ensureWirelessLine(ctx, action, report); stop {
		return outcome
	}

	prepared, outcome := a.prepare(ctx, action, report)
	if prepared == nil {
		return outcome
	}
	transaction := auth.NewTransaction(prepared.line, prepared.gateway)

	report(PhaseChallenge)
	challenge, err := transaction.Challenge(ctx, prepared.username)
	if err != nil {
		return prepared.failed(a, err, domain.AuthUnknown, "")
	}

	// Spec 04: the credentials are re-checked against the line before they go
	// out. A DHCP lease that changes between the challenge and the login means
	// the token was issued for an address this account no longer has, and
	// sending it anyway spends an attempt and teaches the user nothing.
	if err := a.confirmUnchanged(ctx, prepared); err != nil {
		return prepared.failed(a, err, domain.AuthUnknown, "")
	}

	report(PhaseLogin)
	result, err := transaction.Login(ctx,
		auth.Credentials{Username: prepared.username,
			Password: prepared.account.Password},
		prepared.shape, challenge)
	if err != nil {
		return prepared.failed(a, err, domain.AuthAuthenticating, "")
	}

	if result.AlreadyOnline {
		return a.settleAlreadyOnline(ctx, transaction, prepared, challenge, report)
	}
	if result.State == domain.AuthRejected {
		// A refusal is an answer, and retrying it in a loop is how an account
		// gets locked out. The coordinator sees a failure it must not repeat.
		return prepared.failed(a,
			domain.Errorf(domain.CodeAuthRejected, "网关拒绝了本次登录"),
			domain.AuthRejected, result.Identity)
	}

	return a.verify(ctx, transaction, prepared, report)
}

// verify turns the gateway's acceptance into a checked identity.
//
// Spec 04 will not let "the gateway said ok" stand as "this account is online":
// that is the gateway's claim about itself, and the session it is talking about
// may belong to somebody else.
func (a *Authenticator) verify(ctx context.Context, transaction *auth.Transaction,
	prepared *attempt, report func(Phase)) Outcome {

	report(PhaseVerify)
	identity, err := transaction.Online(ctx, prepared.username)
	if err != nil {
		// Accepted but unverifiable. Reporting success here would be reporting
		// the gateway's word for it.
		return prepared.failed(a, err, domain.AuthAccepted, "")
	}

	switch identity.State() {
	case domain.AuthVerifiedSelf:
		return prepared.succeeded(a, "认证完成", identity.Username)
	case domain.AuthVerifiedOther:
		return prepared.failed(a,
			domain.Errorf(domain.CodeOnlineIdentityMismatch,
				"这条线路上在线的是另一个账号"),
			domain.AuthVerifiedOther, identity.Username)
	default:
		return prepared.failed(a,
			domain.Errorf(domain.CodeAuthRejected,
				"网关接受了登录，但这条线路上查不到任何在线会话"),
			domain.AuthAccepted, "")
	}
}

// settleAlreadyOnline handles the gateway's "this line already has a session".
//
// It is neither success nor failure until somebody asks whose session it is,
// and the baseline learned the two cases the hard way:
//
//   - the session is on this address and is ours. A router that rebooted and
//     got the same lease back is already in the state the user wants; calling
//     it a failure makes the daemon retry with backoff forever.
//   - the session is on an address this account no longer has, after a reboot
//     or a reconnect changed the lease. Then the old session has to be unbound
//     before a new login can take.
//
// The clean-up happens once. Doing it in a loop would be a program that logs
// somebody out every time it is unsure.
func (a *Authenticator) settleAlreadyOnline(ctx context.Context,
	transaction *auth.Transaction, prepared *attempt, challenge auth.Challenge,
	report func(Phase)) Outcome {

	report(PhaseVerify)
	identity, err := transaction.Online(ctx, prepared.username)
	if err != nil {
		return prepared.failed(a, err, domain.AuthAccepted, "")
	}

	switch identity.State() {
	case domain.AuthVerifiedSelf:
		return prepared.succeeded(a, "本线路已是该账号的在线会话", identity.Username)

	case domain.AuthVerifiedOther:
		if prepared.intent != auth.IntentManual {
			// Spec 04: automatic maintenance reports another identity and never
			// ends it. The account on this line may be a housemate's.
			return prepared.failed(a,
				domain.Errorf(domain.CodeOnlineIdentityMismatch,
					"这条线路上在线的是另一个账号，自动维护不会将其下线"),
				domain.AuthVerifiedOther, identity.Username)
		}
		// An explicit request may clear this line's session -- and it clears
		// the identity the query just returned, not a name somebody passed in.
		return a.clearAndRetry(ctx, transaction, prepared, challenge,
			identity.Username, report)

	default:
		// Nobody is online at this address, so the session the gateway is
		// refusing over is on the one before the lease changed.
		return a.clearAndRetry(ctx, transaction, prepared, challenge,
			prepared.username, report)
	}
}

// clearAndRetry unbinds one session and logs in once more.
func (a *Authenticator) clearAndRetry(ctx context.Context,
	transaction *auth.Transaction, prepared *attempt, challenge auth.Challenge,
	username string, report func(Phase)) Outcome {

	report(PhaseLogout)
	if _, err := transaction.Logout(ctx, username, ""); err != nil {
		return prepared.failed(a, err, domain.AuthVerifiedOther, username)
	}

	report(PhaseLogin)
	result, err := transaction.Login(ctx,
		auth.Credentials{Username: prepared.username,
			Password: prepared.account.Password},
		prepared.shape, challenge)
	if err != nil {
		return prepared.failed(a, err, domain.AuthAuthenticating, "")
	}
	if result.AlreadyOnline {
		// Cleared once and the gateway still says the line is taken. Spec 04
		// stops here: the wireless rebuild is the next remedy and it belongs
		// to the wireless path, not to another round of logging people out.
		return prepared.failed(a,
			domain.Errorf(domain.CodeConflict,
				"清理旧会话后网关仍报该线路已有会话"),
			domain.AuthAccepted, username)
	}
	if result.State == domain.AuthRejected {
		return prepared.failed(a,
			domain.Errorf(domain.CodeAuthRejected, "网关拒绝了本次登录"),
			domain.AuthRejected, result.Identity)
	}
	return a.verify(ctx, transaction, prepared, report)
}

// logout ends this account's own session on its own line.
func (a *Authenticator) logout(ctx context.Context, action Action,
	report func(Phase)) Outcome {

	prepared, outcome := a.prepare(ctx, action, report)
	if prepared == nil {
		return outcome
	}
	transaction := auth.NewTransaction(prepared.line, prepared.gateway)

	// Whose session to end is settled by asking this line, not by trusting the
	// configured name: spec 04 requires a manual logout to act on the identity
	// this line just reported, and a failed query is not proof of being
	// offline.
	report(PhaseVerify)
	identity, err := transaction.Online(ctx, prepared.username)
	if err != nil {
		return prepared.failed(a, err, domain.AuthUnknown, "")
	}
	if !identity.Present {
		return prepared.succeeded(a, "这条线路上没有在线会话", "")
	}
	if !identity.MatchesExpected && prepared.intent != auth.IntentManual {
		return prepared.failed(a,
			domain.Errorf(domain.CodeOnlineIdentityMismatch,
				"这条线路上在线的是另一个账号，自动维护不会将其下线"),
			domain.AuthVerifiedOther, identity.Username)
	}

	report(PhaseLogout)
	if _, err := transaction.Logout(ctx, identity.Username, identity.ClientIP); err != nil {
		return prepared.failed(a, err, identity.State(), identity.Username)
	}
	return prepared.succeeded(a, "已登出", identity.Username)
}

// prepare resolves everything an attempt needs, or explains why it cannot.
//
// A nil attempt means the Outcome beside it is the answer.
func (a *Authenticator) prepare(ctx context.Context, action Action,
	report func(Phase)) (*attempt, Outcome) {

	cfg := a.settings.Snapshot()
	revision := a.settings.Revision()
	accountID := action.Request.AccountID

	account, known := cfg.CampusAccountByID(accountID)
	if !known {
		return nil, Outcome{State: StateFailed, Code: domain.CodeNotFound,
			Message: "账号 " + accountID + " 不存在"}
	}

	prepared := &attempt{
		account:  account,
		username: config.EffectiveUsername(account),
		shape:    shapeOf(config.EffectiveLogin(cfg, account)),
		intent:   intentOf(action.Request.Kind),
		revision: revision,
		sequence: action.Sequence,
	}

	iface, err := lineInterface(cfg, account)
	if err != nil {
		return nil, a.linkFailure(ctx, prepared, "", err)
	}

	report(PhaseWaitingLink)
	generation := a.generation.Add(1)
	binding, err := a.binder.ResolveBinding(ctx, iface, generation)
	if err != nil {
		return nil, a.linkFailure(ctx, prepared, iface, err)
	}
	prepared.binding = binding

	// Anything older than this observation is gone: its socket is bound to an
	// address this account may no longer have.
	a.lines.Retire(account.ID, generation)

	gateway, err := auth.ParseGateway(account.BaseURL, account.ACID)
	if err != nil {
		return nil, prepared.failed(a, err, domain.AuthUnknown, "")
	}
	prepared.gateway = gateway

	line, err := a.lines.Line(account.ID, binding, gateway.BaseURL)
	if err != nil {
		return nil, prepared.failed(a, err, domain.AuthUnknown, "")
	}
	prepared.line = line
	return prepared, Outcome{}
}

// confirmUnchanged re-reads the line and refuses if it moved.
//
// The same generation goes in deliberately: what is being compared is what the
// interface actually looks like now against what it looked like when the token
// was issued, not two numbers this program made up.
func (a *Authenticator) confirmUnchanged(ctx context.Context, prepared *attempt) error {
	cfg := a.settings.Snapshot()
	iface, err := lineInterface(cfg, prepared.account)
	if err != nil {
		return err
	}
	now, err := a.binder.ResolveBinding(ctx, iface, prepared.binding.Generation)
	if err != nil {
		return err
	}
	if now.L3Device != prepared.binding.L3Device ||
		now.IfIndex != prepared.binding.IfIndex ||
		now.SourceIPv4 != prepared.binding.SourceIPv4 {
		return domain.Errorf(domain.CodeBindingChanged,
			"线路在取得挑战值之后发生变化，本次认证作废")
	}
	return nil
}

// linkFailure asks the adapter what is actually wrong with the line.
//
// "BindingUnavailable" is this program's word for it. "The interface does not
// exist" and "DHCP has not answered yet" are the words that get the problem
// fixed, and only the adapter can tell those apart.
func (a *Authenticator) linkFailure(ctx context.Context, prepared *attempt,
	iface string, cause error) Outcome {

	state := domain.LinkMissing
	if iface != "" {
		if observed, err := a.binder.LinkState(ctx, iface); err == nil {
			state = observed
		}
	}
	outcome := prepared.failed(a, cause, domain.AuthUnknown, "")
	if outcome.Observation != nil {
		outcome.Observation.Link = state
		outcome.Observation.Connectivity = domain.ConnectivityUnknown
	}
	return outcome
}

// succeeded and failed build the Outcome together with what the attempt learned.
//
// The observation travels back with the result rather than being written here:
// spec 02 gives the coordinator the job of confirming a worker's answer is
// still current before anything accepts it, and a worker that wrote global
// state directly would be deciding that for itself.
func (p *attempt) succeeded(a *Authenticator, message, identity string) Outcome {
	return Outcome{
		State:   StateSucceeded,
		Message: message,
		Observation: p.observation(a, domain.AuthVerifiedSelf,
			domain.ConnectivityPortalReachable, identity),
	}
}

func (p *attempt) failed(a *Authenticator, cause error, state domain.AuthState,
	identity string) Outcome {

	code := domain.CodeInternal
	if observed, ok := domain.CodeOf(cause); ok {
		code = observed
	}
	connectivity := domain.ConnectivityUnknown
	if state != domain.AuthUnknown {
		// The gateway answered something, whatever it was. That is what
		// PortalReachable means; whether the internet is reachable is a
		// different question and nothing here has asked it.
		connectivity = domain.ConnectivityPortalReachable
	}
	return Outcome{
		State:       StateFailed,
		Code:        code,
		Message:     userMessage(cause),
		Observation: p.observation(a, state, connectivity, identity),
	}
}

// userMessage is the part of a failure a person is shown.
//
// Only this program's own text, never the wrapped cause. domain.Error keeps the
// cause out of Error() for exactly this reason: a transport failure can carry a
// URL, and a login URL carries a checksum over the password. This string ends
// up on the status page.
func userMessage(cause error) string {
	if typed, ok := errors.AsType[*domain.Error](cause); ok {
		return typed.Message
	}
	return "认证过程出现未分类的错误"
}

func (p *attempt) observation(a *Authenticator, state domain.AuthState,
	connectivity domain.Connectivity, identity string) *observe.Observation {

	link := domain.LinkReady
	if !p.binding.Ready() {
		link = domain.LinkMissing
	}
	return &observe.Observation{
		AccountID:    p.account.ID,
		Revision:     p.revision,
		Generation:   p.binding.Generation,
		Sequence:     p.sequence,
		Link:         link,
		Auth:         state,
		Connectivity: connectivity,
		Identity:     identity,
		At:           a.clock.Now(),
	}
}

// lineInterface is the interface an account authenticates through.
func lineInterface(cfg domain.Config, account domain.CampusAccount) (string, error) {
	if account.IsWired() {
		if account.WiredIface == "" {
			return "", domain.FieldErrorf(domain.CodeInvalidConfig,
				"wired_iface", "该账号没有选择有线接口")
		}
		return account.WiredIface, nil
	}
	if cfg.STAIface == "" {
		return "", domain.FieldErrorf(domain.CodeInvalidConfig, "sta_iface",
			"无线账号需要先指定客户端接口，否则无法确定认证走哪条线路")
	}
	return cfg.STAIface, nil
}

// intentOf is spec 04's distinction between maintenance and a user asking now.
//
// It decides one thing and it decides it here, once: whether this action is
// allowed to end a session it did not create.
func intentOf(kind Kind) auth.Intent {
	if kind.Manual() {
		return auth.IntentManual
	}
	return auth.IntentAutomatic
}

func shapeOf(login config.EffectiveLoginShape) auth.Shape {
	return auth.Shape{
		N:           login.N,
		Type:        login.Type,
		Enc:         login.Enc,
		InfoPrefix:  login.InfoPrefix,
		OS:          login.OS,
		Name:        login.Name,
		DoubleStack: login.DoubleStack,
	}
}
