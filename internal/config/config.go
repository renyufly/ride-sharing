// Package config owns the standalone matcher's runtime configuration.
package config

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"time"

	"ride-sharing/internal/generator"
)

type Algorithm string

const (
	AlgorithmBruteForce Algorithm = "brute-force"
	AlgorithmKDTree     Algorithm = "kd-tree"
)

type Strategy string

const (
	StrategyNearest Strategy = "nearest"
)

// Config contains the complete planned runtime surface. TopK and
// MaxExtraDistanceMeters are reserved for strategy B and must remain disabled
// while only the nearest-rider strategy is available.
type Config struct {
	RiderCount             int
	OrderCount             int
	ArrivalWindow          time.Duration
	RunTimeout             time.Duration
	ArrivalModel           generator.ArrivalModel
	Seed                   int64
	RiderDistribution      generator.SpatialDistribution
	OrderDistribution      generator.SpatialDistribution
	Algorithm              Algorithm
	Workers                int
	BatchSize              int
	ChannelCapacity        int
	Strategy               Strategy
	TopK                   int
	MaxExtraDistanceMeters float64
}

// DisplayConfig is the stable, human-readable representation printed at
// startup. Durations are strings so a run can be copied into an interview or
// benchmark report without converting nanoseconds.
type DisplayConfig struct {
	RiderCount             int     `json:"riderCount"`
	OrderCount             int     `json:"orderCount"`
	ArrivalWindow          string  `json:"arrivalWindow"`
	RunTimeout             string  `json:"runTimeout"`
	ArrivalModel           string  `json:"arrivalModel"`
	Seed                   int64   `json:"seed"`
	RiderDistribution      string  `json:"riderDistribution"`
	OrderDistribution      string  `json:"orderDistribution"`
	Algorithm              string  `json:"algorithm"`
	Workers                int     `json:"workers"`
	BatchSize              int     `json:"batchSize"`
	ChannelCapacity        int     `json:"channelCapacity"`
	Strategy               string  `json:"strategy"`
	TopK                   int     `json:"topK"`
	MaxExtraDistanceMeters float64 `json:"maxExtraDistanceMeters"`
}

func Default() Config {
	return Config{
		RiderCount:        100,
		OrderCount:        10_000,
		ArrivalWindow:     30 * time.Second,
		RunTimeout:        0,
		ArrivalModel:      generator.ArrivalUniformWindow,
		Seed:              42,
		RiderDistribution: generator.DistributionUniform,
		OrderDistribution: generator.DistributionUniform,
		Algorithm:         AlgorithmKDTree,
		Workers:           2,
		BatchSize:         256,
		ChannelCapacity:   16,
		Strategy:          StrategyNearest,
	}
}

// Parse reads command-line arguments without using the process-global FlagSet,
// which keeps configuration tests isolated and deterministic.
func Parse(args []string) (Config, error) {
	cfg := Default()
	flags := flag.NewFlagSet("matcher", flag.ContinueOnError)
	flags.SetOutput(io.Discard)

	algorithm := string(cfg.Algorithm)
	arrivalModel := string(cfg.ArrivalModel)
	riderDistribution := string(cfg.RiderDistribution)
	orderDistribution := string(cfg.OrderDistribution)
	strategy := string(cfg.Strategy)
	flags.IntVar(&cfg.RiderCount, "riders", cfg.RiderCount, "number of riders")
	flags.IntVar(&cfg.OrderCount, "orders", cfg.OrderCount, "number of orders")
	flags.DurationVar(&cfg.ArrivalWindow, "arrival-window", cfg.ArrivalWindow, "order arrival window")
	flags.DurationVar(&cfg.RunTimeout, "timeout", cfg.RunTimeout, "whole-run timeout; zero disables the deadline")
	flags.StringVar(&arrivalModel, "arrival-model", arrivalModel, "arrival model: uniform-window, front-loaded-burst, or unbounded")
	flags.Int64Var(&cfg.Seed, "seed", cfg.Seed, "deterministic random seed")
	flags.StringVar(&riderDistribution, "rider-distribution", riderDistribution, "rider distribution: uniform, hotspot, or skewed")
	flags.StringVar(&orderDistribution, "order-distribution", orderDistribution, "order distribution: uniform, hotspot, or skewed")
	flags.StringVar(&algorithm, "algorithm", algorithm, "matching algorithm: brute-force or kd-tree")
	flags.IntVar(&cfg.Workers, "workers", cfg.Workers, "matching worker count")
	flags.IntVar(&cfg.BatchSize, "batch-size", cfg.BatchSize, "orders per batch")
	flags.IntVar(&cfg.ChannelCapacity, "channel-capacity", cfg.ChannelCapacity, "bounded batch channel capacity")
	flags.StringVar(&strategy, "strategy", strategy, "matching strategy (step one supports nearest only)")

	if err := flags.Parse(args); err != nil {
		return Config{}, err
	}
	if flags.NArg() != 0 {
		return Config{}, fmt.Errorf("unexpected positional arguments: %v", flags.Args())
	}

	cfg.Algorithm = Algorithm(algorithm)
	cfg.ArrivalModel = generator.ArrivalModel(arrivalModel)
	cfg.RiderDistribution = generator.SpatialDistribution(riderDistribution)
	cfg.OrderDistribution = generator.SpatialDistribution(orderDistribution)
	cfg.Strategy = Strategy(strategy)
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) Validate() error {
	var errs []error
	if c.RiderCount <= 0 {
		errs = append(errs, errors.New("riders must be greater than zero"))
	}
	if c.OrderCount <= 0 {
		errs = append(errs, errors.New("orders must be greater than zero"))
	}
	if c.ArrivalWindow < 0 {
		errs = append(errs, errors.New("arrival window cannot be negative"))
	}
	if c.RunTimeout < 0 {
		errs = append(errs, errors.New("run timeout cannot be negative"))
	}
	if err := generator.ValidateArrivalModel(c.ArrivalModel); err != nil {
		errs = append(errs, err)
	}
	if err := generator.ValidateSpatialDistribution(c.RiderDistribution); err != nil {
		errs = append(errs, fmt.Errorf("rider distribution: %w", err))
	}
	if err := generator.ValidateSpatialDistribution(c.OrderDistribution); err != nil {
		errs = append(errs, fmt.Errorf("order distribution: %w", err))
	}
	if c.Algorithm != AlgorithmBruteForce && c.Algorithm != AlgorithmKDTree {
		errs = append(errs, fmt.Errorf("unsupported algorithm %q", c.Algorithm))
	}
	if c.Workers <= 0 {
		errs = append(errs, errors.New("workers must be greater than zero"))
	}
	if c.BatchSize <= 0 {
		errs = append(errs, errors.New("batch size must be greater than zero"))
	}
	if c.ChannelCapacity <= 0 {
		errs = append(errs, errors.New("channel capacity must be greater than zero"))
	}
	if c.Strategy != StrategyNearest {
		errs = append(errs, fmt.Errorf("unsupported strategy %q: step one enables nearest only", c.Strategy))
	}
	if c.TopK != 0 || c.MaxExtraDistanceMeters != 0 {
		errs = append(errs, errors.New("strategy B options must remain disabled until strategy A is complete"))
	}
	return errors.Join(errs...)
}

func (c Config) Display() DisplayConfig {
	return DisplayConfig{
		RiderCount:             c.RiderCount,
		OrderCount:             c.OrderCount,
		ArrivalWindow:          c.ArrivalWindow.String(),
		RunTimeout:             c.RunTimeout.String(),
		ArrivalModel:           string(c.ArrivalModel),
		Seed:                   c.Seed,
		RiderDistribution:      string(c.RiderDistribution),
		OrderDistribution:      string(c.OrderDistribution),
		Algorithm:              string(c.Algorithm),
		Workers:                c.Workers,
		BatchSize:              c.BatchSize,
		ChannelCapacity:        c.ChannelCapacity,
		Strategy:               string(c.Strategy),
		TopK:                   c.TopK,
		MaxExtraDistanceMeters: c.MaxExtraDistanceMeters,
	}
}
