package run

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"ride-sharing/internal/adapter/simulation/runsetup"
	apprun "ride-sharing/internal/app/run"
	"ride-sharing/internal/domain/geo"
	"ride-sharing/internal/domain/order"
	"ride-sharing/internal/platform/config"
)

type LiveOrderRequest struct {
	OrderID uint64 `json:"orderId"`
	Pickup  struct {
		Latitude  float64 `json:"latitude"`
		Longitude float64 `json:"longitude"`
	} `json:"pickup"`
	TopK                   int     `json:"topK"`
	MaxExtraDistanceMeters float64 `json:"maxExtraDistanceMeters"`
}

type oneOrderSource struct {
	value order.Order
	used  bool
}

func (s *oneOrderSource) Next() (order.Order, bool, error) {
	if s.used {
		return order.Order{}, false, nil
	}
	s.used = true
	return s.value, true, nil
}

func (h *LiveHandler) Order(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()

	var request LiveOrderRequest
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

	pickup := geo.GeoPoint{
		Latitude:  request.Pickup.Latitude,
		Longitude: request.Pickup.Longitude,
	}
	if request.OrderID == 0 ||
		request.TopK <= 0 ||
		request.MaxExtraDistanceMeters < 0 ||
		!geo.SanFranciscoBounds.Contains(pickup) {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{
			Error: "invalid orderId, topK, distance limit or pickup",
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
	if request.TopK > len(locations) {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{
			Error: "topK exceeds online rider count",
		})
		return
	}

	// 与 runsetup.Factory 使用同一个投影原点。
	projector, err := geo.NewProjector(geo.SanFranciscoBounds.Center())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}
	point, err := projector.Project(pickup)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: err.Error()})
		return
	}

	source := &oneOrderSource{value: order.Order{
		ID:     request.OrderID,
		Pickup: pickup,
		Point:  point,
	}}

	cfg := config.Default()
	cfg.RiderCount = len(locations)
	cfg.OrderCount = 1
	cfg.Strategy = config.StrategyBalanced
	cfg.TopK = request.TopK
	cfg.MaxExtraDistanceMeters = request.MaxExtraDistanceMeters
	cfg.ArrivalModel = config.ArrivalUnbounded
	cfg.ArrivalWindow = time.Second
	cfg.RunTimeout = 2 * time.Minute

	result, err := apprun.RunLive(
		r.Context(),
		cfg,
		runsetup.Factory{RiderLocations: locations},
		source,
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