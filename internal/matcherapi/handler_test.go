package matcherapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRunAndRetryKeepSynchronousResponseContract(t *testing.T) {
	handler := NewHandler()
	runBody, err := json.Marshal(Request{
		RiderCount: 1, OrderCount: 2, ArrivalWindow: "1s", ArrivalModel: "unbounded",
		Seed: 42, RiderDistribution: "uniform", OrderDistribution: "uniform",
		Algorithm: "kd-tree", Strategy: "balanced", Workers: 1, BatchSize: 1,
		ChannelCapacity: 2, TopK: 1, MaxExtraDistanceMeters: 0,
		MaxOrdersPerRider: 1, PreviewSize: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	first := httptest.NewRecorder()
	handler.Run(first, httptest.NewRequest(http.MethodPost, "/matcher/run", bytes.NewReader(runBody)))
	if first.Code != http.StatusOK {
		t.Fatalf("Run status = %d, body = %s", first.Code, first.Body.String())
	}
	var initial RunResponse
	if err := json.Unmarshal(first.Body.Bytes(), &initial); err != nil {
		t.Fatal(err)
	}
	if initial.RunID == "" || initial.CurrentRound != 1 || len(initial.Rounds) != 1 || initial.Counts.Deferred != 1 {
		t.Fatalf("Run response changed: %+v", initial)
	}

	retryBody, err := json.Marshal(RetryRequest{RunID: initial.RunID, SourceRound: 1, MaxOrdersPerRider: 2, GracePeriodMs: 1000})
	if err != nil {
		t.Fatal(err)
	}
	second := httptest.NewRecorder()
	handler.Retry(second, httptest.NewRequest(http.MethodPost, "/matcher/retry", bytes.NewReader(retryBody)))
	if second.Code != http.StatusOK {
		t.Fatalf("Retry status = %d, body = %s", second.Code, second.Body.String())
	}
	var replayed RunResponse
	if err := json.Unmarshal(second.Body.Bytes(), &replayed); err != nil {
		t.Fatal(err)
	}
	if replayed.RunID != initial.RunID || replayed.CurrentRound != 2 || len(replayed.Rounds) != 2 || replayed.Counts.Completed != 1 {
		t.Fatalf("Retry response changed: %+v", replayed)
	}
}
