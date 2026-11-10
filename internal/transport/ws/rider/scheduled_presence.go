package rider

import (
	"context"
	"log"
	"time"

	"ride-sharing/internal/adapter/realtime"
	"ride-sharing/internal/app/dispatch"
	"ride-sharing/internal/domain/geo"
	"ride-sharing/internal/platform/config"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

const presenceWriteTimeout = 2 * time.Second

type scheduledRiderStore interface {
	RegisterRiderSession(context.Context, dispatch.RiderSession, time.Time) error
	RenewRiderSession(context.Context, dispatch.RiderSession, time.Time) (bool, error)
	SaveRiderLocation(context.Context, dispatch.RiderSession, geo.GeoPoint) (bool, error)
	UnregisterRiderSession(context.Context, dispatch.RiderSession) error
	RespondToOffer(context.Context, dispatch.RiderSession, uint64, string, bool) (bool, error)
	ScheduledStatusForRider(context.Context, dispatch.RiderSession, uint64) (dispatch.RiderOrderStatus, error)
	CompleteScheduledOrderForSession(context.Context, dispatch.RiderSession, uint64) error
}

func NewScheduledHandler(
	connections *realtime.Manager,
	identify Identify,
	cfg config.RealtimeConfig,
	gatewayID string,
	store scheduledRiderStore,
) *Handler {
	h := NewHandler(
		connections, nil, identify,
		cfg.ReadLimitBytes, cfg.WriteTimeout, cfg.PingInterval, cfg.PongTimeout,
		nil,
	)
	h.scheduled = store
	h.gatewayID = gatewayID
	return h
}

func (h *Handler) registerScheduledSession(
	ctx context.Context, riderUID uint64, conn *websocket.Conn,
) (dispatch.RiderSession, func(), error) {
	route := dispatch.RiderSession{
		RiderUID:  riderUID,
		GatewayID: h.gatewayID,
		SessionID: uuid.NewString(),
	}

	h.connections.RegisterSession(route.SessionID, conn)
	cleanup := func() {
		h.connections.RemoveSession(route.SessionID, conn)

		cleanupCtx, cancel := context.WithTimeout(context.Background(), presenceWriteTimeout)
		defer cancel()
		if err := h.scheduled.UnregisterRiderSession(cleanupCtx, route); err != nil {
			log.Printf("clear scheduled rider session %d: %v", riderUID, err)
		}
	}

	writeCtx, cancel := context.WithTimeout(ctx, presenceWriteTimeout)
	defer cancel()
	err := h.scheduled.RegisterRiderSession(writeCtx, route, time.Now().Add(h.pongTimeout))
	return route, cleanup, err
}
