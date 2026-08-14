package main

import (
	"fmt"
	"strings"
)

const (
	raceWatch  = "watch"
	raceReplay = "replay"
)

var raceModes = []string{raceWatch, raceReplay}

// raceOptions is the parsed --race value; Replay additionally re-runs projects to detect non-determinism.
type raceOptions struct {
	Enabled bool
	Replay  bool
}

// resolveRace validates --race (empty = disabled); modes are comma-combinable.
func resolveRace(input string) (raceOptions, error) {
	if input == "" {
		return raceOptions{}, nil
	}
	var opts raceOptions
	for _, part := range strings.Split(input, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue // tolerate "watch," and ",replay"
		}
		switch part {
		case raceWatch:
			opts.Enabled = true
		case raceReplay:
			opts.Replay = true
		default:
			return raceOptions{}, fmt.Errorf("unknown race mode %q (choose: %s)",
				part, strings.Join(raceModes, ", "))
		}
	}
	return opts, nil
}
