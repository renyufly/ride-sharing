package dispatch

import (
	"context"
	"ride-sharing/internal/domain/matching"
	"ride-sharing/internal/domain/order"
)

// internal\adapter\spatial\kdtree\matcher.go
type CandidateFinder interface {
	TopKInto(order.Order, int, []matching.Candidate) ([]matching.Candidate, error)
}

type Offer struct {
	Order     order.Order
	RequestID string
}


type Sender interface {
	SendOffer(ctx context.Context, riderUID uint64, offer Offer) error
}