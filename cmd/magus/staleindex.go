package main

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	magus "github.com/egladman/magus"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/symbols"
	"github.com/egladman/magus/project"
)

// The staleness line refs, query and explain print under an answer drawn from a symbol
// index older than the sources it describes.
//
// It exists because the alternative was prose. "If refs reports a project not-indexed, run
// `magus graph build` and ask again" is written in a skill and in a guard advisory, and a
// rule that lives only in text is a rule with roughly even odds. This says it where the
// answer is, on every host, with nothing to wire and no hook to install.
//
// It never withholds and never fails a lookup. The rows print first and this is one line
// under them, because a stale index still holds true facts - it holds FEWER of them, and
// the failure it prevents is reading a short list as a complete one. printVerdict covers
// the neighbouring case, an answer that found NOTHING; this one speaks under an answer
// that found something and may be missing the rest.
//
// Freshness by MTIME, not through the cache. SymbolGaps records the reason it stops short
// of freshness: deciding whether an index would replay needs a cache handle, and these
// verbs INSPECT the workspace rather than opening it, because opening writes. A file newer
// than the index that covers it is the same question answered with two stats, and it is
// wrong only in the direction that stays quiet.

// staleIndexScanLimit bounds the per-project walk. A tree past it is one where a banner is
// not worth the stat calls, so the probe gives up and says nothing rather than slowing
// every lookup down to answer a question about tidiness.
const staleIndexScanLimit = 20000

// printIndexStaleness writes the staleness line under an answer, or nothing when every
// built index is current.
//
// Text only, and the callers are all inside their text arm already: a structured caller
// reads coverage off the answer record, and a line appended to json would corrupt it.
func printIndexStaleness(ctx context.Context, w io.Writer, root string) {
	if notice := staleIndexNotice(staleIndexProjects(ctx, root)); notice != "" {
		fmt.Fprint(w, notice)
	}
}

// staleIndexNotice renders the line, or "" for an empty list. Split from the probe so the
// two halves are testable apart: what is stale is a filesystem question, and what to say
// about it is not.
func staleIndexNotice(stale []string) string {
	if len(stale) == 0 {
		return ""
	}
	return fmt.Sprintf("\nstale index: %s changed since %s last indexed %s, so this answer may be missing sites in %s.\n",
		plural(len(stale), "a project", "projects"), hint.GraphBuild,
		plural(len(stale), "it", "them"), strings.Join(stale, ", ")) +
		fmt.Sprintf("  refresh and ask again: %s\n", hint.GraphBuild)
}

// plural picks a word by count. Two call sites in one sentence, so the alternative is an
// interpolation that reads worse than the branch.
func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// staleIndexProjects lists, workspace-relative and sorted, the projects whose built symbol
// index predates a source file it covers.
//
// Silent on every uncertainty, which is the same contract every other advisory on this
// surface keeps. A project with NO index is deliberately not reported here: that is the
// gap probe's answer, printVerdict already renders it as "outside coverage", and one fact
// stated twice in two vocabularies teaches a reader to skip both.
func staleIndexProjects(ctx context.Context, root string) []string {
	ws, err := inspectWorkspace(ctx, root)
	if err != nil || ws == nil {
		return nil
	}
	projects, err := ws.ListProjects(ctx)
	if err != nil {
		return nil
	}
	cacheDir, err := magus.ResolveCacheDir(ws.Root(), magus.WithLoadedConfig(globalCfg))
	if err != nil {
		return nil
	}
	var stale []string
	for _, p := range projects.Projects {
		// IndexPath is the conventional location. A project that declared its index
		// somewhere else through knowledge.symbols is not found here and draws silence,
		// which is the right way to be wrong: an override reported as stale would be a
		// banner nobody can clear.
		info, serr := os.Stat(symbols.IndexPath(cacheDir, p.Dir))
		if serr != nil {
			continue
		}
		if sourceNewerThan(p.Dir, info.ModTime()) {
			path := p.Path
			if path == "" {
				path = "."
			}
			stale = append(stale, path)
		}
	}
	slices.Sort(stale)
	return stale
}

// sourceNewerThan reports whether any file under dir was modified after cutoff.
//
// It stops at the FIRST one. The question is whether the index is behind, not by how much
// or because of what, so the walk is over the moment one file answers it - which is the
// common case in a session that has been editing, and the case where the walk would
// otherwise cost the most.
func sourceNewerThan(dir string, cutoff time.Time) bool {
	seen, newer := 0, false
	// The error is swallowed at every level: an unreadable subtree means magus cannot
	// establish staleness there, and a probe that cannot establish it says nothing.
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // an unreadable subtree is skipped, not fatal
		}
		if d.IsDir() {
			// The root itself is never pruned - a project directory whose own name looks
			// ignorable is still the project being asked about.
			if path != dir && project.IsIgnoreDir(d.Name()) {
				return fs.SkipDir
			}
			return nil
		}
		if seen++; seen > staleIndexScanLimit {
			return fs.SkipAll
		}
		// An entry that vanished between the walk and the stat says nothing about
		// freshness either way, so it is skipped rather than guessed at.
		if info, ierr := d.Info(); ierr == nil && info.ModTime().After(cutoff) {
			newer = true
			return fs.SkipAll
		}
		return nil
	})
	return newer
}

// The guard's half of the same fact. Half (a) above is the load-bearing one: it works on
// every host with nothing wired, because it rides the command's own output. This one
// reaches a host that runs a pre-tool hook, and it arrives one call EARLIER - before the
// stale answer is read rather than under it.
//
// It is an advisory and never a deny. A stale index still answers, the answer is still
// worth having, and blocking a read to enforce tidiness is the shape of guard rule this
// project has already reverted once.

// graphReadSubcommands are the verbs that answer FROM the index, so a stale one changes
// what they report.
var graphReadSubcommands = []string{"refs", "query", "explain", "path"}

// commandReadsGraph reports whether a shell line would run one of them.
//
// Through the parser rather than a pattern, for the reason commandRunsGate is: it resolves
// quoting and peels wrappers, so a launcher prefix reads the same as the bare form and the
// word `refs` inside a quoted argument reads as neither.
//
// `magus query output <ref>` is exempt. It reads a captured log, not the graph, and it is
// the same exemption the output-pipe rule carves out for the same reason.
func commandReadsGraph(command string) bool {
	cmds, ok := parseGuardCommands(command)
	if !ok {
		return false
	}
	for _, c := range cmds {
		if c.Name != "magus" || len(c.Args) == 0 {
			continue
		}
		if !slices.Contains(graphReadSubcommands, c.Args[0]) {
			continue
		}
		if c.Args[0] == "query" && len(c.Args) >= 2 && c.Args[1] == "output" {
			continue
		}
		return true
	}
	return false
}

// staleGraphAdvice is what the guard says to a graph read about to answer from an index
// older than the tree, or "" when every built index is current.
func staleGraphAdvice(ctx context.Context) string {
	stale := staleIndexProjects(ctx, "")
	if len(stale) == 0 {
		return ""
	}
	return fmt.Sprintf("magus workspace: run `%s` first, then ask again. The answer you are about to get is drawn from an index built before the current sources.\n", hint.GraphBuild) +
		fmt.Sprintf("%s changed since %s last indexed %s: %s. A symbol added or moved since then is missing from the answer, and a lookup that misses it reports \"unknown, not absent\" rather than nothing being there.\n",
			plural(len(stale), "One project", "Several projects"), hint.GraphBuild, plural(len(stale), "it", "them"), strings.Join(stale, ", ")) +
		"This is an advisory: a stale index still holds true facts, so the read is worth running either way."
}
