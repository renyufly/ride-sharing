package dispatch

import (
	"context"
	"log"
	"time"
)

type deliveryStore interface {
	ClaimNextDelivery(context.Context, string) (*OfferDelivery, error)
	IsRiderSessionCurrent(context.Context, RiderSession) (bool, error)
	StopOfferDelivery(context.Context, OfferDelivery) error
}

func RunOfferDelivery(
	ctx context.Context,
	gatewayID string,
	store deliveryStore,
	send func(context.Context, string, Offer) error,
) {
	for ctx.Err() == nil {
		stepCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		found, err := deliverNextOffer(stepCtx, gatewayID, store, send)
		cancel()

		if err != nil && ctx.Err() == nil {
			log.Printf("deliver scheduled offer: %v", err)
		}
		if !found || err != nil {
			if !waitForNextClaim(ctx) {
				return
			}
		}
	}
}

func deliverNextOffer(
	ctx context.Context,
	gatewayID string,
	store deliveryStore,
	send func(context.Context, string, Offer) error,
) (bool, error) {
	delivery, err := store.ClaimNextDelivery(ctx, gatewayID)
	if err != nil || delivery == nil {
		return false, err
	}

	current, err := store.IsRiderSessionCurrent(ctx, delivery.Route)
	if err != nil {
		return true, err
	}
	if !current || !delivery.ExpiresAt.After(time.Now()) {
		return true, store.StopOfferDelivery(ctx, *delivery)
	}

	writeDeadline := time.Now().Add(500 * time.Millisecond)
	if delivery.ExpiresAt.Before(writeDeadline) {
		writeDeadline = delivery.ExpiresAt
	}
	writeCtx, cancel := context.WithDeadline(ctx, writeDeadline)
	err = send(writeCtx, delivery.Route.SessionID, delivery.Offer)
	cancel()
	if err != nil {
		return true, err
	}

	return true, store.StopOfferDelivery(ctx, *delivery)
}