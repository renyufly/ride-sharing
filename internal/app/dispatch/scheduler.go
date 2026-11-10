package dispatch

import (
	"context"
	"log"
	"sync"
	"time"
)

// TODO: 待修改成config
const (
	schedulerWorkers = 2
	claimLease       = 5 * time.Second
	idleDelay        = 250 * time.Millisecond
	noCandidateDelay = time.Second
	storeTimeout     = 2 * time.Second
	expiryInterval   = 500 * time.Millisecond
	expiryBatchSize  = 16
)

type schedulerStore interface {
	ClaimNext(context.Context, time.Duration) (*ClaimedOrder, error)
	RequeueClaim(context.Context, ClaimedOrder, time.Duration) (bool, error)
	ExpireBeforeOffer(context.Context) (bool, error)
	ExpireNextOffer(context.Context) (bool, error)
}

// ClaimProcessor returns true after persisting a transition out of matching.
// It returns false, nil when the order should be retried after a delay.
type ClaimProcessor func(context.Context, ClaimedOrder) (bool, error)

func RunScheduler(ctx context.Context, store schedulerStore, process ClaimProcessor) {
	var group sync.WaitGroup
	for range schedulerWorkers {
		group.Go(func() {
			runClaimWorker(ctx, store, process)
		})
	}
	group.Go(func() {
		runExpiryWorker(ctx, store)
	})
	group.Wait()
}

func runClaimWorker(ctx context.Context, store schedulerStore, process ClaimProcessor) {
	for ctx.Err() == nil {
		claimCtx, cancel := context.WithTimeout(ctx, storeTimeout)
		claim, err := store.ClaimNext(claimCtx, claimLease)
		cancel()
		if err != nil && ctx.Err() == nil {
			log.Printf("claim scheduled order: %v", err)
		}
		if err != nil || claim == nil {
			if !waitForNextClaim(ctx) {
				return
			}
			continue
		}

		deadline := claim.LeaseUntil
		if claim.DeadlineAt.Before(deadline) {
			deadline = claim.DeadlineAt
		}
		processCtx, cancel := context.WithDeadline(ctx, deadline)
		advanced, err := process(processCtx, *claim)
		cancel()
		if err != nil {
			if ctx.Err() == nil {
				log.Printf("process scheduled order %d: %v", claim.OrderID, err)
			}
			continue
		}
		if advanced {
			continue
		}

		requeueCtx, cancel := context.WithTimeout(ctx, storeTimeout)
		_, err = store.RequeueClaim(requeueCtx, *claim, noCandidateDelay)
		cancel()
		if err != nil && ctx.Err() == nil {
			log.Printf("requeue scheduled order %d: %v", claim.OrderID, err)
		}
	}
}

func runExpiryWorker(ctx context.Context, store schedulerStore) {
	ticker := time.NewTicker(expiryInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		sweepCtx, cancel := context.WithTimeout(ctx, storeTimeout)
		for range expiryBatchSize {
			beforeOffer, err := store.ExpireBeforeOffer(sweepCtx)
			if err != nil {
				log.Printf("expire order before offer: %v", err)
				break
			}

			offered, err := store.ExpireNextOffer(sweepCtx)
			if err != nil {
				log.Printf("expire rider offer: %v", err)
				break
			}

			if !beforeOffer && !offered {
				break
			}
		}

		cancel()
	}
}

func waitForNextClaim(ctx context.Context) bool {
	timer := time.NewTimer(idleDelay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}