package config

import (
	"fmt"
	"os"
	"strconv"
)

type DispatchRuntime struct {
	SchedulerWorkers int
	DeliveryWorkers  int
}

func LoadDispatchRuntime() (DispatchRuntime, error) {
	scheduler, err := positiveEnv("MATCHER_SCHEDULER_WORKERS", 2)
	if err != nil {
		return DispatchRuntime{}, err
	}
	delivery, err := positiveEnv("MATCHER_DELIVERY_WORKERS", 1)
	if err != nil {
		return DispatchRuntime{}, err
	}
	return DispatchRuntime{
		SchedulerWorkers: scheduler,
		DeliveryWorkers:  delivery,
	}, nil
}

func positiveEnv(name string, fallback int) (int, error) {
	raw, exists := os.LookupEnv(name)
	if !exists {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 1 {
		return 0, fmt.Errorf("%s must be a positive integer", name)
	}
	return value, nil
}