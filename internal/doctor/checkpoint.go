package doctor

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/types"
)

func (r *runner) checkCheckpointWiring() types.Check {
	return checkCheckpointWiring(r.ws.Root(), workspaceHarnesses(r.ws)...)
}

// checkCheckpointWiring reports a host that magus is wired into but that records no
// checkpoint, so nothing says where the work stood when a session stopped.
//
// It asks only of a checkout that ALREADY has a host hook config. A machine with no host
// wiring at all is guard-wiring's finding, and repeating it here would be a second
// advisory about the same absence. What this catches is the narrower and quieter case: a
// host set up months ago, guarding correctly, silently recording nothing.
//
// Advice rather than a failure. Not every workspace wants this wired, and a doctor that
// fails over an optional hook teaches people to stop reading it.
func checkCheckpointWiring(root string, wired ...string) types.Check {
	const name = "checkpoint-wiring"

	var hosts, recording []string
	for _, path := range HookConfigs(context.Background(), root, wired...) {
		body, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		hosts = append(hosts, path)
		if configRecordsCheckpoint(root, body) {
			recording = append(recording, path)
		}
	}

	switch {
	case len(hosts) == 0:
		return types.Check{
			Name:     name,
			Status:   types.CheckOK,
			Evidence: types.EvidenceUnknown,
			Message:  "no agent-host hook config in this checkout, so there is nothing to record from; skipped",
		}
	case len(recording) == 0:
		return types.Check{
			Name:    name,
			Status:  types.CheckAdvice,
			Message: fmt.Sprintf("%d host hook config(s) wired, none recording a checkpoint; nothing says where work stood when a session stopped", len(hosts)),
			Details: append(append([]string{}, hosts...),
				"wire a stop hook to "+checkpointTemplate+": docs/guides/integrations/agents.md",
				"or record one by hand: "+hint.SessionCheckpoint.With(`--note "..."`)),
		}
	default:
		return types.Check{
			Name:    name,
			Status:  types.CheckOK,
			Message: fmt.Sprintf("%d of %d host hook config(s) record a checkpoint", len(recording), len(hosts)),
			Details: recording,
		}
	}
}

// configRecordsCheckpoint reports whether a host hook config (or a workspace script
// it names) actually records a session checkpoint. Cursor embeds the call inside
// cursor-hook.sh rather than spelling "checkpoint" in hooks.json; looking only at
// the JSON body falsely grades that host as silent.
func configRecordsCheckpoint(root string, body []byte) bool {
	if bytes.Contains(body, []byte("checkpoint")) {
		return true
	}
	var decoded any
	if err := json.Unmarshal(body, &decoded); err != nil {
		return false
	}
	for _, cmd := range collectJSONStringFields(decoded, "command") {
		if strings.Contains(cmd, "checkpoint") {
			return true
		}
		for _, rel := range shellScriptPaths(cmd) {
			script, err := os.ReadFile(filepath.Join(root, rel))
			if err != nil {
				continue
			}
			if bytes.Contains(script, []byte("session checkpoint")) ||
				bytes.Contains(script, []byte(checkpointTemplate)) {
				return true
			}
		}
	}
	return false
}

func collectJSONStringFields(v any, key string) []string {
	var out []string
	switch t := v.(type) {
	case map[string]any:
		for k, child := range t {
			if k == key {
				if s, ok := child.(string); ok && s != "" {
					out = append(out, s)
				}
			}
			out = append(out, collectJSONStringFields(child, key)...)
		}
	case []any:
		for _, child := range t {
			out = append(out, collectJSONStringFields(child, key)...)
		}
	}
	return out
}

// shellScriptPaths returns workspace-relative .sh operands from a hook command line
// (e.g. `sh docs/guides/integrations/agents/cursor-hook.sh`).
func shellScriptPaths(cmd string) []string {
	var out []string
	for _, field := range strings.Fields(cmd) {
		clean := strings.Trim(field, `"'`)
		if strings.HasSuffix(clean, ".sh") && !filepath.IsAbs(clean) {
			out = append(out, filepath.Clean(clean))
		}
	}
	return out
}

// checkpointTemplate is the shipped stop-hook script, named here so the check can spot a
// config that runs it. A path, which is the one host-specific shape magus owns.
const checkpointTemplate = "magus-checkpoint.sh"
