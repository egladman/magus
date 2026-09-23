package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	rootmagus "github.com/egladman/magus"
	"github.com/egladman/magus/internal/agent"
	"github.com/egladman/magus/internal/doctor"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/sessions"
	"github.com/egladman/magus/internal/trail"
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

// briefTextWidth is the longest line the text view emits. Criteria and a validation
// command are as long as their author made them, and this lands
// in a context window through a hook, so the bound is on the rendered LINE rather
// than on the fields behind it.
const briefTextWidth = 160

// briefCommitWalk bounds the unpushed count. Each commit costs the backend one
// resolution, and past a couple of dozen the answer a reader acts on is "many", so
// the walk stops and says so rather than paying for precision nobody uses.
const briefCommitWalk = 20

const (
	briefFeedbackEvents   = 100
	briefFeedbackItems    = 1
	briefFeedbackEvidence = 2
)

// briefRecentSessions bounds the earlier sessions listed. Three, because this is a
// pointer into a store rather than the store itself: a reader who wants the fourth has
// `magus session` for the whole list, and every line here is spent from the context
// window the brief exists to refill.
const briefRecentSessions = 3

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
	// Console is where a person opens the console for this checkout, empty when none is
	// served. It is here because this payload lands in a model's context, and a model
	// that knows the address can hand it to the person who asked where to look.
	Console string `json:"console,omitempty"`
	// PromptCache is how long since a tool call last ran past the guard in this
	// checkout, against every published cache window: a resume past a closed window
	// re-pays the whole prompt. Empty Providers when the trail here has seen nothing.
	PromptCache sessions.PromptCacheClock `json:"prompt_cache,omitzero"`
	// Feedback is the bounded, recurring guard evidence a compacted session can
	// review. It is derived from activity on every brief, not copied into a
	// checkpoint, so an old stop never keeps injecting an already-resolved issue.
	Feedback []trail.GuardFeedback `json:"feedback,omitempty"`
	// Recent is what OTHER sessions in this repository did lately, from transcripts a
	// `magus session load` folded in. Empty until a workspace declares an adapter, and
	// empty is the honest answer then: nothing was observed, rather than nothing happened.
	//
	// The other fields answer "what is this checkout", which one session can work out
	// alone. This one cannot be worked out alone: a session that starts fresh, or wakes
	// up compacted, has no way to know another session spent yesterday in the files it is
	// about to open. That is the whole reason transcripts are loaded at all, and a brief
	// that omitted it would leave the evidence sitting in a store nobody reads.
	Recent []briefSession `json:"recent,omitempty"`
}

// briefSession is one earlier session reduced to what makes it worth opening: who it was,
// when it stopped, and how much it did. Not what it did, which is `magus session show`'s
// to answer at a length this payload cannot afford.
type briefSession struct {
	Session string `json:"session"`
	Host    string `json:"host,omitempty"`
	LastMs  int64  `json:"last_ms"`
	Events  int    `json:"events"`
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
	Exec       string `json:"exec"`
	Criteria   string `json:"criteria,omitempty"`
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
	brief.GuardWiring = relativeTo(root, doctor.HookConfigs(ctx, root, workspaceHarnessNames(ws)...))
	brief.Rules = ruleLocations(ctx, root, workspaceHarnessNames(ws)...)
	brief.PromptCache = promptCacheForCheckout(root, time.Now())
	if base, err := rootmagus.ResolveCacheDir(root, rootmagus.WithLoadedConfig(globalCfg)); err == nil {
		if feedback, err := trail.RecentGuardFeedback(base, "", briefFeedbackEvents); err == nil {
			for _, item := range feedback {
				if item.NeedsReview() {
					item.Evidence = item.Evidence[:min(len(item.Evidence), briefFeedbackEvidence)]
					brief.Feedback = append(brief.Feedback, item)
					if len(brief.Feedback) == briefFeedbackItems {
						break
					}
				}
			}
		}
	}
	brief.Recent = recentLoadedSessions(resolveRootOrEmpty(root))
	brief.Console = consoleRootURL()
	return brief
}

// recentLoadedSessions lists the last few sessions a transcript load folded in, newest
// first.
//
// Only sessions with loaded EVENTS count. Every run magus performs also writes a row
// here, so listing rows outright would fill the brief with this checkout's own builds,
// which the reader already knows about and cannot open anyway.
//
// THE CALLER'S OWN SESSION IS NOT EXCLUDED, and that is deliberate rather than a
// limitation of what a hook can know about itself. This payload exists for a session
// whose history was replaced by a summary, and the transcript of its own earlier hours is
// the single most useful thing in the list to that reader.
//
// Best-effort and silent, like every other source feeding the brief: a workspace that
// loads no transcripts contributes nothing instead of an error.
func recentLoadedSessions(root string) []briefSession {
	dir, err := sessions.Dir(root)
	if err != nil {
		return nil
	}
	fold, err := sessions.ReadAll(dir)
	if err != nil {
		return nil
	}
	var out []briefSession
	for _, s := range sessions.Summarize(fold) {
		if s.Events == 0 {
			continue
		}
		out = append(out, briefSession{Session: s.Session, Host: s.Host, LastMs: s.LastMs, Events: s.Events})
		if len(out) == briefRecentSessions {
			break
		}
	}
	return out
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
	store, err := openJobs(root)
	if err != nil {
		return nil
	}
	rows, err := store.List()
	if err != nil {
		return nil
	}
	out := make([]briefLease, 0, len(rows))
	for _, row := range rows {
		if !row.State.Live() {
			continue
		}
		criteria, _, _ := strings.Cut(strings.TrimSpace(row.Criteria), "\n")
		if criteria == "" {
			criteria = "no criteria recorded"
		}
		out = append(out, briefLease{
			ID:         row.ID,
			State:      string(row.State),
			Exec:       hint.JobExec.With(row.ID),
			Criteria:   criteria,
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
// there teaches a reader to ignore the line. wired are magusfile-selected harness
// spell names (workspaceHarnessNames(ws)); a harness with no id in wired names no
// skill directory here.
func ruleLocations(ctx context.Context, root string, wired ...string) []string {
	var out []string
	if _, err := os.Stat(filepath.Join(root, agent.AgentsFile)); err == nil {
		out = append(out, agent.AgentsFile)
	}
	// Best-effort display: omit skill dirs when descriptors fail to load rather
	// than inventing Installed state from an empty list.
	if dirs, err := agent.HarnessSkillDirs(ctx, root, wired...); err == nil {
		for _, dir := range dirs {
			if info, err := os.Stat(filepath.Join(root, dir)); err == nil && info.IsDir() {
				out = append(out, dir)
			}
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
	if b.Console != "" {
		briefLine(&s, "console: %s (it asks for a token)", b.Console)
	}
	b.writeTree(&s)
	b.writePromptCache(&s)
	b.writeLeases(&s)
	b.writeFailures(&s)
	b.writeFeedback(&s)
	b.writeRecent(&s)

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

// writeRecent names the earlier sessions and the one command that opens one.
//
// It NAMES them rather than summarizing them. A summary of what another session did is
// prose about prose, two removes from the transcript and wrong in a way the reader cannot
// detect; the id plus `magus session show` puts them one command from the record itself.
func (b sessionBrief) writeRecent(s *strings.Builder) {
	if len(b.Recent) == 0 {
		return
	}
	briefLine(s, "earlier sessions loaded here (%s reads one):", hint.SessionShow)
	for _, r := range b.Recent {
		host := r.Host
		if host == "" {
			host = "unknown host"
		}
		briefLine(s, "  %s  %s, %s, last active %s",
			r.Session, host, countOf(r.Events, "event"), humanAge(time.UnixMilli(r.LastMs)))
	}
}

func (b sessionBrief) writeFeedback(s *strings.Builder) {
	if len(b.Feedback) == 0 {
		return
	}
	item := b.Feedback[0]
	followed := "no later replacement request observed"
	if item.FollowedSessions > 0 {
		followed = fmt.Sprintf("replacement requested in %d session(s); execution outcome is unobservable", item.FollowedSessions)
	}
	briefLine(s, "improvement review: %s denied %d times across %d session(s); %s", item.Rule, item.Denied, item.Sessions, followed)
	briefLine(s, "  inspect evidence and choose a human-reviewed action: %s (recurring-guard-denials)", hint.Doctor.String())
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
		briefLine(s, "  %s (%s): %s", l.ID, orDash(l.State), orDash(l.Criteria))
		briefLine(s, "    exec: %s", l.Exec)
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
