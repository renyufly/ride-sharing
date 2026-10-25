package memory

import (
	"context"
	"testing"

	appmatching "ride-sharing/internal/app/matching"
	"ride-sharing/internal/domain/order"
)

func TestSnapshotAndReplayOwnTheirOrderSlices(t *testing.T) {
	sink, err := NewMemorySink(1)
	if err != nil {
		t.Fatal(err)
	}
	first := appmatching.DeferredOrder{Order: order.Order{ID: 42, Sequence: 7, PlannedArrivalNs: 99}, Attempt: 1}
	if err := sink.Store(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	if err := sink.Store(context.Background(), appmatching.DeferredOrder{Order: order.Order{ID: 43}}); err != nil {
		t.Fatal(err)
	}
	snapshot := sink.Snapshot()
	if snapshot.Total != 2 || len(snapshot.Orders) != 1 {
		t.Fatalf("snapshot = %+v", snapshot)
	}
	source, err := NewMemorySource(snapshot.Orders)
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Orders[0].Order.ID = 999
	if sink.Snapshot().Orders[0].Order.ID != 42 {
		t.Fatal("snapshot shares the sink's order slice")
	}
	replayed, ok, err := source.Next()
	if err != nil || !ok || replayed.ID != 42 || replayed.Sequence != 0 || replayed.PlannedArrivalNs != 0 {
		t.Fatalf("replayed order = (%+v, %t, %v)", replayed, ok, err)
	}
}
