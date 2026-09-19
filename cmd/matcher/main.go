// Command matcher is the standalone entry point for the matching exercise.
//
// Step one only parses and validates configuration. Matching is deliberately
// not started until the generator and baseline matcher are implemented in
// later steps.
package main

import (
	"encoding/json"
	"fmt"
	"os"

	"ride-sharing/internal/config"
)

type startupOutput struct {
	Phase   string               `json:"phase"`
	Message string               `json:"message"`
	Config  config.DisplayConfig `json:"config"`
}

func main() {
	cfg, err := config.Parse(os.Args[1:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid matcher configuration: %v\n", err)
		os.Exit(2)
	}

	output := startupOutput{
		Phase:   "skeleton",
		Message: "configuration accepted; matching is not implemented in step one",
		Config:  cfg.Display(),
	}

	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(output); err != nil {
		fmt.Fprintf(os.Stderr, "write startup output: %v\n", err)
		os.Exit(1)
	}
}
