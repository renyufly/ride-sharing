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
	
	request, pickup, err := decodeLiveOrderRequest(w, r)
	if err != nil {
		_ = writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: err.Error()})
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

	cfg.MaxOrdersPerRider = h.maxOrdersPerRider

	result, err := apprun.RunLive(
		h.orderContext,
		cfg,
		runsetup.Factory{RiderLocations: locations},
		source,
		h.dispatcher,
		h.sender,
		h.responseTimeout,
		h.ledger,
	)
	
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, appdispatch.ErrOrderAlreadyAssigned) ||
			errors.Is(err, appdispatch.ErrOrderInProgress) {
			status = http.StatusConflict
		}
		writeJSON(w, status, ErrorResponse{Error: err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, result)
}

func decodeLiveOrderRequest(
	w http.ResponseWriter, r *http.Request,
) (LiveOrderRequest, geo.GeoPoint, error) {
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()

	var req LiveOrderRequest
	if err := decoder.Decode(&req); err != nil {
		return req, geo.GeoPoint{}, err
	}
	var extra struct{}
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return req, geo.GeoPoint{}, errors.New("request body must contain one JSON object")
	}

	pickup := geo.GeoPoint{
		Latitude:  req.Pickup.Latitude,
		Longitude: req.Pickup.Longitude,
	}
	if req.OrderID == 0 || req.TopK <= 0 || req.MaxExtraDistanceMeters < 0 ||
		!geo.SanFranciscoBounds.Contains(pickup) {
		return req, pickup, errors.New("invalid orderId, topK, distance limit or pickup")
	}
	return req, pickup, nil
}