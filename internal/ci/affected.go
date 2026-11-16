package ci

import (
	"cmp"
	"context"
	"fmt"
	"io"
	"maps"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/journal"
	"github.com/egladman/magus/libs/text/plural"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
)

// UndeclaredSeedNotice renders MGS1028's message, without the code, for the seed
// projects in undeclaredBySeed. With withFiles each project carries its undeclared files,
// for a reader that cannot see them anywhere else; both lists are capped by CappedList.
func UndeclaredSeedNotice(undeclaredBySeed map[string][]string, withFiles bool) string {
	seeds := slices.Sorted(maps.Keys(undeclaredBySeed))
	named := make([]string, 0, len(seeds))
	for _, seed := range seeds {
		if withFiles {
			seed += fmt.Sprintf(" (%s)", strings.Join(CappedList(undeclaredBySeed[seed]), ", "))
		}
		named = append(named, seed)
	}
	return fmt.Sprintf(
		"projects seeded by changed files nothing declares: %s. Directory containment "+
			"selected them, so the targets they rerun were already correct. Declare the files "+
			"in the owning project's sources, or leave them undeclared deliberately (see %s)",
		strings.Join(CappedList(named), ", "),
		types.CodeURL(types.UndeclaredSeedingFile))
}

// UndeclaredOnlySeeds narrows an affected result to the seeds selected ONLY by files no
// project declares: every changed file under them is undeclared, so nothing that moved a
// cache key put them in the set. A seed with one declared file among them had a keyed
// reason to run and is not what this reports.
func UndeclaredOnlySeeds(res *types.AffectedResult) map[string][]string {
	if res == nil {
		return nil
	}
	out := map[string][]string{}
	for seed, undeclared := range res.UndeclaredBySeed {
		if len(undeclared) > 0 && len(undeclared) == len(res.FilesBySeed[seed]) {
			out[seed] = undeclared
		}
	}
	return out
}

// TrackedUndeclaredSeeds narrows undeclaredOnly to files the VCS TRACKS, dropping any
// seed left with none.
//
// The same rule, for the same reason, as the standing twin of this diagnostic in
// internal/doctor/checks.go (checkUndeclaredSeedingFiles): an untracked file that seeds
// a rerun is a missing ignore rule, not a missing declaration, and a fresh clone must
// not read differently from a working one. Without it the run named its OWN leavings
// back at the reader. A workspace whose .gitignore does not cover the cache directory
// got 45 lock files under .magus/locks/, written by the very run that then advised
// declaring them, alongside the binary that run had just linked: advice nobody can act
// on, in the one hint a -s gate still prints.
//
// A backend that cannot answer reports NOTHING rather than guessing, the way the doctor
// twin skips the question. Stricter than the drift gate's "leave the set alone" in
// run.go, and deliberately: this is advice, so silence costs one unsaid hint while a
// guess costs the reader's trust in every hint after it.
func TrackedUndeclaredSeeds(ctx context.Context, root string, opts types.VCSOptions, undeclaredOnly map[string][]string) map[string][]string {
	if len(undeclaredOnly) == 0 {
		return undeclaredOnly
	}
	res, err := vcs.Resolve(ctx, root, "", opts)
	if err != nil || res.VCS == nil {
		return nil
	}
	// One pathspec for every seed's files: TrackedFiles is a subprocess per call, and a
	// changeset spanning a dozen projects would otherwise fork a dozen times to answer
	// one hint.
	var ask []string
	for _, files := range undeclaredOnly {
		ask = append(ask, files...)
	}
	slices.Sort(ask)
	known, err := res.VCS.TrackedFiles(ctx, root, slices.Compact(ask))
	if err != nil {
		return nil
	}
	tracked := make(map[string]bool, len(known))
	for _, p := range known {
		tracked[filepath.ToSlash(p)] = true
	}
	out := map[string][]string{}
	for seed, files := range undeclaredOnly {
		var kept []string
		for _, f := range files {
			if tracked[f] {
				kept = append(kept, f)
			}
		}
		if len(kept) > 0 {
			out[seed] = kept
		}
	}
	return out
}

// CappedList bounds an inline list at undeclaredSeedHintCap, replacing the tail with a
// count of what it left out.
func CappedList(items []string) []string {
	if len(items) <= undeclaredSeedHintCap {
		return items
	}
	return append(items[:undeclaredSeedHintCap:undeclaredSeedHintCap],
		fmt.Sprintf("and %d more", len(items)-undeclaredSeedHintCap))
}

// CappedUnits is units as project:target, bounded by CappedList.
func CappedUnits(units []types.Unit) string {
	return strings.Join(CappedList(types.UnitNames(units)), ", ")
}

// undeclaredSeedHintCap bounds how many seed projects (and how many of one project's
// files) MGS1028 names inline.
const undeclaredSeedHintCap = 5

// impactFileCap bounds how many changed files are listed per seed project in text
// mode; a large changeset stays readable while the full set is still one -o json away.
const impactFileCap = 12

// PrintImpactText renders the impact report in the `magus graph explain` house style:
// counts before lists, verbs not arrows, full ids, plain ASCII. Changed files are
// grouped under the seed project that owns them (not dumped as one flat list) so a
// large changeset stays legible.
func PrintImpactText(w io.Writer, out *types.ImpactResult, explorerLink func(seed string) (link, authHint string)) error {
	// Notes carry graceful degradation (a knowledge graph that failed to load, a
	// missing symbol index), so they must survive the early returns below. Without
	// this, an empty changeset rendered as a clean "nothing is affected" while the
	// reason the overlays were absent went unsaid, which is the worst shape a silent
	// failure can take in a gate command.
	defer func() {
		for _, n := range out.Notes {
			fprintf(w, "\nnote: %s\n", n)
		}
	}()

	if out.ChangedFileCount == 0 {
		fprintf(w, "No changed files against %s; nothing is affected.\n", out.Base)
		return nil
	}

	fprintf(w, "Changeset impact (base: %s)\n", out.Base)
	fprintf(w, "%s changed, seeding %s, affecting %s.\n",
		plural.Count(out.ChangedFileCount, "file", "files"),
		plural.Count(len(out.SeedProjects), "project", "projects"),
		plural.Count(len(out.AffectedProjects), "project", "projects"))

	if len(out.AffectedProjects) == 0 {
		fprintf(w, "\nNo projects are affected (every changed file sits outside a project).\n")
		return nil
	}

	// Seeds first (they carry the changed files), then the projects reached only
	// through the dependency closure. Counting seeds here also yields the number of
	// changed files that landed inside a project, for an outside-any-project note.
	seeded := 0
	fprintf(w, "\nAffected projects (%d):\n", len(out.AffectedProjects))
	for _, p := range out.AffectedProjects {
		if !p.Seed {
			continue
		}
		seeded += len(p.Files)
		// "seeded by 12 changed files" reads as twelve files this project is built
		// from. Some of them may be seeding it by directory containment alone, which
		// is a rerun whose result was already correct, so the count says how many,
		// and the listing below marks which.
		label := plural.Count(len(p.Files), "changed file", "changed files")
		if n := len(p.UndeclaredFiles); n > 0 {
			label += fmt.Sprintf(", %d declared by no project", n)
		}
		fprintf(w, "  %s (seeded by %s)\n", p.Path, label)
		if len(p.Targets) > 0 {
			fprintf(w, "    targets: %s\n", strings.Join(p.Targets, ", "))
		}
		shown := p.Files
		if len(shown) > impactFileCap {
			shown = shown[:impactFileCap]
		}
		for _, f := range shown {
			if slices.Contains(p.UndeclaredFiles, f) {
				fprintf(w, "    %s (undeclared)\n", f)
				continue
			}
			fprintf(w, "    %s\n", f)
		}
		if extra := len(p.Files) - len(shown); extra > 0 {
			fprintf(w, "    ... and %d more\n", extra)
		}
	}
	for _, p := range out.AffectedProjects {
		if p.Seed {
			continue
		}
		fprintf(w, "  %s (via dependencies)\n", p.Path)
		if len(p.Targets) > 0 {
			fprintf(w, "    targets: %s\n", strings.Join(p.Targets, ", "))
		}
	}

	if outside := out.ChangedFileCount - seeded; outside > 0 {
		fprintf(w, "\n%s changed outside any project (seeded nothing).\n", plural.Count(outside, "file", "files"))
	}

	printImpactOverlays(w, out)

	// Complementary deep-link into the live Graph Explorer, focused on a single
	// representative seed with a blast view (what depends on it: the closure the
	// change ripples out to). The query grammar ANDs its terms with no OR, so the
	// full affected set cannot be selected in one query. Always printed; the server
	// may not be up when the browser opens it, hence the hint.
	if len(out.SeedProjects) > 0 {
		seed := out.SeedProjects[0]
		label := seed
		if len(out.SeedProjects) > 1 {
			label = fmt.Sprintf("%s (1 of %d seeds)", seed, len(out.SeedProjects))
		}
		link, authHint := explorerLink(seed)
		fprintf(w, "\nView the blast radius of %s in the Graph Explorer: %s\n", label, link)
		fprintf(w, "%s\n", authHint)
		fprintf(w, "(start the magus server if the graph does not load)\n")
	}

	fprintf(w, "\nRun the full pipeline over this set with: %s\n", hint.Affected.With("ci"))
	return nil
}

// impactSymbolCap bounds how many changed symbols the caller overlay lists in text
// mode; the widest-reach symbols lead (the list is sorted by descending caller count),
// so a large changeset stays readable while the full set is one -o json away.
const impactSymbolCap = 20

// printImpactOverlays renders the differentiated overlay sections (changed-symbol
// callers and coverage on changed code) beneath the blast radius. Each is additive and
// self-suppressing: an overlay with no data prints nothing here (its honest output is
// the Note the enrichment appended). Same house style as the blast radius: counts before
// lists, verbs not arrows, plain ASCII.
func printImpactOverlays(w io.Writer, out *types.ImpactResult) {
	if len(out.ChangedSymbols) > 0 {
		files := map[string]struct{}{}
		for _, s := range out.ChangedSymbols {
			files[s.File] = struct{}{}
		}
		fprintf(w, "\nChanged-symbol callers (%s across %s):\n",
			plural.Count(len(out.ChangedSymbols), "symbol", "symbols"),
			plural.Count(len(files), "changed file", "changed files"))
		shown := out.ChangedSymbols
		if len(shown) > impactSymbolCap {
			shown = shown[:impactSymbolCap]
		}
		for _, s := range shown {
			name := s.Label
			if name == "" {
				name = s.Symbol
			}
			line := fmt.Sprintf("  %s (%s): %s across %s", name, s.File,
				plural.Count(s.RefCount, "caller", "callers"),
				plural.Count(s.FileCount, "file", "files"))
			if s.Coverage.Total > 0 {
				line += fmt.Sprintf(" [coverage %s]", impactPct(s.Coverage.Ratio))
			}
			fprintln(w, line)
		}
		if extra := len(out.ChangedSymbols) - len(shown); extra > 0 {
			fprintf(w, "  ... and %d more\n", extra)
		}
	}

	if len(out.ChangedFileCoverage) > 0 {
		fprintf(w, "\nCoverage on changed files (%s):\n",
			plural.Count(len(out.ChangedFileCoverage), "file", "files"))
		for _, c := range out.ChangedFileCoverage {
			fprintf(w, "  %s: %s (%d/%d stmts)\n", c.File,
				impactPct(c.Coverage.Ratio), c.Coverage.Covered, c.Coverage.Total)
		}
	}
}

// impactPct renders a 0..1 coverage ratio as a whole-percent string ("80%").
func impactPct(ratio float64) string {
	return fmt.Sprintf("%.0f%%", ratio*100)
}

// AffectedExplainOutput is the structured result for --explain.
type AffectedExplainOutput struct {
	Project  string                `json:"project"         yaml:"project"`
	Affected bool                  `json:"affected"        yaml:"affected"`
	Base     string                `json:"base"            yaml:"base"`
	Paths    []AffectedExplainPath `json:"paths,omitempty" yaml:"paths,omitempty"`
}

type AffectedExplainPath struct {
	Seed  string   `json:"seed"  yaml:"seed"`
	Chain []string `json:"chain" yaml:"chain"`
	Files []string `json:"files" yaml:"files"`
	// Undeclared is the subset of Files that no project declares: the ones whose
	// answer to "why did this run" is directory containment rather than a cache key.
	Undeclared []string `json:"undeclared,omitempty" yaml:"undeclared,omitempty"`
}

// PrintAffectedExplainText renders the text and wide forms of `magus affected --explain`,
// reporting whether the project is affected at all; the caller appends the VCS diff
// hints only when it is. Split out for the same reason PrintImpactText is: the rendering
// is a pure function of the result, and the I/O around it is not.
func PrintAffectedExplainText(w io.Writer, out AffectedExplainOutput) bool {
	if !out.Affected {
		fprintf(w, "%s is not affected (base: %s)\n", out.Project, out.Base)
		return false
	}
	fprintf(w, "%s\n", out.Project)
	for _, ap := range out.Paths {
		if len(ap.Chain) == 1 {
			fprintf(w, "  changed files:\n")
		} else {
			fprintf(w, "  via %s:\n", strings.Join(ap.Chain, " -> "))
		}
		for _, f := range ap.Files {
			// The marker answers the question the file list raises but cannot settle:
			// whether this path is an input the seed is built from, or one that only
			// happens to sit inside it.
			if slices.Contains(ap.Undeclared, f) {
				fprintf(w, "    %s (undeclared: seeds by directory containment, keys nothing)\n", f)
				continue
			}
			fprintf(w, "    %s\n", f)
		}
	}
	return true
}

// PrintFailedResults closes a failed run with one line per failed target: its output ref,
// its project and target, and the first line of its cause. The excerpt of each log was
// printed when the target failed; the ref is what reaches the rest of it. A refusal has no
// ref, since nothing ran, and names its MGS code instead.
func PrintFailedResults(w io.Writer, events []journal.Event) {
	if len(events) == 0 {
		return
	}
	fprintf(w, "\nfailed: %s; `magus query output <ref>` prints a whole log\n", plural.Count(len(events), "target", "targets"))
	for _, e := range events {
		ref := e.Ref
		if ref == "" {
			ref = cmp.Or(e.DiagnosticCode, "no-ref")
		}
		cause, _, _ := strings.Cut(strings.TrimSpace(e.Text), "\n")
		fprintf(w, "  %s %s:%s", ref, e.Project, e.Target)
		if cause != "" {
			fprintf(w, "  %s", cause)
		}
		fprintln(w)
	}
}

// PrintRunRef names the whole run, which no per-target ref reaches.
func PrintRunRef(w io.Writer, inv string) {
	if inv == "" {
		return
	}
	fprintf(w, "\nrun: %s; `%s` prints the whole run\n", inv, hint.QueryInvocation.With(inv))
}

func fprintf(w io.Writer, format string, a ...any) { _, _ = fmt.Fprintf(w, format, a...) } // a report that cannot be written has nowhere better to say so

func fprintln(w io.Writer, a ...any) { _, _ = fmt.Fprintln(w, a...) } // a report that cannot be written has nowhere better to say so
