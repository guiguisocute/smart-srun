package daemon

import (
	"context"

	"github.com/matthewlu070111/smart-srun/core/internal/application"
	"github.com/matthewlu070111/smart-srun/core/internal/auth"
	"github.com/matthewlu070111/smart-srun/core/internal/domain"
	"github.com/matthewlu070111/smart-srun/core/internal/openwrt"
	"github.com/matthewlu070111/smart-srun/core/internal/policy"
	"github.com/matthewlu070111/smart-srun/core/internal/transport"
)

// This file is the assembly the rest of the program is arranged to avoid doing.
//
// application declares what it needs -- a Binder, Lines, Settings -- and cannot
// reach the adapter or the transport that satisfy them. That is what lets it be
// tested without a router. Somebody still has to put the two halves together,
// and this is the one place allowed to know about both.

// deviceBinder answers where a line is by asking the router.
type deviceBinder struct{ adapter *openwrt.Adapter }

func (b deviceBinder) ResolveBinding(ctx context.Context, iface string,
	generation uint64) (domain.Binding, error) {

	return b.adapter.ResolveBinding(ctx, iface, generation)
}

// LinkState replaces this program's word for a failure with the router's.
//
// "BindingUnavailable" is accurate and useless. Whether the interface is
// missing, the cable is out, or DHCP has not answered yet are three different
// things for the person who has to fix it, and only netifd can tell them apart.
func (b deviceBinder) LinkState(ctx context.Context, iface string) (domain.LinkState, error) {
	status, err := b.adapter.InterfaceStatus(ctx, iface)
	if err != nil {
		return domain.LinkMissing, err
	}
	return status.LinkState(), nil
}

// pooledLines hands out bound clients from the one connection pool.
type pooledLines struct{ pool *transport.Pool }

func (l pooledLines) Line(accountID string, binding domain.Binding,
	gateway string) (auth.Line, error) {

	client, err := l.pool.Get(accountID, binding, gateway)
	if err != nil {
		// Returned explicitly rather than as `return client, err`. A typed nil
		// pointer in an interface is not a nil interface, so the caller's check
		// for a missing line would be false on exactly the path that has none.
		return nil, err
	}
	return client, nil
}

func (l pooledLines) Retire(accountID string, generation uint64) int {
	return l.pool.Retire(accountID, generation)
}

// newDeviceRunner assembles the worker that actually authenticates.
//
// Wireless is left nil on purpose. This build reads a radio -- it can scan, and
// it can see what the client is associated with -- but it does not write one:
// spec 07 puts the wireless transaction in M10 and forbids modifying a real
// radio until that card passes. A switch is therefore refused with
// UnsupportedCapability rather than half-performed, which is the same choice
// M08 made about actions with no worker behind them.
func newDeviceRunner(settings application.Settings, pool *transport.Pool,
	clock policy.Clock) application.Runner {

	return application.NewAuthenticator(application.AuthenticatorOptions{
		Binder:   deviceBinder{adapter: openwrt.NewAdapter(openwrt.Runner{})},
		Lines:    pooledLines{pool: pool},
		Settings: settings,
		Clock:    clock,
	})
}
