package run

import (
	"context"
	"time"

	appdispatch "ride-sharing/internal/app/dispatch"
	appmatching "ride-sharing/internal/app/matching"
	domaindispatch "ride-sharing/internal/domain/dispatch"
	"ride-sharing/internal/domain/order"
)

type liveOfferConfirmer struct {
	dispatcher      *appdispatch.Service
	sender          appdispatch.Sender
	responseTimeout time.Duration
	notifier appdispatch.AssignmentNotifier
}

var _ appmatching.OfferConfirmer = liveOfferConfirmer{}
var _ appmatching.OfferLedger = (*appdispatch.LiveLedger)(nil)

func (c liveOfferConfirmer) Offer(
	ctx context.Context,
	currentOrder order.Order,
	riderUID uint64,
) (domaindispatch.Attempt, error) {
	return c.dispatcher.OfferOne(
		ctx,
		currentOrder,
		riderUID,
		c.sender,
		c.responseTimeout,
	)
}

func (c liveOfferConfirmer) NotifyAccepted(
    ctx context.Context,
    attempt domaindispatch.Attempt,
) error {
    return c.notifier.SendAssignmentConfirmed(
        ctx, attempt.RiderUID, attempt.OrderID, attempt.RequestID,
    )
}