package application

import (
	"context"
	"time"

	"github.com/matthewlu070111/smart-srun/core/internal/domain"
	"github.com/matthewlu070111/smart-srun/core/internal/policy"
)

// Ownership is deliberately runtime-only. An already selected hotspot, or one
// observed after a restart without an owned transition, is left to the user.
type quietSwitchState struct {
	occurrence string
	hotspotID  string
	inFlight   string
	done       bool
	owned      bool
	dueAt      time.Time
}

func (m *Maintainer) scheduleQuietSwitch(ctx context.Context, cfg *domain.Config, quiet policy.QuietState, now time.Time) {
	s := &m.quietSwitch
	if !cfg.Enabled || !cfg.Failover.Enabled {
		s.owned = false
		return
	}
	if quiet.Active && s.occurrence != quiet.Occurrence {
		*s = quietSwitchState{occurrence: quiet.Occurrence}
	}
	if s.inFlight != "" || now.Before(s.dueAt) {
		return
	}
	if _, ok := cfg.CampusAccountByID(cfg.Selection.ActiveCampusID); !ok {
		return
	}
	kind := KindQuietCampus
	if quiet.Active {
		if s.done {
			return
		}
		if quiet.ForceLogout && len(m.sweep.Pending(quiet.Occurrence, policy.ForcedLogoutTargets(cfg))) != 0 {
			return // finish the configured logout sweep before moving its radio
		}
		s.hotspotID = cfg.Selection.ActiveHotspotID
		if s.hotspotID == "" {
			s.hotspotID = cfg.Selection.DefaultHotspotID
		}
		kind = KindQuietHotspot
	} else if !s.owned {
		return
	}
	if _, ok := cfg.HotspotByID(s.hotspotID); !ok {
		return
	}
	receipt, err := m.submit(ctx, Request{Kind: kind, AccountID: cfg.Selection.ActiveCampusID,
		HotspotID: s.hotspotID, CheckRevision: true, ConfigRevision: cfg.Revision,
		IdempotencyKey: m.key(string(kind), cfg.Selection.ActiveCampusID)})
	if err == nil {
		s.inFlight = receipt.ActionID
	}
}

func (m *Maintainer) applyQuietSwitch(action Action, cfg domain.Config, now time.Time) bool {
	s := &m.quietSwitch
	if action.Request.Kind == KindSwitchCampus || action.Request.Kind == KindSwitchHotspot {
		if action.State == StateSucceeded {
			s.owned, s.done = false, true
		}
		return false
	}
	if action.Request.Kind != KindQuietHotspot && action.Request.Kind != KindQuietCampus {
		return false
	}
	if action.ID != s.inFlight {
		return true
	}
	s.inFlight = ""
	s.dueAt = now.Add(max(30*time.Second, checkInterval(&cfg)))
	if action.State == StateCancelled || action.State == StateInterrupted {
		s.done, s.owned = true, false
	} else if action.State == StateSucceeded {
		s.done = true
		s.owned = action.Request.Kind == KindQuietHotspot && !action.MaintenanceDeferred
		s.dueAt = time.Time{}
	}
	return true
}
