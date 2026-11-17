package review

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/egladman/magus/internal/changeset"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/interactive"
	"github.com/egladman/magus/libs/termkit"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/types/diff"
)

// PathLinker returns a function that makes a workspace-relative path clickable, or the
// identity when the terminal cannot use one.
//
// A path printed as bare text is a path the reader has to retype or hand to an editor
// themselves, and this report prints nothing BUT paths. delta has had OSC 8 links for years
// and they are the cheapest legibility win available here.
//
// The gate is tty.WantsHyperlinks, which already refuses a pipe, TERM=screen, and (the
// subtle one) an SSH session, where a file:// URL names a path on the remote machine that
// the local terminal would resolve against the wrong filesystem. Keeping that decision in one
// place is what preserves the property that piped output carries no escape sequences at all.
func PathLinker(root string) func(string) string {
	if !termkit.WantsHyperlinks(os.Stdout, termkit.SystemProbe) {
		return func(p string) string { return p }
	}
	return func(p string) string {
		abs := p
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(root, p)
		}
		return termkit.Hyperlink(p, (&url.URL{Scheme: "file", Path: abs}).String())
	}
}

// diagnosticLine renders a Diagnostic the way a coded error prints: "[CODE] message (url)".
func diagnosticLine(d types.Diagnostic) string {
	line := d.Message
	if d.Code != "" {
		line = fmt.Sprintf("[%s] %s", d.Code, line)
	}
	if d.URL != "" {
		line += fmt.Sprintf(" (%s)", d.URL)
	}
	return line
}

// CountsLine is the headline every rendering of a changeset opens with: how much there
// is to read before any of it is shown.
//
// The interactive reader leaves this same line behind when it quits, which is why it is a
// string rather than a print: a session that erased its viewport and printed nothing would
// leave the scrollback with no record that a review happened at all.
// The fold state is threaded rather than assumed: under --generated every generated file is
// printed right below this line, so calling them folded would contradict the page it introduces.
func CountsLine(rev diff.Diff, showGenerated bool) string {
	gen := rev.GeneratedCount()
	line := fmt.Sprintf("%d files to read", len(rev.Files)-gen)
	if gen > 0 {
		state := "folded"
		if showGenerated {
			state = "shown"
		}
		line += fmt.Sprintf(", %d generated %s", gen, state)
	}
	if n := len(rev.SeedProjects); n > 0 {
		// Both halves name the noun: a bare "N rebuild" leaves the reader guessing what is
		// being counted.
		line += fmt.Sprintf("; %d projects edited, %d projects rebuild", n, len(rev.AffectedProjects))
	}
	return line
}

// TextOptions is what the printed report is told beyond the changeset.
type TextOptions struct {
	ShowGenerated bool
	// Link makes a path clickable, or is the identity.
	Link func(string) string
	// Impact is the --impact report, already rendered. It is nil unless --impact was passed, and
	// nil prints nothing at all: the report above it must not change shape because a second one
	// was appended.
	Impact []string
	// Console names where the same changeset opens: a link and the line that says how to sign in
	// to it. It is asked only at a terminal, and a blank link prints nothing.
	Console func() (link, signIn string)
}

// PrintText renders the diff in the house style: counts before lists, the evidence
// beside the claim, plain ASCII.
func PrintText(w io.Writer, rev diff.Diff, o TextOptions) error {
	var primary, generated []diff.File
	for _, f := range rev.Files {
		if f.Generated() {
			generated = append(generated, f)
		} else {
			primary = append(primary, f)
		}
	}

	fmt.Fprintln(w, CountsLine(rev, o.ShowGenerated))
	fmt.Fprintln(w)
	if lines := apiLines(rev.API); len(lines) > 0 {
		for _, line := range lines {
			fmt.Fprintln(w, line)
		}
		fmt.Fprintln(w)
	}

	// The ordering caveat prints BEFORE the list, and only this placement works. As a trailing
	// note it arrived after the reader had already read the first entry as the most dangerous
	// one, and it named the missing overlays rather than the missing order, so three separate
	// readers concluded the ranking had considered churn and rejected it. Say the one thing
	// that changes how the next twelve lines should be read, first.
	if !rev.Ranked() && len(primary) > 1 {
		fmt.Fprintln(w, "UNRANKED: no symbol index, so there is no consequence to rank by.")
		fmt.Fprintln(w, "What follows is path order, not a ranking. Build the index with")
		fmt.Fprintf(w, "`%s` to order these by what they can break.\n", hint.GraphBuild.String())
		fmt.Fprintln(w)
	}

	for _, f := range primary {
		printFile(w, f, o.Link)
	}

	if len(generated) > 0 {
		fmt.Fprintln(w)
		// Both branches name `magus describe file`, because "why is this folded" is the one
		// question the fold provokes and the answer was a hop out of reach: the folded list
		// said THAT a target rewrites these and never WHICH, so a reader who suspected a
		// mis-declared glob had nowhere to go.
		if o.ShowGenerated {
			fmt.Fprintf(w, "generated (%d) - a target rewrites these; the source edit is what to read\n", len(generated))
			for _, f := range generated {
				printFile(w, f, o.Link)
			}
			fmt.Fprintf(w, "      why is one of these folded? `%s` names the project that declares it\n", hint.DescribeFile.With("<path>"))
		} else {
			fmt.Fprintf(w, "%d generated files folded. They are declared target outputs: reading one is\n", len(generated))
			fmt.Fprintln(w, "reading a machine's restatement of a change made elsewhere. Show them with --generated,")
			fmt.Fprintf(w, "or ask why one is folded with `%s`.\n", hint.DescribeFile.With("<path>"))
		}
	}

	printOrder(w, rev, o.ShowGenerated)

	if e := rev.ConformanceError; e != nil {
		fmt.Fprintf(w, "\nerror: conformance could not check this change: %s\n", diagnosticLine(*e))
	}
	for _, u := range rev.Uncovered {
		fmt.Fprintf(w, "\nconformance did not check %s: %s\n", u.Project, u.Reason.Sentence())
	}
	// Notes name what could NOT be measured. Surfaced rather than swallowed, so an empty
	// column reads as "nothing was measured" rather than as "nothing depends on this".
	for _, n := range rev.Notes {
		fmt.Fprintf(w, "\nnote: %s\n", n)
	}

	// Above the console link rather than below it: the link is a trailing pointer somewhere
	// else, and burying it under forty lines of report would cost it every reader it has.
	if o.Impact != nil {
		fmt.Fprintln(w)
		for _, line := range o.Impact {
			fmt.Fprintln(w, line)
		}
	}

	// What to do next, and where to see the same changeset. Both are for a person: the console
	// link carries no token (see printJobWatchHint), so the terminal check is an invitation
	// rather than a secrecy measure, and it keeps `magus diff > file` as data.
	if termkit.IsTerminalWriter(w, termkit.SystemProbe) {
		for _, line := range nextStepLines(len(primary)) {
			fmt.Fprintln(w, line)
		}
		if o.Console != nil {
			if u, signIn := o.Console(); u != "" {
				fmt.Fprintf(w, "open in console: %s\n%s\n", u, signIn)
			}
		}
	}
	return nil
}

// nextStepLines names what a reader can do with the changeset they were just shown.
//
// This output is where most people meet magus's review workflow, so `--tui` and `--impact` are
// named here rather than only in `-h` prose and the man page.
//
// Nothing when nothing was listed: a clean tree or an all-generated changeset has no reading
// to offer, and a suggestion there is a suggestion to do nothing.
func nextStepLines(readable int) []string {
	if readable == 0 {
		return nil
	}
	// At a terminal the viewer has already opened, so what is left to teach is the two ways out
	// of it: this report, and the impact question the viewer has nowhere to put.
	return []string{
		"",
		"the blast radius:  " + hint.Diff.With("--impact"),
		"just the report:   " + hint.Diff.With("--no-tui"),
	}
}

func printFile(w io.Writer, f diff.File, link func(string) string) {
	fmt.Fprintf(w, "  %s\n", link(f.Path))
	for _, fact := range FileFacts(f) {
		fmt.Fprintf(w, "      %s\n", fact)
	}
	for _, t := range f.Threads {
		at := "not on a hunk of this changeset"
		for _, h := range f.Hunks {
			if h.Index == t.Hunk {
				at = "on " + changeset.NewRange(f.Path, h.NewStart, h.NewCount)
			}
		}
		fmt.Fprintf(w, "      %s, %s\n", ThreadRefLine(t), at)
	}
	// Agent touches come last: they are the deepest context and the least urgent, and a reader
	// scanning for risk should meet reach and coverage first.
	for _, t := range f.Touches {
		who := t.Host
		if who == "" {
			who = "an agent"
		}
		fmt.Fprintf(w, "      written by %s", who)
		if len(t.Read) > 0 {
			fmt.Fprintf(w, ", after reading %s", strings.Join(capSlice(t.Read, 4), ", "))
		}
		fmt.Fprintln(w)
		if t.Transcript != "" {
			fmt.Fprintf(w, "        transcript: %s\n", t.Transcript)
		}
	}
}

// apiLines states the semver bump a baseline comparison proved, or nothing without one.
//
// The floor leads because it is the only claim magus can prove; the likely bump follows with
// the count that raised it, so a reader who knows the signature change is compatible can see
// exactly what they are overruling.
func apiLines(api *diff.API) []string {
	if api == nil {
		return nil
	}
	head := "API: at least " + api.Floor
	if api.Likely != api.Floor {
		head += fmt.Sprintf(", likely %s (%d public signature(s) changed)", api.Likely, api.Signature)
	}
	lines := []string{
		fmt.Sprintf("%s against %s", head, api.Base),
		fmt.Sprintf("  public symbols: %d added, %d removed, %d re-signed, %d changed in body only",
			api.Added, api.Removed, api.Signature, api.Body),
	}
	if api.Stage.Known() {
		stage := "  newest release " + api.Stage.Tag
		if api.Stage.Major == 0 {
			stage += ": below 1.0 a major floor ships as a minor release"
		}
		lines = append(lines, stage)
	}
	return append(lines, "  a floor, not a verdict: behavior can change under an unchanged signature, so raise it freely")
}

// FileFacts is what magus knows about one changed file, one claim per line.
//
// One definition for the printer and the interactive reader, so both show the SAME sentences:
// two renderings of "12 files reference its widest changed symbol" would drift, and the drift
// would be invisible until somebody compared two views side by side.
func FileFacts(f diff.File) []string {
	var facts []string
	if f.Visibility == diff.VisibilityPublic {
		var api []string
		var across []string
		seen := map[string]bool{}
		for _, s := range f.Symbols {
			if s.PublicBeyondWorkspace && s.Label != "" {
				api = append(api, s.Label)
			}
			for _, p := range s.PublicTo {
				if !seen[p] {
					seen[p] = true
					across = append(across, p)
				}
			}
		}
		switch {
		case len(api) > 0:
			facts = append(facts, "EXPORTED API: exports "+strings.Join(capSlice(api, 6), ", "))
		case len(across) > 0:
			facts = append(facts, "EXPORTED API: used by "+strings.Join(across, ", "))
		default:
			facts = append(facts, "EXPORTED API")
		}
		// Only the changes a consumer can see. A private helper listed here would sit beside
		// the bump it did not move, and read as the reason for it.
		for _, s := range f.Symbols {
			if !s.PublicBeyondWorkspace && len(s.PublicTo) == 0 {
				continue
			}
			switch s.Change {
			case diff.ChangeRemoved:
				facts = append(facts, "REMOVED "+s.Qualified)
			case diff.ChangeAdded:
				facts = append(facts, "ADDED "+s.Qualified)
			case diff.ChangeSignature:
				facts = append(facts, fmt.Sprintf("SIGNATURE %s: `%s` -> `%s`", s.Qualified, s.BaseSignature, s.Signature))
			}
		}
	}
	for _, s := range f.Symbols {
		for _, c := range s.Checks {
			facts = append(facts, "CONFORMANCE "+c.Message)
		}
	}
	for _, c := range f.Layout {
		facts = append(facts, "LAYOUT "+c.Message)
	}
	if n := f.ReachOr(0); f.Reach != nil && n > 0 {
		noun := "files"
		if n == 1 {
			noun = "file"
		}
		facts = append(facts, fmt.Sprintf("%d %s reference its widest changed symbol", n, noun))
	}
	if c := f.Coverage; c != nil && c.Total > 0 {
		facts = append(facts, fmt.Sprintf("%d%% covered", int(c.Ratio*100+0.5)))
	}
	// Said plainly, because an empty annotation row otherwise reads as "quiet file" when it
	// means "magus has never seen this file before".
	if f.NoHistory {
		facts = append(facts, "NO HISTORY - nothing has exercised this yet")
	}
	if ch := f.Churn; ch != nil && ch.Commits > 0 {
		noun := "commits"
		if ch.Commits == 1 {
			noun = "commit"
		}
		churn := fmt.Sprintf("changed in %d %s", ch.Commits, noun)
		if ch.Authors > 1 {
			churn += fmt.Sprintf(" by %d people", ch.Authors)
		}
		if ch.NotableRank() {
			churn += fmt.Sprintf(", hotspot #%d", ch.Rank)
		}
		if ch.Rising() {
			churn += " AND RISING - worth asking why it keeps changing"
		}
		facts = append(facts, churn)
	}
	if f.Project != "" {
		facts = append(facts, "in "+f.Project)
	}
	return facts
}

// capSlice bounds a list for display, reporting the remainder rather than truncating in
// silence: a list that stops without saying so reads as the whole answer.
func capSlice(xs []string, n int) []string {
	if len(xs) <= n {
		return xs
	}
	return append(xs[:n:n], fmt.Sprintf("and %d more", len(xs)-n))
}

// HintSinceLastReview tells a reader who has been here before what they can skip.
//
// The exhausting part of a second pass is not the reading, it is that nothing separates what moved
// from what you already weighed, so you re-read the whole change, find the same things, and learn
// to rubber-stamp the next one. Every review tool that solved this did it the same way, by naming
// the revision you last got through and diffing from there; the one everybody uses did not, which
// is why the habit of re-reading everything feels normal.
//
// It NAMES the command rather than narrowing the changeset itself. Silently showing a subset of
// what was asked for would be the one failure this report cannot afford: a reader who was shown
// less than they asked for, and not told, concludes they have seen a file they have not.
//
// Silent unless there is a genuine earlier pass to subtract: no receipts, receipts from a
// working-tree review that names no revision, or an earlier pass at the revision already in front
// of them all print nothing.
func HintSinceLastReview(ctx context.Context, rev diff.Diff, src Source) {
	if !interactive.HintsEnabled() || src.Kind != SourceRevRange {
		return
	}
	at, covered := rev.Reviewed.At, rev.Reviewed.Files
	if covered == 0 || at.Revision == src.Base {
		return
	}
	interactive.Hint(ctx, fmt.Sprintf(
		"you last reviewed %d of these %d files at %s: `%s` shows only what changed since",
		covered, len(rev.Files), short(at.Revision), hint.Diff.With("--rev", fmt.Sprintf("%s...%s", at.Revision, src.Head))))
}

// short abbreviates a revision for a message a person reads, keeping the full one for the command.
func short(rev string) string {
	if len(rev) <= 12 {
		return rev
	}
	return rev[:12]
}
