package run

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"ride-sharing/internal/adapter/simulation/runsetup"
	appdispatch "ride-sharing/internal/app/dispatch"
	apprun "ride-sharing/internal/app/run"
	"ride-sharing/internal/platform/config"
)

type LiveHandler struct {
	dispatcher      *appdispatch.Service
	sender          appdispatch.Sender
	responseTimeout time.Duration
}

func NewLiveHandler(
	dispatcher *appdispatch.Service,
	sender appdispatch.Sender,
	responseTimeout time.Duration,
) *LiveHandler {
	return &LiveHandler{
		dispatcher:      dispatcher,
		sender:          sender,
		responseTimeout: responseTimeout,
	}
}

type LiveRequest struct {
	RiderCount int   `json:"riderCount"`
	TopK       int   `json:"topK"`
	Seed       int64 `json:"seed"`
}

func (h *LiveHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()

	var request LiveRequest
	if err := decoder.Decode(&request); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: err.Error()})
		return
	}
	var extra struct{}
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{
			Error: "request body must contain one JSON object",
		})
		return
	}

	if request.RiderCount <= 0 || request.TopK <= 0 ||
		request.TopK > request.RiderCount {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{
			Error: "require riderCount > 0 and 0 < topK <= riderCount",
		})
		return
	}

	cfg := config.Default()
	cfg.RiderCount = request.RiderCount
	cfg.OrderCount = 1
	cfg.Seed = request.Seed
	cfg.Strategy = config.StrategyBalanced
	cfg.TopK = request.TopK
	cfg.ArrivalWindow = time.Second
	cfg.ArrivalModel = config.ArrivalUnbounded
	cfg.RunTimeout = 2 * time.Minute

	result, err := apprun.RunLive(
		r.Context(),
		cfg,
		runsetup.Factory{},
		h.dispatcher,
		h.sender,
		h.responseTimeout,
	)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, result)
}