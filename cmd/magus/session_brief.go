package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/egladman/magus/internal/agent"
	"github.com/egladman/magus/internal/doctor"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/ledger"
	"github.com/egladman/magus/internal/sessions"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
)

// The rehydration brief: what a session needs handed back when its host replaces
// the history with a summary.
//
// Every line is READ FROM DISK on the call. A summary is prose about what
// happened and degrades with each retelling; this is state, so it cannot. It
// restates no rule either: the trailer names the files the rules live in, because
// a rule copied here is a second copy to go stale.
//
// magus PRINTS it and the host's hook places it, the same split the guard
// templates keep. Nothing here knows what a session is, when one compacts, or
// which host asked.

// briefTextWidth is the longest line the text view emits. A goal and a validation
// command are as long as their author made them, and this lands
// in a context window through a hook, so the bound is on the rendered LINE rather
// than on the fields behind it.
const briefTextWidth = 160

// briefCommitWalk bounds the unpushed count. Each commit costs the backend one
// resolution, and past a couple of dozen the answer a reader acts on is "many", so
// the walk stops and says so rather than paying for precision nobody uses.
const briefCommitWalk = 20

// sessionBrief is the whole payload, and the -o json shape.
type sessionBrief struct {
	Workspace string `json:"workspace"`
	Branch    string `json:"branch,omitempty"`
	Revision  string `json:"revision,omitempty"`
	// Unpushed is absent rather than zero when the base ref does not resolve: a
	// checkout with no upstream and one with nothing to push are different facts.
	Unpushed *briefUnpushed `json:"unpushed,omitempty"`
	Tree     briefTree      `json:"tree"`
	Leases   []briefLease   `json:"leases,omitempty"`
	Failures []briefFailure `json:"failures,omitempty"`
	// GuardWiring is the host hook config paths in this checkout that invoke magus.
	// Empty means the rules exist here and nothing runs them.
	GuardWiring []string `json:"guard_wiring,omitempty"`
	// Rules are the instruction files and skill directories that exist here, named
	// so the model re-reads them instead of trusting a summary of them.
	Rules []string `json:"rules,omitempty"`
	// PromptCache is how long since a tool call last ran past the guard in this
	// checkout, against every published cache window. A session reading this brief is
	// deciding whether to resume, and a resume past a closed window re-pays the whole
	// prompt. Empty Providers when the trail here has seen nothing.
	PromptCache sessions.PromptCacheClock `json:"prompt_cache,omitzero"`
}

// briefUnpushed counts the commits this checkout carries that its base ref does not.
type briefUnpushed struct {
	Base  string `json:"base"`
	Count int    `json:"count"`
	// AtLeast marks a walk that hit briefCommitWalk before reaching the base, so
	// Count reads as a floor rather than as the number.
	AtLeast bool `json:"at_least,omitempty"`
}

// briefTree is the dirty tree, split by the classifier `magus describe file` uses.
type briefTree struct {
	// Reported is false when no VCS answered here. A tree nobody could read is not
	// a clean one, and the two would otherwise print identically.
	Reported  bool     `json:"reported"`
	Dirty     int      `json:"dirty"`
	Sources   []string `json:"sources,omitempty"`
	Outputs   []string `json:"outputs,omitempty"`
	Unclaimed []string `json:"unclaimed,omitempty"`
	// Classified is false when the magusfile would not load, which is when a dirty
	// path can be listed but not judged. Without it, three empty classes read as a
	// tree nothing claims rather than as a question nobody could ask.
	Classified bool `json:"classified"`
}

// briefLease is one live lease and the single command that binds it here.
type briefLease struct {
	ID         string `json:"id"`
	State      string `json:"state,omitempty"`
	Bind       string `json:"bind"`
	Goal       string `json:"goal,omitempty"`
	Validation string `json:"validation,omitempty"`
}

// briefFailure is one target the last recorded run failed, and the command that
// reads its captured output back.
type briefFailure struct {
	Target  string `json:"target"`
	Project string `json:"project,omitempty"`
	Ref     string `json:"ref,omitempty"`
	Inspect string `json:"inspect,omitempty"`
	At      string `json:"at,omitempty"`
}

// sessionBriefCmd implements `magus session --brief`.
func sessionBriefCmd(ctx context.Context, root string) error {
	root = resolveRootOrEmpty(root)
	if root == "" {
		return fmt.Errorf("magus session --brief: no workspace here: the brief is read out of a checkout, so run from inside one or pass --root <path>")
	}
	// The magusfile is optional here for the reason `session checkpoint` makes it
	// optional: the states where it will not load are exactly the ones where a
	// reader most needs to be told where the work stands.
	var ws types.WorkspaceRepository
	if loaded, err := inspectWorkspace(ctx, root); err == nil {
		ws, root = loaded, loaded.Root()
	}
	brief := gatherSessionBrief(ctx, root, ws)

	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}
	switch opts.Format {
	case outputText:
		fmt.Print(brief.Text())
		return nil
	case outputName:
		// The revision, matching `magus session checkpoint -o name`: the one value
		// that identifies the state this brief describes. A tree with no revision to
		// report prints nothing rather than a blank line, which a caller reading
		// names line by line would take for an entry.
		if brief.Revision == "" {
			return nil
		}
		return emitNames([]string{brief.Revision})
	}
	return emitFormatted(opts, brief)
}

// gatherSessionBrief reads the checkout. ws is the loaded workspace, or nil when the
// magusfile would not load, which costs the classification and nothing else.
//
// It returns no error: this runs from a hook on a tree that may be mid-bootstrap, and
// a brief missing a section is worth more than a refusal, so a source that cannot
// answer contributes nothing instead.
func gatherSessionBrief(ctx context.Context, root string, ws types.WorkspaceRepository) sessionBrief {
	brief := sessionBrief{Workspace: root}

	vcsOpts := types.VCSOptions{}
	if ws != nil {
		vcsOpts = ws.VCSOptions()
	}
	if res, err := vcs.Resolve(ctx, root, "", vcsOpts); err == nil && res.VCS != nil {
		brief.readVCS(ctx, res, ws)
	}
	brief.Leases = briefLeases(root)
	brief.Failures = lastRunFailures(root)
	brief.GuardWiring = relativeTo(root, doctor.HookConfigs(root))
	brief.Rules = ruleLocations(root)
	brief.PromptCache = promptCacheForCheckout(root, time.Now())
	return brief
}

// readVCS fills in the branch, the revision, the unpushed count and the dirty tree.
// ws may be nil, which drops the classification and keeps the counts.
func (b *sessionBrief) readVCS(ctx context.Context, res types.VCSResolution, ws types.WorkspaceRepository) {
	if meta, err := res.VCS.Metadata(ctx, b.Workspace); err == nil {
		b.Branch, b.Revision = meta.Ref, meta.Short
	}
	b.Unpushed = unpushedCommits(ctx, res, b.Workspace)

	dirty, err := res.VCS.DirtyFiles(ctx, b.Workspace, nil)
	if err != nil {
		return
	}
	b.Tree.Reported, b.Tree.Dirty = true, len(dirty)
	if ws == nil || len(dirty) == 0 {
		return
	}
	files, err := ws.ClassifyFiles(ctx, dirty)
	if err != nil {
		return
	}
	// The same three-way split staging reports, so a brief and `magus vcs add`
	// cannot disagree about what a path is.
	b.Tree.Sources, b.Tree.Outputs, b.Tree.Unclaimed = classifyForStaging(files)
	b.Tree.Classified = true
}

// unpushedCommits counts how many commits lead back to the base ref, or nil when the
// base does not resolve here (an unfetched remote, a repository with no upstream).
//
// Walked rather than asked: no backend capability reports a commit RANGE, and the
// four backends magus drives spell one differently enough that inventing that
// capability for this line would be the wrong trade. briefCommitWalk bounds it.
func unpushedCommits(ctx context.Context, res types.VCSResolution, root string) *briefUnpushed {
	if res.Base == "" {
		return nil
	}
	base, err := res.VCS.FindCommit(ctx, root, res.Base)
	if err != nil || base.ID == "" {
		return nil
	}
	history, err := res.VCS.History(ctx, root, briefCommitWalk)
	if err != nil {
		return nil
	}
	for i, c := range history {
		if c.ID == base.ID {
			return &briefUnpushed{Base: res.Base, Count: i}
		}
	}
	return &briefUnpushed{Base: res.Base, Count: len(history), AtLeast: true}
}

// briefLeases reads the rows a worker here may still be acting under, through the
// same filter the write guard applies, so the brief and the refusals agree about
// which leases are live.
func briefLeases(root string) []briefLease {
	store, err := openLedger(root)
	if err != nil {
		return nil
	}
	rows, err := store.List()
	if err != nil {
		return nil
	}
	live := liveLeases(rows)
	out := make([]briefLease, 0, len(live))
	for _, row := range live {
		// The bind line comes from the brief constructor rather than from a second
		// spelling here, so `magus ledger brief` and this cannot drift.
		out = append(out, briefLease{
			ID:         row.ID,
			State:      string(row.State),
			Bind:       ledger.NewBrief(row).Bind,
			Goal:       goalLine(row),
			Validation: row.Validation,
		})
	}
	return out
}

// lastRunFailures reports the failing targets of the most recent session that ran
// one, with the command that reads each one's captured output.
//
// The most recent SESSION rather than the most recent failure: a session that has
// since run green answered the question already, and reporting its predecessor's
// failures would send a reader after work that is done.
func lastRunFailures(root string) []briefFailure {
	dir, err := sessions.Dir(root)
	if err != nil {
		return nil
	}
	fold, err := sessions.ReadAll(dir)
	if err != nil {
		return nil
	}
	for _, s := range sessions.Summarize(fold) {
		if len(s.Targets) == 0 {
			continue
		}
		var out []briefFailure
		for _, t := range s.Targets {
			if t.Outcome != sessions.OutcomeFail {
				continue
			}
			f := briefFailure{Target: t.Target, Project: t.Project, Ref: t.Ref, At: briefTime(s.LastMs)}
			if t.Ref != "" {
				f.Inspect = hint.QueryOutput.With(t.Ref)
			}
			out = append(out, f)
		}
		return out
	}
	return nil
}

// ruleLocations names the instruction files and skill directories that exist in this
// checkout. What EXISTS, never a recommended list: a path magus prints that is not
// there teaches a reader to ignore the line.
func ruleLocations(root string) []string {
	var out []string
	if _, err := os.Stat(filepath.Join(root, agent.AgentsFile)); err == nil {
		out = append(out, agent.AgentsFile)
	}
	for _, dir := range agent.WellKnownSkillDirs() {
		if info, err := os.Stat(filepath.Join(root, dir)); err == nil && info.IsDir() {
			out = append(out, dir)
		}
	}
	return out
}

// relativeTo renders absolute paths inside root as workspace-relative ones, which is
// the spelling every other magus surface prints and takes back.
func relativeTo(root string, paths []string) []string {
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		if rel, err := filepath.Rel(root, p); err == nil && !strings.HasPrefix(rel, "..") {
			p = filepath.ToSlash(rel)
		}
		out = append(out, p)
	}
	return out
}

// Text renders the brief. Plain ASCII, no escape sequences and no color: a hook
// writes this into a model's context, where a control sequence is noise that costs
// tokens and can never be seen. Every line goes through briefLine, so the width
// bound holds for the output rather than for the fields it was assembled from.
func (b sessionBrief) Text() string {
	var s strings.Builder
	briefLine(&s, "magus brief: read from this checkout just now, not from the summary above")

	if b.Branch != "" {
		briefLine(&s, "branch: %s  revision: %s", b.Branch, orDash(b.Revision))
	} else {
		briefLine(&s, "revision: %s", orDash(b.Revision))
	}

	if u := b.Unpushed; u != nil {
		at := ""
		if u.AtLeast {
			at = "at least "
		}
		briefLine(&s, "unpushed: %s%d commit(s) not on %s", at, u.Count, u.Base)
	}
	b.writeTree(&s)
	b.writePromptCache(&s)
	b.writeLeases(&s)
	b.writeFailures(&s)

	if len(b.GuardWiring) > 0 {
		briefLine(&s, "guard wiring: %s", strings.Join(b.GuardWiring, ", "))
	} else {
		briefLine(&s, "guard wiring: none in this checkout, so nothing here runs the rules")
	}
	if len(b.Rules) > 0 {
		briefLine(&s, "rules live in %s; re-read them, nothing above restates one", strings.Join(b.Rules, ", "))
	}
	return s.String()
}

func (b sessionBrief) writeTree(s *strings.Builder) {
	if !b.Tree.Reported {
		briefLine(s, "tree: no VCS answered here, so nothing is reported about it")
		return
	}
	if b.Tree.Dirty == 0 {
		briefLine(s, "tree: clean")
		return
	}
	if !b.Tree.Classified {
		// Said out loud, because the counts a reader expects here are missing for a
		// reason they can act on.
		briefLine(s, "tree: %d changed file(s), unclassified (the magusfile did not load)", b.Tree.Dirty)
		return
	}
	// Counts only: the paths are one `git status` away, and a list here would spend
	// the window this lands in on what the model can read for itself.
	briefLine(s, "tree: %d changed file(s): %d source, %d output, %d unclaimed",
		b.Tree.Dirty, len(b.Tree.Sources), len(b.Tree.Outputs), len(b.Tree.Unclaimed))
}

// writePromptCache splits the windows into the two groups a resuming session acts on,
// rather than listing each one's closing instant the way the human listing does. The
// question here is binary and the answer is three lines of context at most: this lands in
// a model's window through a hook, and a five-row table of clock times would cost more
// than the decision it informs.
func (b sessionBrief) writePromptCache(s *strings.Builder) {
	if len(b.PromptCache.Providers) == 0 {
		return
	}
	var closed, open []string
	for _, p := range b.PromptCache.Providers {
		for _, w := range p.Windows {
			if w.Closed {
				closed = append(closed, p.Provider+" "+w.Window)
			} else {
				open = append(open, p.Provider+" "+w.Window)
			}
		}
	}
	briefLine(s, "prompt cache: last tool call here %s ago; a resume past a closed window re-pays the prompt", b.PromptCache.SinceText())
	if len(closed) > 0 {
		briefLine(s, "  closed: %s", strings.Join(closed, ", "))
	}
	if len(open) > 0 {
		briefLine(s, "  open: %s", strings.Join(open, ", "))
	}
}

func (b sessionBrief) writeLeases(s *strings.Builder) {
	if len(b.Leases) == 0 {
		return
	}
	briefLine(s, "leases live here:")
	for _, l := range b.Leases {
		briefLine(s, "  %s (%s): %s", l.ID, orDash(l.State), orDash(l.Goal))
		briefLine(s, "    bind: %s", l.Bind)
		if l.Validation != "" {
			briefLine(s, "    validation: %s", l.Validation)
		}
	}
}

func (b sessionBrief) writeFailures(s *strings.Builder) {
	if len(b.Failures) == 0 {
		return
	}
	briefLine(s, "the last recorded run failed:")
	for _, f := range b.Failures {
		briefLine(s, "  %s %s (%s)", f.Target, orDash(f.Project), orDash(f.At))
		if f.Inspect != "" {
			briefLine(s, "    %s", f.Inspect)
		}
	}
}

// briefLine writes one line, clipped to briefTextWidth and marked where it cut.
func briefLine(s *strings.Builder, format string, args ...any) {
	line := fmt.Sprintf(format, args...)
	if len(line) > briefTextWidth {
		line = line[:briefTextWidth-3] + "..."
	}
	s.WriteString(line)
	s.WriteByte('\n')
}

// briefTime renders a recorded millisecond stamp, or "" for one that was never set.
func briefTime(ms int64) string {
	if ms == 0 {
		return ""
	}
	return time.UnixMilli(ms).Format("2006-01-02 15:04:05")
}
