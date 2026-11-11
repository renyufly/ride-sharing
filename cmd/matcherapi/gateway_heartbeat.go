package main

import (
	"context"
	"log"
	"time"

	"ride-sharing/internal/adapter/mongostore"
)

const (
	gatewayHeartbeatInterval = 2 * time.Second
	gatewayLeaseDuration     = 6 * time.Second
	gatewayWriteTimeout      = 2 * time.Second
)

func startGatewayHeartbeat(
	ctx context.Context, store *mongostore.OrderStore, gatewayID string,
) (<-chan struct{}, error) {
	renew := func() error {
		writeCtx, cancel := context.WithTimeout(ctx, gatewayWriteTimeout)
		defer cancel()
		return store.RenewGateway(writeCtx, gatewayID, gatewayLeaseDuration)
	}

	if err := renew(); err != nil {
		return nil, err
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		defer func() {
			cleanupCtx, cancel := context.WithTimeout(
				context.Background(), gatewayWriteTimeout,
			)
			defer cancel()
			if err := store.RemoveGateway(cleanupCtx, gatewayID); err != nil {
				log.Printf("remove gateway lease %s: %v", gatewayID, err)
			}
		}()

		ticker := time.NewTicker(gatewayHeartbeatInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := renew(); err != nil && ctx.Err() == nil {
					log.Printf("renew gateway lease %s: %v", gatewayID, err)
				}
			}
		}
	}()

	return done, nil
}