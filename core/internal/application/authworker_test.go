package application

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"testing"

	"github.com/matthewlu070111/smart-srun/core/internal/auth"
	"github.com/matthewlu070111/smart-srun/core/internal/domain"
)

// The worker is tested against a portal rather than against a stubbed
// transaction.
//
// What is under test here is the sequence -- when a logout is allowed, when
// credentials may leave, what a failure is reported as -- and a stub that
// returned canned Results would let the sequence be wrong in exactly the ways
// that matter while every assertion still passed. The bytes themselves are
// checked against a frozen oracle in protocol/srun and are not re-verified.
const (
	challengePath = "/cgi-bin/get_challenge"
	portalPath    = "/cgi-bin/srun_portal"
	onlinePath    = "/cgi-bin/rad_user_info"
)

type portal struct {
	server *httptest.Server

	mu       sync.Mutex
	requests []string

	challenge string
	clientIP  string
	// loginBody is what the portal answers a login with. The default is a
	// success; tests that need already-online or a refusal replace it.
	loginBody string
	// loginBodies, when non-empty, is consumed one per login, so a test can say
	// "already online, then fine" without a counter of its own.
	loginBodies []string
	logoutBody  string
	onlineBody  string
	onlineFails bool
}

func newPortal(t *testing.T) *portal {
	t.Helper()
	p := &portal{
		challenge:  "token-abcdef",
		clientIP:   "10.0.0.77",
		loginBody:  `{"error":"ok","suc_msg":"login_ok","client_ip":"10.0.0.77","user_name":"2020123456"}`,
		logoutBody: `{"error":"ok","suc_msg":"logout_ok"}`,
		onlineBody: `{"error":"ok","user_name":"2020123456","online_ip":"10.0.0.77"}`,
	}
	p.server = httptest.NewServer(http.HandlerFunc(p.serve))
	t.Cleanup(p.server.Close)
	return p
}

func (p *portal) serve(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	label := r.URL.Path
	if r.URL.Query().Get("action") == "logout" {
		label += "?logout"
	}
	p.requests = append(p.requests, label)

	var body string
	switch {
	case r.URL.Path == challengePath:
		body = `{"challenge":"` + p.challenge + `","client_ip":"` + p.clientIP + `"}`
	case r.URL.Path == portalPath && r.URL.Query().Get("action") == "logout":
		body = p.logoutBody
	case r.URL.Path == portalPath:
		body = p.loginBody
		if len(p.loginBodies) > 0 {
			body = p.loginBodies[0]
			p.loginBodies = p.loginBodies[1:]
		}
	case r.URL.Path == onlinePath:
		if p.onlineFails {
			p.mu.Unlock()
			http.Error(w, "gateway is unhappy", http.StatusInternalServerError)
			return
		}
		body = p.onlineBody
	default:
		p.mu.Unlock()
		http.NotFound(w, r)
		return
	}
	p.mu.Unlock()

	w.Write([]byte(r.URL.Query().Get("callback") + "(" + body + ")"))
}

func (p *portal) seen() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.requests...)
}

func (p *portal) count(label string) int {
	total := 0
	for _, seen := range p.seen() {
		if seen == label {
			total++
		}
	}
	return total
}

// fakeBinder answers where the line is. Each call may answer differently, which
// is how the "the lease moved mid-attempt" case is produced.
type fakeBinder struct {
	mu        sync.Mutex
	bindings  []domain.Binding
	err       error
	linkState domain.LinkState
	calls     int
}

func steadyBinding() domain.Binding {
	return domain.Binding{
		LogicalIface: "wan",
		L3Device:     "eth0",
		IfIndex:      3,
		SourceIPv4:   netip.MustParseAddr("10.0.0.77"),
		Generation:   1,
	}
}

func (b *fakeBinder) ResolveBinding(_ context.Context, _ string,
	generation uint64) (domain.Binding, error) {

	b.mu.Lock()
	defer b.mu.Unlock()
	b.calls++
	if b.err != nil {
		return domain.Binding{}, b.err
	}
	binding := b.bindings[min(b.calls-1, len(b.bindings)-1)]
	binding.Generation = generation
	return binding, nil
}

func (b *fakeBinder) LinkState(context.Context, string) (domain.LinkState, error) {
	return b.linkState, nil
}

// fakeLines hands out a line that talks straight to the test portal.
type fakeLines struct {
	client  *http.Client
	source  netip.Addr
	mu      sync.Mutex
	retired []uint64
	err     error
}

type directLine struct {
	client *http.Client
	source netip.Addr
}

func (l directLine) Do(req *http.Request) (*http.Response, error) {
	return l.client.Do(req)
}

func (l directLine) SourceAddr() netip.Addr { return l.source }

func (f *fakeLines) Line(string, domain.Binding, string) (auth.Line, error) {
	if f.err != nil {
		return nil, f.err
	}
	return directLine{client: f.client, source: f.source}, nil
}

func (f *fakeLines) Retire(_ string, generation uint64) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.retired = append(f.retired, generation)
	return 0
}

func (f *fakeLines) retirements() []uint64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]uint64(nil), f.retired...)
}

type fakeSettings struct {
	cfg      domain.Config
	revision uint64
}

func (s *fakeSettings) Snapshot() domain.Config { return s.cfg }
func (s *fakeSettings) Revision() uint64        { return s.revision }

// workerFor wires an Authenticator to a portal with a wired campus account.
func workerFor(t *testing.T, p *portal, binder *fakeBinder) (*Authenticator, *fakeLines) {
	t.Helper()
	if binder.bindings == nil {
		binder.bindings = []domain.Binding{steadyBinding()}
	}
	lines := &fakeLines{
		client: p.server.Client(),
		source: netip.MustParseAddr("10.0.0.77"),
	}
	settings := &fakeSettings{
		revision: 7,
		cfg: domain.Config{
			CampusAccounts: []domain.CampusAccount{{
				ID:         "c1",
				Label:      "校园网",
				UserID:     "2020123456",
				Password:   "hunter2",
				AccessMode: domain.AccessModeWired,
				WiredIface: "wan",
				BaseURL:    p.server.URL,
				ACID:       "12",
			}},
		},
	}
	return NewAuthenticator(binder, lines, settings, nil), lines
}

func runWorker(t *testing.T, worker *Authenticator, kind Kind) Outcome {
	t.Helper()
	action := Action{
		ID:       "a1",
		Request:  Request{Kind: kind, AccountID: "c1", IdempotencyKey: "k1"},
		Sequence: 5,
	}
	return worker.Run(t.Context(), action, func(Phase) {})
}

// A login that the gateway accepted is reported only after the line is asked
// whose session is actually on it.
func TestASuccessfulLoginIsVerifiedBeforeItIsReported(t *testing.T) {
	p := newPortal(t)
	worker, _ := workerFor(t, p, &fakeBinder{})

	outcome := runWorker(t, worker, KindLogin)
	if outcome.State != StateSucceeded {
		t.Fatalf("outcome = %+v", outcome)
	}
	if p.count(onlinePath) == 0 {
		t.Error("success was reported without asking who is online")
	}
	if outcome.Observation == nil {
		t.Fatal("a finished attempt reported nothing about the line")
	}
	got := *outcome.Observation
	if got.Auth != domain.AuthVerifiedSelf {
		t.Errorf("Auth = %v, want VerifiedSelf", got.Auth)
	}
	if got.AccountID != "c1" || got.Revision != 7 || got.Sequence != 5 {
		t.Errorf("observation is not stamped with what produced it: %+v", got)
	}
	if got.Generation == 0 {
		t.Error("the observation carries no binding generation")
	}
}

// The gateway saying ok is the gateway's claim about itself.
//
// When the online query then shows nobody at this address, reporting success
// would be reporting that claim as a fact -- and the user would see "connected"
// on a line that is not.
func TestAGatewayThatAcceptsButShowsNoSessionIsNotSuccess(t *testing.T) {
	p := newPortal(t)
	p.onlineBody = `{"error":"not_online_error"}`
	worker, _ := workerFor(t, p, &fakeBinder{})

	outcome := runWorker(t, worker, KindLogin)
	if outcome.State != StateFailed {
		t.Fatalf("outcome = %+v, want a failure", outcome)
	}
	if outcome.Observation == nil || outcome.Observation.Auth == domain.AuthVerifiedSelf {
		t.Errorf("observation claims a verified session: %+v", outcome.Observation)
	}
}

// Spec 04, T05, the automatic half: maintenance reports another identity and
// does not end it.
//
// The session on this line may be a housemate's. A daemon that logged them out
// because it wanted the line is a daemon that fights other people's devices
// every few minutes, and nobody would be able to tell what was doing it.
func TestAutomaticMaintenanceReportsAnotherIdentityAndDoesNotEndIt(t *testing.T) {
	p := newPortal(t)
	p.loginBody = `{"error":"ip_already_online_error"}`
	p.onlineBody = `{"error":"ok","user_name":"somebody-else","online_ip":"10.0.0.77"}`
	worker, _ := workerFor(t, p, &fakeBinder{})

	outcome := runWorker(t, worker, KindMaintain)
	if outcome.State != StateFailed {
		t.Fatalf("outcome = %+v, want a refusal", outcome)
	}
	if outcome.Code != domain.CodeOnlineIdentityMismatch {
		t.Errorf("Code = %s, want OnlineIdentityMismatch", outcome.Code)
	}
	if n := p.count(portalPath + "?logout"); n != 0 {
		t.Fatalf("automatic maintenance sent %d logout(s)", n)
	}
	if outcome.Observation == nil || outcome.Observation.Identity != "somebody-else" {
		t.Errorf("the other identity was not reported: %+v", outcome.Observation)
	}
}

// Spec 04, T05, the manual half: an explicit login may clear this line's
// session, once.
//
// The same portal answers as the test above. Only the intent differs, which is
// the whole point -- these two must not be collapsed into one rule.
func TestAnExplicitLoginClearsThisLinesSessionExactlyOnce(t *testing.T) {
	p := newPortal(t)
	p.loginBodies = []string{
		`{"error":"ip_already_online_error"}`,
		`{"error":"ok","suc_msg":"login_ok","user_name":"2020123456"}`,
	}
	p.onlineBody = `{"error":"ok","user_name":"somebody-else","online_ip":"10.0.0.77"}`
	worker, _ := workerFor(t, p, &fakeBinder{})

	outcome := runWorker(t, worker, KindLogin)
	if n := p.count(portalPath + "?logout"); n != 1 {
		t.Fatalf("an explicit login sent %d logouts, want exactly 1", n)
	}
	// It logged in again after clearing; whether that second login verifies is
	// the online query's business and is asserted elsewhere.
	if n := p.count(portalPath); n != 2 {
		t.Errorf("sent %d logins, want the original and one retry", n)
	}
	if outcome.State == StateQueued || outcome.State == StateRunning {
		t.Errorf("worker returned a non-terminal state: %+v", outcome)
	}
}

// And clearing really is once. A gateway that still says the line is taken is
// not answered with another logout.
//
// A loop here is a program that logs somebody out every time it is unsure,
// which on a shared address is indistinguishable from an attack.
func TestClearingIsNotRetriedWhenTheGatewayStillRefuses(t *testing.T) {
	p := newPortal(t)
	p.loginBody = `{"error":"ip_already_online_error"}`
	p.onlineBody = `{"error":"ok","user_name":"somebody-else","online_ip":"10.0.0.77"}`
	worker, _ := workerFor(t, p, &fakeBinder{})

	outcome := runWorker(t, worker, KindLogin)
	if n := p.count(portalPath + "?logout"); n != 1 {
		t.Fatalf("sent %d logouts, want exactly 1 even though it did not help", n)
	}
	if outcome.State != StateFailed || outcome.Code != domain.CodeConflict {
		t.Errorf("outcome = %+v, want a Conflict", outcome)
	}
}

// Credentials do not go out on a line that moved after the challenge.
//
// The token was issued for an address this account no longer has, so sending
// the login anyway spends an attempt and teaches the user nothing. Spec 04 asks
// for the re-check specifically.
func TestCredentialsAreNotSentWhenTheLineMovedAfterTheChallenge(t *testing.T) {
	moved := steadyBinding()
	moved.SourceIPv4 = netip.MustParseAddr("10.0.0.99")

	p := newPortal(t)
	worker, _ := workerFor(t, p, &fakeBinder{
		bindings: []domain.Binding{steadyBinding(), moved},
	})

	outcome := runWorker(t, worker, KindLogin)
	if outcome.State != StateFailed || outcome.Code != domain.CodeBindingChanged {
		t.Fatalf("outcome = %+v, want BindingChanged", outcome)
	}
	if p.count(challengePath) != 1 {
		t.Errorf("challenge requests = %d", p.count(challengePath))
	}
	if n := p.count(portalPath); n != 0 {
		t.Fatalf("the password was sent %d time(s) on a line that had moved", n)
	}
}

// Older generations are retired before anything is sent.
//
// A socket bound to an address this account no longer has is a socket a reply
// can still arrive on, and crediting that reply to the current attempt is how a
// stale answer becomes the status a user reads.
func TestOlderGenerationsAreRetiredBeforeCredentialsAreSent(t *testing.T) {
	p := newPortal(t)
	worker, lines := workerFor(t, p, &fakeBinder{})

	runWorker(t, worker, KindLogin)
	retired := lines.retirements()
	if len(retired) == 0 {
		t.Fatal("nothing was retired before the attempt")
	}
	if retired[0] == 0 {
		t.Errorf("retired generation %d, want the one just resolved", retired[0])
	}
}

// A line that is not up is explained by the adapter, not by this program's
// vocabulary.
//
// "BindingUnavailable" is a word from inside this codebase. "The cable is
// unplugged" is the one that gets the problem fixed, and only the adapter can
// tell that apart from a missing interface or DHCP still in flight.
func TestALineThatIsNotUpIsExplainedByTheAdapter(t *testing.T) {
	p := newPortal(t)
	binder := &fakeBinder{
		err:       domain.Errorf(domain.CodeBindingUnavailable, "接口尚未就绪"),
		linkState: domain.LinkDown,
	}
	worker, _ := workerFor(t, p, binder)

	outcome := runWorker(t, worker, KindLogin)
	if outcome.State != StateFailed {
		t.Fatalf("outcome = %+v", outcome)
	}
	if outcome.Observation == nil {
		t.Fatal("a link failure reported nothing about the line")
	}
	if outcome.Observation.Link != domain.LinkDown {
		t.Errorf("Link = %v, want what the adapter reported", outcome.Observation.Link)
	}
	if len(p.seen()) != 0 {
		t.Errorf("a request was sent on a line that had no binding: %v", p.seen())
	}
}

// The message a user sees is this program's own text, never the error's.
//
// An error from outside this codebase carries whatever its author put in it,
// and down this path that is routinely a URL -- a login URL carries a checksum
// over the password. This string ends up on the status page and in the log, so
// an unclassified error contributes its existence and nothing else.
func TestAnUnclassifiedErrorsOwnTextNeverReachesTheUser(t *testing.T) {
	p := newPortal(t)
	secret := "https://portal.example/srun_portal?password=%7BMD5%7Ddeadbeef"
	worker, _ := workerFor(t, p, &fakeBinder{err: &urlish{text: secret}})

	outcome := runWorker(t, worker, KindLogin)
	if strings.Contains(outcome.Message, "password") ||
		strings.Contains(outcome.Message, "deadbeef") ||
		strings.Contains(outcome.Message, "portal.example") {
		t.Fatalf("the error's own text reached the user message: %q", outcome.Message)
	}
	if outcome.Message == "" {
		t.Error("a failure produced no message at all")
	}
}

// And a classified error contributes its message, not its rendering.
//
// Error() is "Code: message", which is the form a log line wants and not the
// form a person reading a status card does. Showing it would put InvalidConfig
// and TransportFailure on the screen next to Chinese prose.
func TestAClassifiedErrorContributesItsMessageWithoutTheCode(t *testing.T) {
	p := newPortal(t)
	cause := domain.Errorf(domain.CodeTransportFailure, "线路不可用")
	worker, _ := workerFor(t, p, &fakeBinder{err: cause})

	outcome := runWorker(t, worker, KindLogin)
	if outcome.Message != "线路不可用" {
		t.Errorf("Message = %q, want just the message", outcome.Message)
	}
	if strings.Contains(outcome.Message, string(domain.CodeTransportFailure)) {
		t.Errorf("the error code is being shown to a person: %q", outcome.Message)
	}
	// The code still travels; it is just not part of the sentence.
	if outcome.Code != domain.CodeTransportFailure {
		t.Errorf("Code = %s, want it preserved separately", outcome.Code)
	}
}

type urlish struct{ text string }

func (u *urlish) Error() string { return u.text }

// An account that is not in the configuration is NotFound, not a crash.
func TestAnUnknownAccountIsNotFound(t *testing.T) {
	p := newPortal(t)
	worker, _ := workerFor(t, p, &fakeBinder{})

	outcome := worker.Run(t.Context(), Action{
		ID:      "a2",
		Request: Request{Kind: KindLogin, AccountID: "gone", IdempotencyKey: "k2"},
	}, func(Phase) {})

	if outcome.State != StateFailed || outcome.Code != domain.CodeNotFound {
		t.Fatalf("outcome = %+v, want NotFound", outcome)
	}
}

// A failed online query is not proof of being offline.
//
// Treating "the gateway did not answer" as "nobody is logged in" would make a
// manual logout report success for a session that is still up.
func TestAFailedOnlineQueryIsNotProofOfBeingOffline(t *testing.T) {
	p := newPortal(t)
	p.onlineFails = true
	worker, _ := workerFor(t, p, &fakeBinder{})

	outcome := runWorker(t, worker, KindLogout)
	if outcome.State != StateFailed {
		t.Fatalf("outcome = %+v, want a failure rather than a claim of success",
			outcome)
	}
	if n := p.count(portalPath + "?logout"); n != 0 {
		t.Errorf("a logout was sent on the strength of a failed query")
	}
}

// A manual logout acts on the identity this line just reported.
func TestAManualLogoutActsOnTheIdentityThisLineReports(t *testing.T) {
	p := newPortal(t)
	worker, _ := workerFor(t, p, &fakeBinder{})

	outcome := runWorker(t, worker, KindLogout)
	if outcome.State != StateSucceeded {
		t.Fatalf("outcome = %+v", outcome)
	}
	if n := p.count(portalPath + "?logout"); n != 1 {
		t.Errorf("sent %d logouts, want 1", n)
	}
}

// Nothing on this line, and a logout says so instead of sending a request.
func TestLoggingOutWhenNobodyIsOnlineSaysSo(t *testing.T) {
	p := newPortal(t)
	p.onlineBody = `{"error":"not_online_error"}`
	worker, _ := workerFor(t, p, &fakeBinder{})

	outcome := runWorker(t, worker, KindLogout)
	if outcome.State != StateSucceeded {
		t.Fatalf("outcome = %+v", outcome)
	}
	if n := p.count(portalPath + "?logout"); n != 0 {
		t.Errorf("sent %d logouts for a line with no session", n)
	}
}
