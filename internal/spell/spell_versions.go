package spell

import (
	"context"
	"fmt"
	"io"

	"github.com/egladman/magus/project"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
)

// ProbeSpellVersions EXECUTES a spell's version probes and reports what they return
// today, alongside the cache-key fragment each produces.
//
// Declared and observed are different questions, and only the second one debugs
// anything. The existing output answers the first with a bare boolean, which says a
// probe exists and nothing about what it reports, so a toolchain that has drifted
// from what the project pins is invisible even though its value is sitting in every
// cache key. It is also the first thing to check for MGS1009 (a target that never
// replays), because a probe returning a moving value moves the key with it.
//
// Never cached. A probe's whole job is to observe what is installed right now, and a
// cached probe's failure mode is a wrong cache HIT, which trades away the guarantee
// the probe exists to provide.
func ProbeSpellVersions(ctx context.Context, name, dir string) []types.SpellVersion {
	sp, ok := project.DefaultSpellRegistry().Lookup(name)
	if !ok || !sp.HasVersionProbe() {
		return nil
	}
	var out []types.SpellVersion
	// One uniform pass: every declared binary keys as spell:tool:version, with no
	// privileged primary. A tool with a constant version reports it without spawning.
	for _, tool := range sp.ToolNames() {
		t, _ := sp.Tool(tool)
		if !t.HasProbe() {
			continue
		}
		if t.Probe.Bin == "" {
			out = append(out, types.SpellVersion{
				Tool: tool, Version: t.Key.Const,
				CacheKey: fmt.Sprintf("spell:%s:version=%s", tool, t.Key.Const),
			})
			continue
		}
		v, err := sp.ProbeVersion(ctx, tool, dir)
		switch {
		case err != nil:
			out = append(out, types.SpellVersion{Tool: tool, Error: err.Error()})
		case v == "":
			out = append(out, types.SpellVersion{Tool: tool})
		default:
			token, _ := spells.VersionToken(v, t.Key)
			out = append(out, types.SpellVersion{
				Tool: tool, Version: v,
				CacheKey: fmt.Sprintf("spell:%s:version=%s", tool, token),
			})
		}
	}
	return out
}

// PrintSpellVersions renders probe results for the text formats.
func PrintSpellVersions(w io.Writer, vs []types.SpellVersion) {
	if len(vs) == 0 {
		return
	}
	fmt.Fprintf(w, "    versions (probed just now, never cached):\n")
	for _, v := range vs {
		switch {
		case v.Error != "":
			fmt.Fprintf(w, "      %-16s probe failed: %s\n", v.Tool, v.Error)
		case v.Version == "":
			fmt.Fprintf(w, "      %-16s (no version reported)\n", v.Tool)
		default:
			fmt.Fprintf(w, "      %-16s %s\n", v.Tool, v.Version)
			fmt.Fprintf(w, "      %-16s key: %s\n", "", v.CacheKey)
		}
	}
}
