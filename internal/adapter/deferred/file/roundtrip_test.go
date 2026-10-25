package file

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	appmatching "ride-sharing/internal/app/matching"
	"ride-sharing/internal/domain/geo"
	"ride-sharing/internal/domain/order"
)

func TestJSONLRoundTripKeepsRecordAndReplaySemantics(t *testing.T) {
	path := filepath.Join(t.TempDir(), "deferred.jsonl")
	sink, err := NewFileSink(path)
	if err != nil {
		t.Fatal(err)
	}
	record := appmatching.DeferredOrder{
		Order:  order.Order{ID: 42, Sequence: 7, PlannedArrivalNs: 99, Pickup: geo.GeoPoint{Latitude: 37.7, Longitude: -122.4}, Point: geo.Point2D{X: 12, Y: 34}},
		Reason: appmatching.DeferredReasonCapacityExhausted, DeferredAtNs: 55, Attempt: 2,
	}
	if err := sink.Store(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	if err := sink.Close(); err != nil {
		t.Fatal(err)
	}
	encoded, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var saved deferredRecord
	if err := json.Unmarshal(encoded, &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Version != 1 || saved.Attempt != 2 || saved.Reason != record.Reason || saved.Order.Sequence != 7 || saved.Order.PlannedArrivalNs != 99 {
		t.Fatalf("saved record changed: %+v", saved)
	}
	source, count, err := OpenFileSource(path, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	if count != 1 {
		t.Fatalf("count = %d, want 1", count)
	}
	replayed, ok, err := source.Next()
	if err != nil || !ok {
		t.Fatalf("Next() = (%+v, %t, %v)", replayed, ok, err)
	}
	if replayed.ID != 42 || replayed.Sequence != 0 || replayed.PlannedArrivalNs != 0 || replayed.Pickup != record.Order.Pickup || replayed.Point != record.Order.Point {
		t.Fatalf("replayed order changed: %+v", replayed)
	}
}
