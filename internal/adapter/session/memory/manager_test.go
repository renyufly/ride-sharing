package memory

import (
	"testing"

	appmatching "ride-sharing/internal/app/matching"
	"ride-sharing/internal/domain/order"
	"ride-sharing/internal/platform/config"
)

func TestManagerSerializesRunsAndChecksReplayState(t *testing.T) {
	manager := New[int]()
	lease, ok := manager.TryBegin()
	if !ok {
		t.Fatal("first run could not acquire the slot")
	}
	if _, ok := manager.TryBegin(); ok {
		t.Fatal("second run acquired the occupied slot")
	}
	if _, status := lease.RetryState("run-1", 1); status != RetrySessionMissing {
		t.Fatalf("empty session status = %d", status)
	}
	cfg := config.Default()
	cfg.Strategy = config.StrategyBalanced
	deferred := []appmatching.DeferredOrder{{Order: order.Order{ID: 42}}}
	first := lease.SaveFirst(cfg, 3, deferred, 10)
	deferred[0].Order.ID = 99
	if first.RunID != "run-1" || first.CurrentRound != 1 || first.DeferredOrders[0].Order.ID != 42 {
		t.Fatalf("first session changed: %+v", first)
	}
	for _, check := range []struct {
		runID string
		round int
		want  RetryStatus
	}{
		{"run-2", 1, RetryRunReplaced},
		{"run-1", 0, RetryRoundStale},
		{"run-1", 1, RetryReady},
	} {
		if _, status := lease.RetryState(check.runID, check.round); status != check.want {
			t.Fatalf("RetryState(%q, %d) = %d, want %d", check.runID, check.round, status, check.want)
		}
	}
	next := lease.SaveNext(cfg, nil, 20)
	if next.RunID != "run-1" || next.CurrentRound != 2 || len(next.Rounds) != 2 || next.Rounds[0] != 10 || next.Rounds[1] != 20 {
		t.Fatalf("next session changed: %+v", next)
	}
	if _, status := lease.RetryState("run-1", 2); status != RetryNoDeferredOrders {
		t.Fatalf("empty deferred status = %d", status)
	}
	lease.End()
	lease.End()
	second, ok := manager.TryBegin()
	if !ok {
		t.Fatal("slot remained occupied after End")
	}
	defer second.End()
	cfg.Strategy = config.StrategyNearest
	third := second.SaveFirst(cfg, 1, []appmatching.DeferredOrder{{}}, 30)
	if third.RunID != "run-2" {
		t.Fatalf("replacement run ID = %q", third.RunID)
	}
	if _, status := second.RetryState("run-2", 1); status != RetryRequiresBalanced {
		t.Fatalf("nearest retry status = %d", status)
	}
}
