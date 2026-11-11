package run

import (
	"context"
	"errors"
	"log"
	"net/http"
	"strconv"
	"time"

	"ride-sharing/internal/app/dispatch"
)

const scheduledOrdersPath = "/matcher/v2/live/orders"

type scheduledOrderStore interface {
	Enqueue(context.Context, uint64, dispatch.ScheduleSpec, time.Time) (dispatch.ScheduleStatus, error)
	GetOrder(context.Context, uint64) (dispatch.ScheduledOrderDetails, error)
}

type scheduledOrderResponse struct {
	OrderID string                  `json:"orderId"`
	Status  dispatch.ScheduleStatus `json:"status"`
}

func RegisterScheduledOrderRoutes(mux *http.ServeMux, store scheduledOrderStore) {
	mux.HandleFunc("POST "+scheduledOrdersPath, func(w http.ResponseWriter, r *http.Request) {
		req, pickup, err := decodeLiveOrderRequest(w, r)
		if err != nil {
			_ = writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: err.Error()})
			return
		}
		const maxTopK = 100
		if req.TopK > maxTopK {
			_ = writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "topK must be at most 100"})
			return
		}

		spec := dispatch.ScheduleSpec{
			Pickup:                 pickup,
			TopK:                   req.TopK,
			MaxExtraDistanceMeters: req.MaxExtraDistanceMeters,
		}
		deadline := time.Now().Add(2 * time.Minute)
		status, err := store.Enqueue(r.Context(), req.OrderID, spec, deadline)
		if errors.Is(err, dispatch.ErrOrderPayloadConflict) {
			_ = writeJSON(w, http.StatusConflict, ErrorResponse{Error: err.Error()})
			return
		}
		if err != nil {
			log.Printf("enqueue scheduled order: %v", err)
			_ = writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "could not queue order"})
			return
		}

		id := strconv.FormatUint(req.OrderID, 10)
		w.Header().Set("Location", scheduledOrdersPath+"/"+id)
		_ = writeJSON(w, http.StatusAccepted, scheduledOrderResponse{OrderID: id, Status: status})
	})

	mux.HandleFunc("GET "+scheduledOrdersPath+"/{orderId}", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseUint(r.PathValue("orderId"), 10, 64)
		if err != nil || id == 0 {
			_ = writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "invalid orderId"})
			return
		}

		details, err := store.GetOrder(r.Context(), id)
		if errors.Is(err, dispatch.ErrScheduledOrderNotFound) {
			_ = writeJSON(w, http.StatusNotFound, ErrorResponse{Error: "order not found"})
			return
		}
		if err != nil {
			log.Printf("get scheduled order status: %v", err)
			_ = writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "could not read order status"})
			return
		}

		w.Header().Set("Cache-Control", "no-store")
		_ = writeJSON(w, http.StatusOK, details)
	})
}