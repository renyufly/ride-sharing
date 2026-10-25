package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	runhttp "ride-sharing/internal/transport/http/run"
)

func TestRunAndRetryRoutesKeepJSONAndStatusContract(t *testing.T) {
	mux := newMux(runhttp.NewHandler())
	body := `{"riderCount":1,"orderCount":2,"arrivalWindow":"1s","arrivalModel":"unbounded","seed":42,"riderDistribution":"uniform","orderDistribution":"uniform","algorithm":"kd-tree","strategy":"balanced","workers":1,"batchSize":1,"channelCapacity":2,"topK":1,"maxExtraDistanceMeters":0,"maxOrdersPerRider":1,"previewSize":2}`
	first := httptest.NewRecorder()
	mux.ServeHTTP(first, httptest.NewRequest(http.MethodPost, "/matcher/run", strings.NewReader(body)))
	if first.Code != http.StatusOK {
		t.Fatalf("POST /matcher/run status = %d, body = %s", first.Code, first.Body.String())
	}
	var runResult map[string]json.RawMessage
	if err := json.Unmarshal(first.Body.Bytes(), &runResult); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"runId", "currentRound", "rounds", "counts", "map", "deferred"} {
		if _, ok := runResult[field]; !ok {
			t.Fatalf("Run response missing %q: %s", field, first.Body.String())
		}
	}
	var runID string
	if err := json.Unmarshal(runResult["runId"], &runID); err != nil || runID == "" {
		t.Fatalf("Run ID = %q, error = %v", runID, err)
	}
	for _, check := range []struct {
		path, body string
		want       int
	}{
		{"/matcher/run", `{"unknown":true}`, http.StatusBadRequest},
		{"/matcher/retry", `{"runId":"missing","sourceRound":1}`, http.StatusNotFound},
		{"/matcher/retry", `{"runId":"` + runID + `","sourceRound":0}`, http.StatusConflict},
	} {
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, httptest.NewRequest(http.MethodPost, check.path, strings.NewReader(check.body)))
		if response.Code != check.want {
			t.Fatalf("POST %s status = %d, want %d; body = %s", check.path, response.Code, check.want, response.Body.String())
		}
		var failure struct {
			Error string `json:"error"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &failure); err != nil || failure.Error == "" {
			t.Fatalf("error response = %s, decode error = %v", response.Body.String(), err)
		}
	}
	retryBody := `{"runId":"` + runID + `","sourceRound":1,"maxOrdersPerRider":2,"gracePeriodMs":1000}`
	second := httptest.NewRecorder()
	mux.ServeHTTP(second, httptest.NewRequest(http.MethodPost, "/matcher/retry", strings.NewReader(retryBody)))
	if second.Code != http.StatusOK {
		t.Fatalf("POST /matcher/retry status = %d, body = %s", second.Code, second.Body.String())
	}
	var retryResult struct {
		RunID        string            `json:"runId"`
		CurrentRound int               `json:"currentRound"`
		Rounds       []json.RawMessage `json:"rounds"`
	}
	if err := json.Unmarshal(second.Body.Bytes(), &retryResult); err != nil {
		t.Fatal(err)
	}
	if retryResult.RunID != runID || retryResult.CurrentRound != 2 || len(retryResult.Rounds) != 2 {
		t.Fatalf("Retry response changed: %+v", retryResult)
	}
	for _, check := range []struct {
		method, path string
		want         int
	}{
		{http.MethodGet, "/matcher/run", http.StatusMethodNotAllowed},
		{http.MethodPost, "/matcher/unknown", http.StatusNotFound},
	} {
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, httptest.NewRequest(check.method, check.path, nil))
		if response.Code != check.want {
			t.Fatalf("%s %s status = %d, want %d", check.method, check.path, response.Code, check.want)
		}
	}
}
