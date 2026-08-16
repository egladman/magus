package cache

import (
	"context"
	"fmt"
	"log/slog"
	"slices"

	"github.com/egladman/magus/types"
)

// sourceFingerprint maps a source file's workspace-relative path to its content hash.
// Content, not mtime: a tool that rewrites identical bytes changed nothing a key can
// see, and reporting it would train readers to ignore MGS4007.
type sourceFingerprint map[string]string

// fingerprintSources returns the per-file view hashStep computes and discards. memo
// stays nil: this runs on a path that EXECUTES a target, where a memoized hash is
// precisely the stale answer.
func (c *Cache) fingerprintSources(ctx context.Context, s *Step) (sourceFingerprint, error) {
	files, hashes, _, err := c.expandAndHashSources(ctx, s, nil)
	if err != nil {
		return nil, err
	}
	fp := make(sourceFingerprint, len(files))
	for i, f := range files {
		fp[f.rel] = hashes[i]
	}
	return fp, nil
}

// mutatedSources returns the changed source files no declared glob claims, sorted.
//
// A source that DISAPPEARED counts: deleting an input is a write, and the key then
// describes a file that is not there. One that appeared does not - a target may
// produce a file a broad source glob would have matched, which is MGS1028's question.
func mutatedSources(before, after sourceFingerprint, updates, ownedOutputs []string) []string {
	declared := compileGlobs(append(slices.Clone(updates), ownedOutputs...))
	claimed := func(rel string) bool {
		for _, g := range declared {
			if g.Match(rel) {
				return true
			}
		}
		return false
	}

	var out []string
	for rel, was := range before {
		now, still := after[rel]
		if still && now == was {
			continue
		}
		if claimed(rel) {
			continue
		}
		out = append(out, rel)
	}
	slices.Sort(out)
	return out
}

// checkSourceMutation reports MGS4007 when a target rewrote its own declared sources
// without declaring them via ctx.modifiesExistingFiles. Callers invoke it only after
// fn succeeded, so it never stacks on top of a target's own failure.
func (c *Cache) checkSourceMutation(ctx context.Context, s *Step, before sourceFingerprint) error {
	if before == nil {
		return nil
	}
	after, err := c.fingerprintSources(ctx, s)
	if err != nil {
		// An assertion about the run, not part of it: a tree that moved underfoot
		// between the two passes must not turn a passing target red. Logged rather
		// than dropped, so a check that stops running is still observable.
		c.log.DebugContext(ctx, "cache.debug", slog.String("msg",
			fmt.Sprintf("source mutation check skipped for %s: %v", s.ProjectPath, err)))
		return nil
	}
	changed := mutatedSources(before, after, s.Updates, s.OwnedOutputs)
	if len(changed) == 0 {
		return nil
	}
	return types.DiagnosticErrorf(types.UndeclaredSourceModified,
		"%s:%s modified %s it declared as sources; declare them with ctx.modifiesExistingFiles(...) or stop writing them: %s",
		s.ProjectPath, s.Target, pluralFiles(len(changed)), joinCapped(changed, 5))
}

func pluralFiles(n int) string {
	if n == 1 {
		return "1 file"
	}
	return fmt.Sprintf("%d files", n)
}

// joinCapped names at most max paths, then says how many it withheld: one path is
// enough to find the writing tool, and a formatter would otherwise print hundreds.
func joinCapped(paths []string, max int) string {
	if len(paths) <= max {
		return joinComma(paths)
	}
	return fmt.Sprintf("%s (and %d more)", joinComma(paths[:max]), len(paths)-max)
}

func joinComma(paths []string) string {
	out := ""
	for i, p := range paths {
		if i > 0 {
			out += ", "
		}
		out += p
	}
	return out
}
