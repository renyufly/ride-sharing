package run

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"ride-sharing/internal/adapter/simulation/runsetup"
	appdispatch "ride-sharing/internal/app/dispatch"
	apprun "ride-sharing/internal/app/run"
	"ride-sharing/internal/domain/geo"
	"ride-sharing/internal/platform/config"
)

type LiveHandler struct {
	dispatcher        *appdispatch.Service
	sender            liveSender
	responseTimeout   time.Duration
	ledger            *appdispatch.LiveLedger
	maxOrdersPerRider int
	orderContext      context.Context
}

func NewLiveHandler(
	dispatcher *appdispatch.Service,
	sender liveSender,
	responseTimeout time.Duration,
	ledger *appdispatch.LiveLedger,
	maxOrdersPerRider int,
	orderContext context.Context,
) *LiveHandler {
	return &LiveHandler{
		dispatcher:        dispatcher,
		sender:            sender,
		responseTimeout:   responseTimeout,
		ledger:            ledger,
		maxOrdersPerRider: maxOrdersPerRider,
		orderContext:      orderContext,
	}
}

type LiveRequest struct {
	TopK                   int     `json:"topK"`
	Seed                   int64   `json:"seed"`
	OrderCount             int     `json:"orderCount"`
	BatchSize              int     `json:"batchSize"`
	MaxExtraDistanceMeters float64 `json:"maxExtraDistanceMeters"`
	MaxOrdersPerRider      int     `json:"maxOrdersPerRider"`
}

type liveSender interface {
	appdispatch.Sender
	SnapshotLocations() map[uint64]geo.GeoPoint
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

	locations := h.sender.SnapshotLocations()
	if len(locations) == 0 {
		writeJSON(w, http.StatusConflict, ErrorResponse{
			Error: "no online riders have reported locations",
		})
		return
	}
	if request.TopK <= 0 || request.TopK > len(locations) {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{
			Error: "topK must be between 1 and the number of online riders",
		})
		return
	}
	if request.BatchSize < 0 || request.BatchSize > 4 {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{
			Error: "batchSize must be between 1 and 4 when provided",
		})
		return
	}

	cfg := config.Default()
	cfg.RiderCount = len(locations)
	cfg.OrderCount = request.OrderCount
	if request.BatchSize > 0 {
		cfg.BatchSize = request.BatchSize
	}
	cfg.Seed = request.Seed
	cfg.Strategy = config.StrategyBalanced
	cfg.TopK = request.TopK

	cfg.MaxExtraDistanceMeters = request.MaxExtraDistanceMeters
	cfg.MaxOrdersPerRider = request.MaxOrdersPerRider

	cfg.ArrivalWindow = time.Second
	cfg.ArrivalModel = config.ArrivalUnbounded
	cfg.RunTimeout = 2 * time.Minute

	result, err := apprun.RunLive(
		r.Context(),
		cfg,
		runsetup.Factory{RiderLocations: locations},
		nil,
		h.dispatcher,
		h.sender,
		h.responseTimeout,
		h.ledger,
	)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, result)
}
