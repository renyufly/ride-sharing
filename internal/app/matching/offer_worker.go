package matching

import (
	"context"
	"log"
	"sync"

	domaindispatch "ride-sharing/internal/domain/dispatch"
	domainmatching "ride-sharing/internal/domain/matching"
	"ride-sharing/internal/domain/order"
)

// OfferConfirmer 负责邀请一位骑手并等待回复。
type OfferConfirmer interface {
	Offer(
		ctx context.Context,
		currentOrder order.Order,
		riderUID uint64,
	) (domaindispatch.Attempt, error)

	NotifyAccepted(ctx context.Context, 
		attempt domaindispatch.Attempt) error
}

// offerJob : Worker 处理的邀请任务
type offerJob struct {
	order     order.Order
	candidate domainmatching.Candidate
}

type offerResult struct {
	job     offerJob
	attempt domaindispatch.Attempt
	err     error
	committed chan struct{}
}

type offerPool struct {
	jobs    chan offerJob
	results chan offerResult
	done    chan struct{}
}

func startOfferPool(
	ctx context.Context,
	confirmer OfferConfirmer,
	workerCount int,
	channelCapacity int,
) *offerPool {
	pool := &offerPool{
		jobs:    make(chan offerJob, channelCapacity),
		results: make(chan offerResult, channelCapacity),
		done:    make(chan struct{}),
	}

	var workers sync.WaitGroup
	workers.Add(workerCount)

	for i := 0; i < workerCount; i++ {
		go func() {
			defer workers.Done()
			offerWorker(ctx, confirmer, pool.jobs, pool.results)
		}()
	}

	go func() {
		workers.Wait()
		close(pool.results)
		close(pool.done)
	}()

	return pool
}

// stop 在coordinator已结束，或context已取消后调用。
func (p *offerPool) stop() {
	close(p.jobs)
	<-p.done
}

// offerWorker 与其他派单 worker 同时等待不同骑手回复。
// results 只由Coordinator读取；worker 不提交 Assignment。
func offerWorker(
	ctx context.Context,
	confirmer OfferConfirmer,
	jobs <-chan offerJob,
	results chan<- offerResult,
) {

	for {
		select {
		case <-ctx.Done():
			return

		case job, ok := <-jobs:
			if !ok {
				return
			}

			attempt, err := confirmer.Offer(
				ctx,
				job.order,
				job.candidate.RiderUID,
			)

			result := offerResult{
				job:     job,
				attempt: attempt,
				err:     err,
			}

			if err == nil && attempt.Status == domaindispatch.StatusAccepted {
				result.committed = make(chan struct{})
			}

			select {
			case results <- result:
			case <-ctx.Done():
				return
			}

			if result.committed != nil {
				select {
				case <-result.committed:
					if err := confirmer.NotifyAccepted(ctx, attempt); err != nil {
						log.Printf(
							"assignment confirmation failed for order %d, rider %d: %v",
							attempt.OrderID, attempt.RiderUID, err,
						)
					}
				case <-ctx.Done():
					return
				}
			}
		}
	}
}