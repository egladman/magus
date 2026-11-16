package agent

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/types"
	typesagent "github.com/egladman/magus/types/agent"
)

// vcsSafetyRule is the one always-on version-control rule worth carrying in a
// repo's agent instruction file: it stops one agent's whole-tree revert from destroying
// another's uncommitted work. Shared by the install hint and the starter doc.
const vcsSafetyRule = "Version control is the orchestrator's job: do it yourself, never delegate it to a subagent, and never discard or revert uncommitted changes across the whole tree to verify a build - build in place. A whole-tree revert permanently destroys a concurrent agent's uncommitted work."

// InstallOutput is the record `magus agent install` emits under -o.
type InstallOutput struct {
	DryRun  bool     `json:"dry_run"`
	Written []string `json:"written"`
	Changed []string `json:"changed"`
	Removed []string `json:"removed"`
}

// NonNil turns a nil slice into an empty one, so a record encodes [] and never null.
func NonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// ReportRemovedSkills prints one line per removal on stdout, so a delete shows at the
// default log level. A silent delete is how a person loses a skill they thought they
// had. Both installers share it so the wording stays one.
func ReportRemovedSkills(w io.Writer, removed []string, dryRun bool) {
	verb := "removed"
	if dryRun {
		verb = "would remove"
	}
	for _, p := range removed {
		fmt.Fprintf(w, "%s skill this binary no longer ships: %s\n", verb, p)
	}
}

// InstallSkillTree writes one skill tree, then removes the skill directories this
// binary no longer ships. Only directories carrying magus's stamp are candidates
// (see Catalog.StaleSkillDirs), so a hand-authored skill beside them survives.
// Under dryRun it touches nothing and returns what a run would write and remove.
func (c *Catalog) InstallSkillTree(base, leaf string, force, dryRun bool, form types.SkillForm) (written, changed, removed []string, err error) {
	if dryRun {
		if written, err = c.PlanSkillTree(base, leaf, form); err != nil {
			return nil, nil, nil, err
		}
		removed, err = c.StaleSkillDirs(base, leaf, form)
		return written, nil, removed, err
	}
	if written, changed, err = c.WriteSkillTree(base, leaf, force, form); err != nil {
		return nil, nil, nil, err
	}
	// After writing, never before: a prune that ran first would delete a skill
	// this install then failed to replace.
	removed, err = c.PruneSkillTree(base, leaf, form)
	return written, changed, removed, err
}

// InstallHints is what an install tells the reader to do next, one line per hint, in the
// order they print. Empty when nothing was written.
func (c *Catalog) InstallHints(dir string, written, changed []string, form types.SkillForm, dryRun bool) []string {
	if len(written) == 0 {
		return nil
	}
	// A rehearsal that claims it installed something stops the reader looking for
	// the real run.
	if dryRun {
		return []string{fmt.Sprintf("dry run: %d file(s) would be written; nothing was changed. Re-run without --dry-run to apply", len(written))}
	}
	var hints []string
	// What the run DID, not how many files it touched. A --force reinstall rewrites every
	// file whether or not its bytes moved, so "installed 30 file(s)" is the same sentence
	// on the run that changed everything and the run that changed nothing. A reader who
	// sees it three times stops reading it, which is the run where it finally mattered.
	switch {
	case len(changed) == 0:
		hints = append(hints, fmt.Sprintf("%d skill file(s) were already current; nothing changed", len(written)))
	case len(changed) == len(written):
		hints = append(hints, fmt.Sprintf("wrote all %d skill file(s); commit them so your team and agents share them", len(written)))
	default:
		hints = append(hints, fmt.Sprintf("updated %d of %d skill file(s), the rest already current; commit them so your team and agents share them: %s",
			len(changed), len(written), strings.Join(skillNames(changed), ", ")))
	}
	if cost := c.ContextCost(dir, written); cost != "" {
		hints = append(hints, cost)
	}
	if form == types.SkillFormBoth {
		hints = append(hints, "both installs the short body under each skill's own name plus an always-full <name>-full reference; pass --skill-form=short or --skill-form=full when one body per skill is required")
	}
	// MAGUS.md is regenerated for HUMAN readers; the skills send agents to the live
	// verbs instead, because a generated index is only true as of its last run.
	return append(hints,
		fmt.Sprintf("regenerate MAGUS.md for human readers:  %s  (the skills send agents to the live verbs: %s, %s)", hint.DescribeGraph.With("-o", "markdown"), hint.DescribeTargets.String(), hint.Ls.String()),
		"safety: consider a line in your repo's agent instruction file so parallel agents cannot wipe each other's work:",
		"  \""+vcsSafetyRule+"\"",
		"starter AGENTS.md you can own and tweak (prints, never writes):  "+hint.AgentStarter.String())
}

// skillNames reduces <dest>-relative SKILL.md paths to the skill names a reader
// recognizes: ".claude/skills/magus-run/SKILL.md" is the file, "magus-run" is the thing.
func skillNames(paths []string) []string {
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		out = append(out, filepath.Base(filepath.Dir(p)))
	}
	slices.Sort(out)
	return out
}

// PasteOffer is the managed AGENTS.md block offered for the developer to paste, with the
// sentence that introduces it. ok is false when their file already carries a current one:
// 80 lines of Markdown on every --force reinstall is how a reader learns to scroll past
// this command's output, including the actionable parts.
func (c *Catalog) PasteOffer(ctx context.Context, dir string, routingIndex bool) (intro, block string, ok bool) {
	verb := "add it to AGENTS.md at your repo root"
	// No workspace is open at this point in `agent install` (dir is an explicit CLI
	// argument, not a discovered root), so there is no wired harness list to pass; the
	// AGENTS.md staleness check below does not need one anyway.
	for _, s := range c.CheckStatuses(ctx, dir) {
		if s.Location != AgentsFile {
			continue
		}
		if !s.Stale {
			return "", "", false
		}
		verb = "your AGENTS.md has an older copy: replace it BETWEEN the markers and leave the rest of the file alone"
	}
	return fmt.Sprintf("magus does not write AGENTS.md. That file is yours. If your agent host reads it, %s:", verb), c.AgentsBlock(routingIndex), true
}

// DeclaresRoutingIndex reports whether the workspace rooted at dir declares MAGUS.md as an
// output of its root project. Any failure reads as false: a block that omits the link is
// correct for a workspace that may not exist yet, while a link to a missing file is not.
//
// dir must BE the workspace root. AGENTS.md is pasted at dir, so a root found above it
// would make the block's relative link point at a file that is not beside it.
func DeclaresRoutingIndex(ctx context.Context, dir string, findRoot func(string) (string, error), rootOutputs func(ctx context.Context, root string) ([]types.Glob, error)) bool {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return false
	}
	root, err := findRoot(abs)
	if err != nil || root != abs {
		return false
	}
	outputs, err := rootOutputs(ctx, root)
	if err != nil {
		return false
	}
	return slices.ContainsFunc(outputs, func(g types.Glob) bool { return g.Pattern == "MAGUS.md" })
}

// ContextCost tells the caller how many bytes of instruction the install
// just added, and what the other form would have cost. Empty when nothing always-loaded
// was written.
//
// BYTES, not tokens: a token count is only true for one tokenizer, and these
// files are installed for whatever host the reader uses. Printed at all for
// accountability: an install that never states its own cost has no pressure on
// it to shrink.
func (c *Catalog) ContextCost(dir string, written []string) string {
	// Twins are counted separately, not folded in: only the primary is
	// always-loaded, and a twin is a reference copy fetched by name when a reader
	// needs the long form. Summing them would report the always-loaded cost as
	// roughly double what a session actually carries.
	var installed int64
	var twins int64
	for _, rel := range written {
		info, err := os.Stat(filepath.Join(dir, rel))
		if err != nil {
			continue
		}
		if IsFullTwinName(filepath.Base(filepath.Dir(rel))) {
			twins += info.Size()
			continue
		}
		installed += info.Size()
	}
	if installed == 0 {
		return ""
	}
	msg := fmt.Sprintf("context cost: %s of always-loaded instructions", byteSize(installed))
	// What the always-loaded set would cost if the long form were the one carried. Kept
	// even though it is no longer a flag to choose between, because it is the number that
	// justifies the split: without it the reader cannot see what the shorter form buys.
	if alt, err := c.VariantSize(typesagent.SkillVariantFull); err == nil && alt > installed {
		msg += fmt.Sprintf("; the full form would be %s", byteSize(alt))
	}
	if twins > 0 {
		msg += fmt.Sprintf("; plus %s of full twins, loaded only when asked for by name", byteSize(twins))
	}
	return msg
}

// byteSize renders a size the way a reader compares it, not the way a machine
// stores it.
func byteSize(n int64) string {
	if n < 1024 {
		return fmt.Sprintf("%d B", n)
	}
	return fmt.Sprintf("%.1f KB", float64(n)/1024)
}

// SkillTarget resolves a destination into the (base, leaf) pair the catalog
// writers take.
//
// --global is the flag that permits an absolute destination, and it never
// worked: this command let one past its own guard and Catalog.WriteSkillTree
// then refused it unconditionally, so `agent install /abs/path --global` failed
// with the message telling the caller to pass the flag they had just passed.
//
// Fixed HERE rather than by adding an escape hatch to the catalog. That guard is
// the library's own safety property (it also blocks "../../outside", which no
// CLI flag should be able to switch off), so instead the absolute path is split
// into the directory it names and the leaf inside it. The containment check the
// catalog performs is then still meaningful: the write stays under the directory
// the caller actually named.
func SkillTarget(dir, dest string, global bool) (base, leaf string) {
	if global && filepath.IsAbs(dest) {
		return filepath.Dir(dest), filepath.Base(dest)
	}
	return dir, dest
}
