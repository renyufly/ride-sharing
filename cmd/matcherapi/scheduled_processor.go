package main

import (
	"context"
	"fmt"
	"sync"
	"time"

	"ride-sharing/internal/adapter/mongostore"
	"ride-sharing/internal/adapter/spatial/kdtree"
	"ride-sharing/internal/app/dispatch"
	appmatching "ride-sharing/internal/app/matching"
	"ride-sharing/internal/domain/geo"
	"ride-sharing/internal/domain/order"
	"ride-sharing/internal/domain/rider"
)

const riderSnapshotTTL = 500 * time.Millisecond

type riderSnapshot struct {
	matcher kdtree.Matcher
	loads   map[uint64]appmatching.RiderLoad
	routes  map[uint64]dispatch.RiderSession
}

type scheduledOrderProcessor struct {
	store             *mongostore.OrderStore
	projector         geo.Projector
	maxOrdersPerRider int
	replyTimeout      time.Duration

	mu        sync.Mutex
	snapshot  riderSnapshot
	refreshAt time.Time
}

func newScheduledOrderProcessor(
	store *mongostore.OrderStore,
	maxOrdersPerRider int,
	replyTimeout time.Duration,
) (*scheduledOrderProcessor, error) {
	projector, err := geo.NewProjector(geo.SanFranciscoBounds.Center())
	if err != nil {
		return nil, err
	}
	return &scheduledOrderProcessor{
		store:             store,
		projector:         projector,
		maxOrdersPerRider: maxOrdersPerRider,
		replyTimeout:      replyTimeout,
	}, nil
}

func (p *scheduledOrderProcessor) process(
	ctx context.Context, claim dispatch.ClaimedOrder,
) (bool, error) {
	snapshot, err := p.ridersForMatching(ctx)
	if err != nil {
		return false, err
	}
	if len(snapshot.loads) == 0 {
		return false, nil
	}

	point, err := p.projector.Project(claim.Spec.Pickup)
	if err != nil {
		return false, fmt.Errorf("project order %d pickup: %w", claim.OrderID, err)
	}

	selected, found, err := appmatching.SelectLiveCandidate(
		order.Order{ID: claim.OrderID, Pickup: claim.Spec.Pickup, Point: point},
		snapshot.matcher,
		snapshot.loads,
		claim.TriedRiders,
		claim.Spec.TopK,
		claim.Spec.MaxExtraDistanceMeters,
		p.maxOrdersPerRider,
	)
	if err != nil || !found {
		return false, err
	}

	return p.store.CreateOffer(
		ctx,
		claim,
		snapshot.routes[selected.RiderUID],
		selected.DistanceSquaredMeters,
		p.maxOrdersPerRider,
		p.replyTimeout,
	)
}

func (p *scheduledOrderProcessor) ridersForMatching(
	ctx context.Context,
) (riderSnapshot, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if err := ctx.Err(); err != nil {
		return riderSnapshot{}, err
	}
	if time.Now().Before(p.refreshAt) {
		return p.snapshot, nil
	}

	readCtx, cancel := context.WithTimeout(ctx, time.Second)
	online, err := p.store.ListOnlineRiders(readCtx)
	cancel()
	if err != nil {
		return riderSnapshot{}, err
	}

	next := riderSnapshot{
		loads:  make(map[uint64]appmatching.RiderLoad, len(online)),
		routes: make(map[uint64]dispatch.RiderSession, len(online)),
	}
	projected := make([]rider.Rider, 0, len(online))
	for _, current := range online {
		point, err := p.projector.Project(current.Location)
		if err != nil {
			return riderSnapshot{}, fmt.Errorf("project rider %d location: %w", current.RiderUID, err)
		}

		projected = append(projected, rider.Rider{
			UID:      current.RiderUID,
			Location: current.Location,
			Point:    point,
		})
		next.loads[current.RiderUID] = appmatching.RiderLoad{
			Active:   current.Active,
			Reserved: current.Reserved,
		}
		next.routes[current.RiderUID] = current.RiderSession
	}

	if len(projected) > 0 {
		next.matcher, err = kdtree.New(projected)
		if err != nil {
			return riderSnapshot{}, fmt.Errorf("build live rider index: %w", err)
		}
	}

	p.snapshot = next
	p.refreshAt = time.Now().Add(riderSnapshotTTL)
	return next, nil
}