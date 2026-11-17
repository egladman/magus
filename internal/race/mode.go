package race

import (
	"fmt"
	"strings"
)

const (
	modeWatch  = "watch"
	modeReplay = "replay"
)

// modes lists the accepted --race values.
var modes = []string{modeWatch, modeReplay}

// Options is the parsed --race value; Replay additionally re-runs projects to detect non-determinism.
type Options struct {
	Enabled bool
	Replay  bool
}

// Resolve validates --race (empty = disabled); modes are comma-combinable.
func Resolve(input string) (Options, error) {
	if input == "" {
		return Options{}, nil
	}
	var opts Options
	for _, part := range strings.Split(input, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue // tolerate "watch," and ",replay"
		}
		switch part {
		case modeWatch:
			opts.Enabled = true
		case modeReplay:
			opts.Replay = true
		default:
			return Options{}, fmt.Errorf("unknown race mode %q (choose: %s)",
				part, strings.Join(modes, ", "))
		}
	}
	return opts, nil
}
