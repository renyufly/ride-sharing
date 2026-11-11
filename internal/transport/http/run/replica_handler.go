package run

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

type replicaScaler interface {
	Get(context.Context) (desired, ready int32, err error)
	Set(context.Context, int32) error
}

func RegisterReplicaRoutes(
	mux *http.ServeMux, scaler replicaScaler, token string, maxReplicas int32,
) {
	const path = "/matcher/admin/replicas"

	requireToken := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			expected := "Bearer " + token
			if token == "" || subtle.ConstantTimeCompare(
				[]byte(r.Header.Get("Authorization")), []byte(expected),
			) != 1 {
				_ = writeJSON(w, http.StatusUnauthorized, ErrorResponse{Error: "unauthorized"})
				return
			}
			w.Header().Set("Cache-Control", "no-store")
			next(w, r)
		}
	}

	mux.HandleFunc("GET "+path, requireToken(func(w http.ResponseWriter, r *http.Request) {
		desired, ready, err := scaler.Get(r.Context())
		if err != nil {
			log.Printf("get matcher replicas: %v", err)
			_ = writeJSON(w, http.StatusBadGateway, ErrorResponse{Error: "could not read replicas"})
			return
		}
		_ = writeJSON(w, http.StatusOK, map[string]int32{
			"desired": desired, "ready": ready, "maxReplicas": maxReplicas,
		})
	}))

	mux.HandleFunc("PUT "+path, requireToken(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 1024)
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()

		var request struct {
			Replicas int32 `json:"replicas"`
		}
		if err := decoder.Decode(&request); err != nil {
			_ = writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "invalid request body"})
			return
		}
		var extra json.RawMessage
		if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
			_ = writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "expected one JSON object"})
			return
		}
		if request.Replicas < 1 || request.Replicas > maxReplicas {
			_ = writeJSON(w, http.StatusBadRequest, ErrorResponse{
				Error: fmt.Sprintf("replicas must be between 1 and %d", maxReplicas),
			})
			return
		}

		if err := scaler.Set(r.Context(), request.Replicas); err != nil {
			if apierrors.IsConflict(err) {
				_ = writeJSON(w, http.StatusConflict, ErrorResponse{Error: "replicas changed, reload and retry"})
				return
			}
			log.Printf("set matcher replicas: %v", err)
			_ = writeJSON(w, http.StatusBadGateway, ErrorResponse{Error: "could not update replicas"})
			return
		}

		_ = writeJSON(w, http.StatusAccepted, map[string]int32{"desired": request.Replicas})
	}))
}