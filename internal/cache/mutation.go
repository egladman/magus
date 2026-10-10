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
// describes a file that is not there. One that appeared does not: a target may
// produce a file a broad source glob would have matched, which is MGS1028's question.
func mutatedSources(before, after sourceFingerprint, updates, ownedOutputs []types.Glob) []string {
	declared := compileGlobs(slices.Concat(updates, ownedOutputs))
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

// movedInputs names every hashed input that changed while the step ran, whoever changed it,
// declared updates included.
//
// It must NOT share the exemptions of [mutatedSources], which answers "did this target
// misbehave" and so ignores every declared update and output. This answers "does the key
// still describe the tree it was computed from", where a peer rewriting a generated file
// mid-hash is the whole danger. A declared update counts too: updates are never snapshotted
// or replayed, so an entry filed under the pre-run bytes would, once the file returned to
// them, hit and leave it as the run would not. Only a fixed point records, so a formatter
// caches from its first run that changes nothing and an updater that rewrites its file on
// every run never caches.
func movedInputs(before, after sourceFingerprint) []string {
	return mutatedSources(before, after, nil, nil)
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

// keyStillDescribesInputs reports whether this step's hashed inputs are unchanged, and names
// what moved when they are not.
//
// Callers REFUSE THE ENTRY, never the run: the work succeeded and its output is good, and
// only filing it under this key is unsafe, since a later run that really does hash to it
// would replay bytes built from different sources. Failing instead would punish the target
// that was read rather than the one that wrote.
//
// True when the fingerprint cannot be taken: a tree magus cannot stat twice is evidence of
// nothing, and the alternative stops caching whenever a stat races.
func (c *Cache) keyStillDescribesInputs(ctx context.Context, s *Step, before sourceFingerprint) ([]string, bool) {
	if before == nil {
		return nil, true
	}
	after, err := c.fingerprintSources(ctx, s)
	if err != nil {
		c.log.DebugContext(ctx, "cache.debug", slog.String("msg",
			fmt.Sprintf("key staleness check skipped for %s: %v", s.ProjectPath, err)))
		return nil, true
	}
	moved := movedInputs(before, after)
	return moved, len(moved) == 0
}

// reportUnrecorded says why a run was not recorded. A moved declared update is the step
// doing its job short of a fixed point, which every run that changes a file does, so alone
// it is logged at debug; any other moved input gets [movedInputsNotice].
func (c *Cache) reportUnrecorded(ctx context.Context, s Step, hash string, moved []string) {
	updates := compileGlobs(s.Updates)
	others := slices.DeleteFunc(slices.Clone(moved), func(rel string) bool {
		return slices.ContainsFunc(updates, func(g compiledGlob) bool { return g.Match(rel) })
	})
	if len(others) > 0 {
		c.log.WarnContext(ctx, movedInputsNotice(s, hash, others))
		return
	}
	c.log.DebugContext(ctx, "cache.debug", slog.String("msg", fmt.Sprintf(
		"not recording %s:%s under %s: it rewrote %s it declares as updates, and a hit leaves them as they are: %s",
		s.ProjectPath, s.Target, shortHash(hash), pluralFiles(len(moved)), joinCapped(moved, 5))))
}

// movedInputsNotice explains why the run was not cached. A moved input matching a declared
// output was rewritten by this step's own chain after the key was taken, so the step never
// caches (MGS4010); any other moved input came from outside and the next run caches.
func movedInputsNotice(s Step, hash string, moved []string) string {
	declared := compileGlobs(s.OwnedOutputs)
	var generated []string
	for _, rel := range moved {
		for _, g := range declared {
			if g.Match(rel) {
				generated = append(generated, rel)
				break
			}
		}
	}
	if len(generated) == 0 {
		return fmt.Sprintf("magus/cache: not recording %s:%s under %s: %s changed while it ran, so the key no longer describes its inputs: %s",
			s.ProjectPath, s.Target, shortHash(hash), pluralFiles(len(moved)), joinCapped(moved, 5))
	}
	return types.FormatDiagnostic(types.SelfInvalidatingKey, fmt.Sprintf(
		"%s:%s is never cached: its key reads %s its own run generates after the key is taken: %s. "+
			"Generate them before this target's key, as a skip_cache target it composes or a target it depends on",
		s.ProjectPath, s.Target, pluralFiles(len(generated)), joinCapped(generated, 5)))
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
