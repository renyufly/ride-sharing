package run

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	appdispatch "ride-sharing/internal/app/dispatch"
)

type completeOrderRequest struct {
    OrderID  uint64 `json:"orderId"`
    RiderUID uint64 `json:"riderUid"`
}

func NewLiveCompleteHandler(ledger *appdispatch.LiveLedger, token string) http.Handler {
	return NewCompleteOrderHandler(token, func(_ context.Context, orderID, riderUID uint64) error {
		return ledger.Complete(orderID, riderUID)
	})
}

func NewCompleteOrderHandler(
	token string,
	complete func(context.Context, uint64, uint64) error,
) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        expected := "Bearer " + token
        if token == "" ||
            subtle.ConstantTimeCompare(
                []byte(r.Header.Get("Authorization")),
                []byte(expected),
            ) != 1 {
            writeJSON(w, http.StatusUnauthorized,
                ErrorResponse{Error: "unauthorized"})
            return
        }

        r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
        decoder := json.NewDecoder(r.Body)
        decoder.DisallowUnknownFields()

        var request completeOrderRequest
        if err := decoder.Decode(&request); err != nil {
            writeJSON(w, http.StatusBadRequest,
                ErrorResponse{Error: err.Error()})
            return
        }
        var extra struct{}
        if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
            writeJSON(w, http.StatusBadRequest,
                ErrorResponse{Error: "request body must contain one JSON object"})
            return
        }
        if request.OrderID == 0 || request.RiderUID == 0 {
            writeJSON(w, http.StatusBadRequest,
                ErrorResponse{Error: "orderId and riderUid must be positive"})
            return
        }

        ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
        defer cancel()

        if err := complete(ctx, request.OrderID, request.RiderUID); err != nil {
            status := http.StatusInternalServerError
            switch {
            case errors.Is(err, appdispatch.ErrOrderNotAssigned):
                status = http.StatusNotFound
            case errors.Is(err, appdispatch.ErrOrderRiderMismatch):
                status = http.StatusConflict
            }
            writeJSON(w, status, ErrorResponse{Error: err.Error()})
            return
        }
        w.WriteHeader(http.StatusNoContent)
    })
}
