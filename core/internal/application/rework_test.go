package application

import (
	"net/netip"
	"strings"
	"testing"

	"github.com/matthewlu070111/smart-srun/core/internal/auth"
	"github.com/matthewlu070111/smart-srun/core/internal/domain"
	"github.com/matthewlu070111/smart-srun/core/internal/transport"
)

// These lock the behaviour the independent review of d33528a found missing.

// R02 -- a gateway that refuses the logout has not logged anybody out.
//
// The transport succeeding and the gateway agreeing are different things, and
// reading only the error made the second look like the first: a reply of
// {"error":"sign_error"} came back with a nil error and was reported as 已登出.
func TestARefusedLogoutIsNotReportedAsDone(t *testing.T) {
	p := newPortal(t)
	p.logoutBody = `{"error":"sign_error","error_msg":"refused"}`
	worker, _ := workerFor(t, p, &fakeBinder{})

	outcome := runWorker(t, worker, KindLogout)
	if outcome.State != StateFailed {
		t.Fatalf("outcome = %+v, want a failure", outcome)
	}
	if outcome.Code != domain.CodeAuthRejected {
		t.Errorf("Code = %s, want AuthRejected", outcome.Code)
	}
	// The gateway's own words are quoted, marked as the gateway's.
	if !strings.Contains(outcome.Message, "refused") {
		t.Errorf("the message does not say what the gateway said: %q", outcome.Message)
	}
}

// R02 -- the gateway accepting the unbind is still only the gateway's claim.
//
// Spec 04 does not let that stand for a login and it does not stand for a
// logout either; the baseline asks again too (wait_for_logout_status). A
// logout that never re-checked reported success while the session was still up.
func TestALogoutIsConfirmedAgainstTheLineBeforeItIsReportedDone(t *testing.T) {
	p := newPortal(t)
	p.keepSessionAfterLogout = true
	worker, _ := workerFor(t, p, &fakeBinder{})

	outcome := runWorker(t, worker, KindLogout)
	if outcome.State == StateSucceeded {
		t.Fatalf("a session that is still up was reported as logged out: %+v", outcome)
	}
	if outcome.Code != domain.CodeConflict {
		t.Errorf("Code = %s, want Conflict", outcome.Code)
	}
	// Twice: once to decide whose session it is, once to check it went away.
	if got := p.count(onlinePath); got != 2 {
		t.Errorf("the line was queried %d times, want one before and one after", got)
	}
}

// R02 -- a completed logout does not project this account as online.
//
// The login helper writes VerifiedSelf, which is right for a login and exactly
// wrong here. Sharing it meant a successful logout left the status page saying
// the account was online -- and so did "there was no session here", which
// reported an empty line as a confirmed login.
func TestACompletedLogoutProjectsOfflineRatherThanVerifiedSelf(t *testing.T) {
	for name, build := range map[string]func(*portal){
		"a session that goes away": func(*portal) {},
		"no session to begin with": func(p *portal) { p.onlineBody = offlineAnswer },
	} {
		p := newPortal(t)
		build(p)
		worker, _ := workerFor(t, p, &fakeBinder{})

		outcome := runWorker(t, worker, KindLogout)
		if outcome.State != StateSucceeded {
			t.Errorf("%s: outcome = %+v", name, outcome)
			continue
		}
		if outcome.Observation == nil {
			t.Errorf("%s: reported nothing about the line", name)
			continue
		}
		if outcome.Observation.Auth != domain.AuthOffline {
			t.Errorf("%s: Auth = %v, want Offline", name, outcome.Observation.Auth)
		}
		if outcome.Observation.Identity != "" {
			t.Errorf("%s: a finished logout still names %q as online",
				name, outcome.Observation.Identity)
		}
	}
}

// R02 -- accepted but unconfirmable is neither success nor "still online".
func TestALogoutThatCannotBeConfirmedIsNotCalledDone(t *testing.T) {
	p := newPortal(t)
	worker, _ := workerFor(t, p, &fakeBinder{})

	// The first query names the session; the second one fails.
	p.onlineAfter = func(count int) {
		if count >= 2 {
			p.onlineFails = true
		}
	}

	outcome := runWorker(t, worker, KindLogout)
	if outcome.State == StateSucceeded {
		t.Fatalf("an unconfirmed logout was reported as done: %+v", outcome)
	}
	if outcome.Observation == nil || outcome.Observation.Auth != domain.AuthUnknown {
		t.Errorf("observation = %+v, want Unknown rather than a claim either way",
			outcome.Observation)
	}
	if !strings.Contains(outcome.Message, "无法确认") {
		t.Errorf("the message does not say the result is unconfirmed: %q", outcome.Message)
	}
}

// R05 -- the retry after a clean-up goes through the same checks as the first
// attempt.
//
// It reused the token issued before the unbind and never looked at the line
// again, so a lease that moved during the clean-up sent credentials from an
// address the account no longer had.
func TestTheRetryAfterACleanupRechecksTheLine(t *testing.T) {
	p := newPortal(t)
	p.loginBodies = []string{`{"error":"ip_already_online_error"}`, `{"error":"ok"}`}
	p.onlineBody = offlineAnswer

	moved := steadyBinding()
	moved.SourceIPv4 = netip.MustParseAddr("10.0.0.88")
	// The line is read three times on this path: once to prepare, once before
	// the first login goes out, and once before the retry does. Steady for the
	// first two, moved by the third -- so the retry is the observation that has
	// to notice, and it must stop rather than send.
	binder := &fakeBinder{bindings: []domain.Binding{
		steadyBinding(), steadyBinding(), moved,
	}}
	worker, _ := workerFor(t, p, binder)

	outcome := runWorker(t, worker, KindLogin)
	if got := p.count(portalPath); got != 1 {
		t.Fatalf("sent %d credentialed logins; the retry did not re-check the line", got)
	}
	if outcome.Code != domain.CodeBindingChanged {
		t.Errorf("Code = %s, want BindingChanged", outcome.Code)
	}
}

// R05 -- and it asks for a new challenge rather than replaying the old one.
//
// The unbind is the gateway discarding the session the first token was issued
// against; a token from before it is a token for a state that no longer exists.
func TestTheRetryAfterACleanupAsksForAFreshChallenge(t *testing.T) {
	p := newPortal(t)
	p.loginBodies = []string{`{"error":"ip_already_online_error"}`, `{"error":"ok"}`}
	p.onlineBody = offlineAnswer
	worker, _ := workerFor(t, p, &fakeBinder{})

	runWorker(t, worker, KindLogin)
	logins, challenges := p.count(portalPath), p.count(challengePath)
	if logins != 2 {
		t.Fatalf("sent %d logins, want the original and one retry", logins)
	}
	if challenges != logins {
		t.Errorf("%d logins used %d challenges; a token from before the unbind was replayed",
			logins, challenges)
	}
}

// R05 -- and a clean-up the gateway refused does not lead to another login.
func TestACleanupTheGatewayRefusedDoesNotRetryTheLogin(t *testing.T) {
	p := newPortal(t)
	p.loginBody = `{"error":"ip_already_online_error"}`
	p.onlineBody = `{"error":"ok","user_name":"somebody-else","online_ip":"10.0.0.77"}`
	p.logoutBody = `{"error":"sign_error","error_msg":"refused"}`
	worker, _ := workerFor(t, p, &fakeBinder{})

	outcome := runWorker(t, worker, KindLogin)
	if got := p.count(portalPath); got != 1 {
		t.Fatalf("sent %d logins after a refused clean-up, want 1", got)
	}
	if outcome.State != StateFailed || outcome.Code != domain.CodeConflict {
		t.Errorf("outcome = %+v, want Conflict", outcome)
	}
}

// R09 -- a line that did not move keeps its connection.
//
// A generation is the identity of a binding, not a count of actions. Raising it
// every action retired a pool that was still correct, so each maintenance tick
// paid for a fresh connection and resolution to arrive exactly where it already
// was.
func TestAnUnchangedLineKeepsItsPooledConnection(t *testing.T) {
	p := newPortal(t)
	worker, direct := workerFor(t, p, &fakeBinder{})
	lines := &pooledFixture{direct: direct, pool: transport.NewPool()}
	t.Cleanup(lines.pool.Close)
	worker.lines = lines

	first := runWorker(t, worker, KindLogin)
	second := runWorker(t, worker, KindLogin)
	if first.State != StateSucceeded || second.State != StateSucceeded {
		t.Fatalf("the fixture did not reach two successful logins: %+v / %+v",
			first, second)
	}
	if len(lines.clients) != 2 {
		t.Fatalf("asked for %d clients, want one per action", len(lines.clients))
	}
	if lines.clients[0] != lines.clients[1] {
		t.Errorf("the pool handed out a different client for an unchanged line")
	}
	if lines.closed != 0 {
		t.Errorf("%d client(s) were retired although the line never moved", lines.closed)
	}
	if first.Observation.Generation != second.Observation.Generation {
		t.Errorf("generation moved %d -> %d on an unchanged line",
			first.Observation.Generation, second.Observation.Generation)
	}
}

// R09 -- and a line that did move still retires everything bound to the old
// address, which is the guarantee the churn was standing in for.
func TestALineThatMovesRetiresWhatWasBoundToTheOldAddress(t *testing.T) {
	p := newPortal(t)
	moved := steadyBinding()
	moved.SourceIPv4 = netip.MustParseAddr("10.0.0.88")
	binder := &fakeBinder{bindings: []domain.Binding{
		steadyBinding(), steadyBinding(), moved, moved,
	}}
	worker, direct := workerFor(t, p, binder)
	lines := &pooledFixture{direct: direct, pool: transport.NewPool()}
	t.Cleanup(lines.pool.Close)
	worker.lines = lines

	first := runWorker(t, worker, KindLogin)
	second := runWorker(t, worker, KindLogin)
	if first.Observation.Generation == second.Observation.Generation {
		t.Fatalf("the line moved and the generation did not: %d",
			first.Observation.Generation)
	}
	if lines.closed == 0 {
		t.Error("the client bound to the old address was not retired")
	}
}

// pooledFixture drives the real connection pool so the lifetime decisions under
// test are the pool's own, while the HTTP itself goes to the local portal.
//
// It is not evidence about real socket binding or latency; the binding has its
// own tests in transport, where it can be shown properly.
type pooledFixture struct {
	direct  *fakeLines
	pool    *transport.Pool
	clients []*transport.Client
	closed  int
}

func (l *pooledFixture) Line(accountID string, binding domain.Binding,
	gateway string) (auth.Line, error) {

	client, err := l.pool.Get(accountID, binding, gateway)
	if err != nil {
		return nil, err
	}
	l.clients = append(l.clients, client)
	// The pool decided the lifetime; the request still goes to the fixture.
	return l.direct.Line(accountID, binding, gateway)
}

func (l *pooledFixture) Retire(accountID string, generation uint64) int {
	closed := l.pool.Retire(accountID, generation)
	l.closed += closed
	return closed
}
