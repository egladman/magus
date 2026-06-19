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

var raceFormatHelp = "Race-condition diagnostics (" + strings.Join(raceModes, "|") + ", comma-combinable); omit to disable. " +
	"watch: attribution-gated fsnotify detection (MGS4001/4002/4004); emits only when ≥2 projects' output snapshots confirm a shared write; near-zero false positives. " +
	"replay: re-runs cacheable output-declaring projects sequentially to content-hash outputs for non-determinism (MGS4003); roughly doubles wall-clock. " +
	"Combine with --race=watch,replay for all four codes."
