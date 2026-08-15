package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/egladman/magus/cmd/magus/gen"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/types"
)

// reviewCmd implements `magus review`: the working tree's changes, annotated and ordered by
// what they can break.
//
// It is the TERMINAL client of the same annotation join the console's Review surface reads
// and an agent joins over MCP. One computation, three transports - so a person reviewing in a
// terminal and an agent pairing with them are looking at the same ranking rather than two
// tools' opinions of one changeset.
func reviewCmd(ctx context.Context, root string, args []string) error {
	var rf *gen.ReviewFlags
	if _, err := cmdParse("review", args, func(fs *flag.FlagSet) {
		rf = gen.BindReview(fs)
		fs.Usage = func() { reviewUsage(os.Stderr) }
	}); err != nil {
		return err
	}

	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}

	m, err := loadMagus(ctx, root)
	if err != nil {
		return err
	}

	patch, err := m.WorkingDiff(ctx, nil)
	if err != nil {
		return err
	}
	if strings.TrimSpace(patch) == "" {
		// A clean tree is a STATE, and saying so beats printing an empty table that reads as
		// a failure to find anything.
		if opts.Format == outputText {
			fmt.Println("clean: every change is committed")
			return nil
		}
		return emitFormatted(opts, types.Review{Base: "working"})
	}

	rev, err := m.Review(ctx, changedPathsFromPatch(patch))
	if err != nil {
		return err
	}
	// The churn lenses, from a fresh scan. The daemon serves these from a warm cache; a
	// one-shot CLI has none, so it pays the bounded git-log walk here. Best-effort: a
	// workspace with no history simply reports no churn rather than failing the review.
	// Files: true is required - without it the lens ranks PROJECTS and the per-file list is
	// empty, so every file would silently report no churn.
	if hot, herr := m.Hotspots(ctx, types.InsightOptions{Commits: reviewHistoryCommits, Files: true}); herr == nil {
		var projects []types.TrendEntry
		if tr, terr := m.Trend(ctx, types.InsightOptions{Commits: reviewHistoryCommits}); terr == nil {
			projects = tr.Projects
		}
		rev.AttachChurn(hot.Files, projects)
	}
	// The agent trail: which sessions wrote each file and what they had read first. Empty when
	// no guard hook is wired, which is the common case rather than a fault.
	rev.AttachReplay(reviewTouches(m.Root(), m.CacheDir(), changedPathsFromPatch(patch)))

	switch opts.Format {
	case outputJSON, outputYAML, outputJSONL, outputTemplate:
		return emitFormatted(opts, rev)
	case outputName:
		for _, f := range rev.Files {
			if f.Generated() && !rf.Generated {
				continue
			}
			fmt.Println(f.Path)
		}
		return nil
	}
	return printReviewText(rev, rf.Generated)
}

func reviewUsage(w *os.File) {
	fmt.Fprintln(w, "Usage: magus review [--generated] [flags]")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Read the working tree's uncommitted changes, ordered by what they can break.")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Generated files - declared target outputs - are folded away: reading one is")
	fmt.Fprintln(w, "reading a machine's restatement of a change made elsewhere, so the source edit")
	fmt.Fprintln(w, "is the review. Each remaining file carries the evidence behind its rank: how")
	fmt.Fprintln(w, "widely its changed symbols are referenced, whether they are public API surface,")
	fmt.Fprintln(w, "and the coverage a prior run observed.")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Flags:")
	fmt.Fprintln(w, "  --generated   include the folded declared outputs")
}

// reviewHistoryCommits bounds the git-log walk the churn lenses do. 500 matches what the
// daemon's insight scan uses, so the CLI and the console rank the same files the same way -
// two different windows would report two different "hottest file" answers for one tree.
const reviewHistoryCommits = 500

// reviewReplayEvents bounds the trail walk. Each event costs a small blob read, and a reader
// asking "what was this agent looking at" is asking about recent work by construction.
const reviewReplayEvents = 2000

// reviewTouches adapts the trail's Touch to the review's - a rename across a boundary types
// must not cross, since types imports nothing internal and the trail is internal.
func reviewTouches(root, cacheDir string, paths []string) map[string][]types.ReviewTouch {
	raw := trail.Replay(root, cacheDir, paths, reviewReplayEvents)
	if len(raw) == 0 {
		return nil
	}
	out := make(map[string][]types.ReviewTouch, len(raw))
	for path, touches := range raw {
		conv := make([]types.ReviewTouch, 0, len(touches))
		for _, t := range touches {
			conv = append(conv, types.ReviewTouch{
				Host: t.Host, Session: t.Session, Transcript: t.Transcript, Read: t.Read, Ran: t.Ran,
			})
		}
		out[path] = conv
	}
	return out
}

// printReviewText renders the review in the house style: counts before lists, the evidence
// beside the claim, plain ASCII.
func printReviewText(rev types.Review, showGenerated bool) error {
	var primary, generated []types.ReviewFile
	for _, f := range rev.Files {
		if f.Generated() {
			generated = append(generated, f)
		} else {
			primary = append(primary, f)
		}
	}

	fmt.Printf("%d files to read", len(primary))
	if len(generated) > 0 {
		fmt.Printf(", %d generated folded", len(generated))
	}
	if n := len(rev.SeedProjects); n > 0 {
		fmt.Printf("; %d projects edited, %d rebuild", n, len(rev.AffectedProjects))
	}
	fmt.Println()
	fmt.Println()

	for _, f := range primary {
		printReviewFile(f)
	}

	if len(generated) > 0 {
		fmt.Println()
		if showGenerated {
			fmt.Printf("generated (%d) - a target rewrites these; the source edit is the review\n", len(generated))
			for _, f := range generated {
				printReviewFile(f)
			}
		} else {
			fmt.Printf("%d generated files folded. They are declared target outputs: reading one is\n", len(generated))
			fmt.Println("reading a machine's restatement of a change made elsewhere. Show them with --generated.")
		}
	}

	// Notes name what could NOT be measured. Surfaced rather than swallowed, so an empty
	// column reads as "nothing was measured" rather than as "nothing depends on this".
	for _, n := range rev.Notes {
		fmt.Printf("\nnote: %s\n", n)
	}
	return nil
}

func printReviewFile(f types.ReviewFile) {
	fmt.Printf("  %s\n", f.Path)

	var facts []string
	if f.Surface == types.ReviewSurfacePublic {
		var api []string
		var across []string
		seen := map[string]bool{}
		for _, s := range f.Symbols {
			if s.ModuleAPI && s.Label != "" {
				api = append(api, s.Label)
			}
			for _, p := range s.ExternalProjects {
				if !seen[p] {
					seen[p] = true
					across = append(across, p)
				}
			}
		}
		switch {
		case len(api) > 0:
			facts = append(facts, "PUBLIC SURFACE: exports "+strings.Join(capSlice(api, 6), ", "))
		case len(across) > 0:
			facts = append(facts, "PUBLIC SURFACE: used by "+strings.Join(across, ", "))
		default:
			facts = append(facts, "PUBLIC SURFACE")
		}
	}
	if f.Reach > 0 {
		noun := "files"
		if f.Reach == 1 {
			noun = "file"
		}
		facts = append(facts, fmt.Sprintf("%d %s reference its widest changed symbol", f.Reach, noun))
	}
	if c := f.Coverage; c != nil && c.Total > 0 {
		facts = append(facts, fmt.Sprintf("%d%% covered", int(c.Ratio*100+0.5)))
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
	for _, fact := range facts {
		fmt.Printf("      %s\n", fact)
	}
	// The story, last: it is the deepest context and the least urgent. A reader scanning for
	// risk should hit reach and coverage first and find the narrative when they stop to read.
	for _, t := range f.Touches {
		who := t.Host
		if who == "" {
			who = "an agent"
		}
		fmt.Printf("      written by %s", who)
		if len(t.Read) > 0 {
			fmt.Printf(", after reading %s", strings.Join(capSlice(t.Read, 4), ", "))
		}
		fmt.Println()
		if t.Transcript != "" {
			fmt.Printf("        transcript: %s\n", t.Transcript)
		}
	}
}

// capSlice bounds a list for display, reporting the remainder rather than truncating in
// silence - a list that stops without saying so reads as the whole answer.
func capSlice(xs []string, n int) []string {
	if len(xs) <= n {
		return xs
	}
	return append(xs[:n:n], fmt.Sprintf("and %d more", len(xs)-n))
}

// changedPathsFromPatch reads the `diff --git` headers out of a unified patch.
//
// The paths come from the PATCH rather than from a second VCS call on purpose: a re-derived
// set would race an edit made since the patch was read, and annotate a file the reader is not
// looking at. The console makes the same choice for the same reason.
func changedPathsFromPatch(patch string) []string {
	var out []string
	seen := map[string]bool{}
	for _, line := range strings.Split(patch, "\n") {
		if !strings.HasPrefix(line, "diff --git ") {
			continue
		}
		rest := strings.TrimPrefix(line, "diff --git ")
		cut := strings.LastIndex(rest, " b/")
		if cut < 0 {
			continue
		}
		p := strings.TrimPrefix(rest[cut+1:], "b/")
		if p != "" && !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	return out
}
