package config

import (
	"strings"
	"testing"
	"time"

	"ride-sharing/internal/generator"
)

func TestParseDefaults(t *testing.T) {
	cfg, err := Parse(nil)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	want := Default()
	if cfg != want {
		t.Fatalf("Parse() = %#v, want %#v", cfg, want)
	}
}

func TestParseOverrides(t *testing.T) {
	cfg, err := Parse([]string{
		"--riders=12",
		"--orders=34",
		"--arrival-window=5s",
		"--arrival-model=front-loaded-burst",
		"--seed=99",
		"--rider-distribution=skewed",
		"--order-distribution=hotspot",
		"--algorithm=kd-tree",
		"--workers=2",
		"--batch-size=16",
		"--channel-capacity=4",
		"--strategy=nearest",
	})
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	if cfg.RiderCount != 12 || cfg.OrderCount != 34 || cfg.ArrivalWindow != 5*time.Second || cfg.Seed != 99 {
		t.Fatalf("Parse() basic overrides = %#v", cfg)
	}
	if cfg.ArrivalModel != generator.ArrivalFrontLoadedBurst || cfg.RiderDistribution != generator.DistributionSkewed || cfg.OrderDistribution != generator.DistributionHotspot {
		t.Fatalf("Parse() generator overrides = %#v", cfg)
	}
	if cfg.Algorithm != AlgorithmKDTree || cfg.Workers != 2 || cfg.BatchSize != 16 || cfg.ChannelCapacity != 4 {
		t.Fatalf("Parse() execution overrides = %#v", cfg)
	}
}

func TestParseRejectsInvalidConfiguration(t *testing.T) {
	_, err := Parse([]string{"--riders=0", "--strategy=balanced"})
	if err == nil {
		t.Fatal("Parse() error = nil, want validation error")
	}
	for _, message := range []string{"riders must be greater than zero", "unsupported strategy"} {
		if !strings.Contains(err.Error(), message) {
			t.Fatalf("Parse() error = %q, want it to contain %q", err, message)
		}
	}
}

func TestValidateKeepsStrategyBDisabled(t *testing.T) {
	cfg := Default()
	cfg.TopK = 4

	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "strategy B options") {
		t.Fatalf("Validate() error = %v, want strategy B guard", err)
	}
}

func TestParseRejectsUnknownGeneratorModes(t *testing.T) {
	_, err := Parse([]string{
		"--arrival-model=random",
		"--rider-distribution=everywhere",
		"--order-distribution=nowhere",
	})
	if err == nil {
		t.Fatal("Parse() error = nil, want generator mode errors")
	}
	for _, message := range []string{"unsupported arrival model", "rider distribution", "order distribution"} {
		if !strings.Contains(err.Error(), message) {
			t.Fatalf("Parse() error = %q, want it to contain %q", err, message)
		}
	}
}
