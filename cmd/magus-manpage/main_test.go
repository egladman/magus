package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	imanpage "github.com/egladman/magus/internal/manpage"
)

// TestWriteSeeAlsoMDTrimsPerSubcommandPage is the fix for the link-audit
// finding that every generated man page carried an identical, exhaustive
// See Also listing all 18 commands (noise, no concept links). A
// per-subcommand page must link the umbrella page plus its curated related
// commands only - not every sibling command.
func TestWriteSeeAlsoMDTrimsPerSubcommandPage(t *testing.T) {
	var m mdBuf
	writeSeeAlsoMD(&m, "run")
	out := string(m.bytes())

	assert.Contains(t, out, "## See Also\n\n")
	assert.Contains(t, out, "[**magus**(1)](magus.md)")
	for _, name := range relatedCommands["run"] {
		assert.Contains(t, out, "magus-"+name+".md", "run's See Also must link related command %q", name)
	}
	// Not every command - "man" is unrelated to run and must be absent.
	assert.NotContains(t, out, "magus-man.md")
}

// TestWriteSeeAlsoMDKeepsFullListOnUmbrellaPage verifies requirement 4: the
// umbrella magus(1) page IS the command index, so it keeps the full
// exhaustive list rather than being trimmed like a subcommand page.
func TestWriteSeeAlsoMDKeepsFullListOnUmbrellaPage(t *testing.T) {
	var m mdBuf
	writeSeeAlsoMD(&m, "")
	out := string(m.bytes())

	for _, seg := range imanpage.All {
		assert.Contains(t, out, "magus-"+seg.Name+".md", "umbrella page must still link %q", seg.Name)
	}
}

// TestWriteSeeAlsoMDRendersConcepts checks the new "Concepts" section (md
// format only) links the curated concept pages for a command that has them,
// and is omitted entirely for a command that doesn't.
func TestWriteSeeAlsoMDRendersConcepts(t *testing.T) {
	var m mdBuf
	writeSeeAlsoMD(&m, "run")
	out := string(m.bytes())
	assert.Contains(t, out, "## Concepts\n\n")
	assert.Contains(t, out, "[Targets](../../concepts/targets.md)")
	assert.Contains(t, out, "[Charms](../../concepts/charms.md)")
	assert.Contains(t, out, "[Cache](../../concepts/cache.md)")

	var m2 mdBuf
	writeSeeAlsoMD(&m2, "completion")
	out2 := string(m2.bytes())
	assert.NotContains(t, out2, "## Concepts", "completion has no curated concept pairing")
}

// TestRelatedCommandsReferenceRealCommands guards against a typo in
// relatedCommands silently linking to a man page that is never generated.
func TestRelatedCommandsReferenceRealCommands(t *testing.T) {
	names := make(map[string]bool, len(imanpage.All))
	for _, seg := range imanpage.All {
		names[seg.Name] = true
	}
	for cmd, related := range relatedCommands {
		assert.True(t, names[cmd], "relatedCommands key %q is not a registered command", cmd)
		for _, r := range related {
			assert.True(t, names[r], "relatedCommands[%q] references unregistered command %q", cmd, r)
			assert.NotEqual(t, cmd, r, "relatedCommands[%q] lists itself", cmd)
		}
	}
	for cmd := range commandConcepts {
		assert.True(t, names[cmd], "commandConcepts key %q is not a registered command", cmd)
	}
}

// TestRenderCommandMDIncludesSeeAlsoAndConcepts is an end-to-end check that
// renderCommandMD (the real per-page entry point) wires writeSeeAlsoMD's
// output into the page, not just the helper in isolation.
func TestRenderCommandMDIncludesSeeAlsoAndConcepts(t *testing.T) {
	var runSeg imanpage.Command
	for _, seg := range imanpage.All {
		if seg.Name == "run" {
			runSeg = seg
		}
	}
	out := string(renderCommandMD(runSeg))
	assert.True(t, strings.Contains(out, "## See Also"))
	assert.True(t, strings.Contains(out, "## Concepts"))
	assert.True(t, strings.Contains(out, "[**magus**(1)](magus.md)"))
}
