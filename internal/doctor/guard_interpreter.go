package doctor

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/egladman/magus/internal/agent"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/types"
)

// ownBinaryToken is how a committed hook command names the workspace's own binary.
const ownBinaryToken = "./magus"

// configsNamingOwnBinary lists the hook configs whose command runs ./magus.
//
// A hook command names its interpreter literally, chosen when the harness was applied, and
// nothing re-checks it afterwards. A checkout that loses ./magus has every such hook fail
// before a line of glue runs, which is the one breakage no notice arm inside the glue can
// report, and resolving ./magus-then-PATH the way the scripts do grades it healthy.
//
// Reads the descriptors rather than the verified inventory: verification runs the wired
// command, so it goes quiet in precisely the state this looks for.
func configsNamingOwnBinary(ctx context.Context, root string, wired ...string) []string {
	var out []string
	for _, cfg := range agent.HarnessConfigPaths(ctx, root, wired...) {
		body, err := os.ReadFile(cfg)
		if err != nil {
			continue
		}
		if bytes.Contains(body, []byte(ownBinaryToken)) {
			out = append(out, cfg)
		}
	}
	return out
}

// interpreterProbeTimeout bounds `<interpreter> version`. Far under the host's 10 s per
// hook: a binary that cannot print its version in this long is the hang being diagnosed.
const interpreterProbeTimeout = 3 * time.Second

// interpreterSkew fails when the magus a hook would run as its interpreter is a different
// build from this one, or cannot say which build it is.
//
// The interpreter is the half the verdict binary's freshness says nothing about: a stale
// one never reaches the glue that would name it, so the hook hangs or errors and the host
// runs the call unjudged. A FAIL, because the wiring is not honored as written.
//
// Returns false when there is nothing to compare: this binary's own version is unknown.
func interpreterSkew(ctx context.Context, name, bin, own string) (types.Check, bool) {
	if own == "" {
		return types.Check{}, false
	}
	probeCtx, cancel := context.WithTimeout(ctx, interpreterProbeTimeout)
	defer cancel()
	out, err := exec.CommandContext(probeCtx, bin, "version").Output()
	// `magus <version> (<commit>) built <date>`, the first line every release has printed.
	fields := strings.Fields(strings.SplitN(string(out), "\n", 2)[0])
	if err != nil || len(fields) < 2 || fields[0] != "magus" {
		reason := "printed no version"
		if err != nil {
			reason = err.Error()
		}
		return types.Check{
			Name:    name,
			Status:  types.CheckFail,
			Message: "the hook interpreter " + bin + " did not answer `version` (" + reason + "), so no guard hook it runs can render a verdict",
			Details: []string{"rebuild: " + hint.Run.With("build", ".")},
		}, true
	}
	if fields[1] == own {
		return types.Check{}, false
	}
	return types.Check{
		Name:   name,
		Status: types.CheckFail,
		Message: fmt.Sprintf("the hook interpreter is %s (%s) and this magus is %s, so hooks run a build other than the one checked here",
			bin, fields[1], own),
		Details: []string{
			"a hook runs the session root's ./magus when it is executable, else the first magus on PATH",
			"rebuild ./magus (" + hint.Run.With("build", ".") + "), or put the right magus first on PATH; a live session keeps the binary it started with",
		},
	}, true
}
