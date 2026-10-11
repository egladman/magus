package job

import (
	"cmp"
	"context"
	"fmt"
	"io"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"

	"github.com/bmatcuk/doublestar/v4"

	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
)

// RegeneratedOutput is one declared output a target regenerates from a source inside a
// job's write paths while the output itself sits outside them.
type RegeneratedOutput struct {
	// Output is the workspace-rooted glob as the target declares it, exclusions included.
	Output string `json:"output" yaml:"output"`
	// Target is the producing target, `<project>:<target>`.
	Target string `json:"target" yaml:"target"`
	// Source is the target's source glob that matched a written file: the most specific one
	// when several do.
	Source string `json:"source" yaml:"source"`
}

// RegeneratedLimit is how many outputs a text rendering lists before it counts the rest.
const RegeneratedLimit = 12

// RegeneratedOutside is every declared output (ctx.writesFiles) of a target whose declared
// sources match a file the row may write, where the output is not itself inside the row's
// write paths. A read-only row writes nothing and gets nil.
//
// A target's sources are its ctx.readsFiles, or its project's sources when it declares
// none: the footprint the cache keys it on. A write path is expanded against the tree
// under root, and a literal one names a file even before it exists. An output whose every
// file the VCS ignores is dropped, because it never reaches the diff the job is graded on;
// ignored may be nil, which drops nothing.
//
// Most specific first, by the literal prefix of the matching source glob: a target reading
// `internal/cli/**/*.go` sorts before one reading `**/*.go`, and a target that declares no
// reads of its own sorts after every one that does.
//
// It sees declarations only. A coupling nobody declares, such as a version constant a test
// pins, is not here.
func RegeneratedOutside(root string, row types.Job, projects []*types.Project, ignored func([]string) []string) []RegeneratedOutput {
	if row.ReadOnly || len(row.WritePaths) == 0 {
		return nil
	}
	type candidate struct {
		out         RegeneratedOutput
		glob        types.Glob
		specificity int
	}
	var (
		written []string
		walked  bool
		found   []candidate
	)
	for _, p := range projects {
		for _, name := range slices.Sorted(maps.Keys(p.TargetOutputs)) {
			var outside []types.Glob
			for _, ref := range p.TargetOutputs[name] {
				if g := ref.Rooted(p.Path); !outputInside(row, g) && !slices.ContainsFunc(outside, func(o types.Glob) bool { return o.Compare(g) == 0 }) {
					outside = append(outside, g)
				}
			}
			if len(outside) == 0 {
				continue
			}
			// The walk waits for the first output outside the paths, so a row whose paths
			// already hold every output never expands a glob.
			if !walked {
				written, walked = writtenFiles(root, row), true
			}
			sources, declared := targetSources(p, name)
			source, specificity, ok := sourcedBy(sources, written)
			if !ok {
				continue
			}
			if declared {
				specificity++
			} else {
				specificity = 0
			}
			for _, g := range outside {
				found = append(found, candidate{
					out:         RegeneratedOutput{Output: g.String(), Target: p.Path + ":" + name, Source: source.String()},
					glob:        g,
					specificity: specificity,
				})
			}
		}
	}
	if ignored != nil && len(found) > 0 {
		probes := make([][]string, len(found))
		var all []string
		for i, c := range found {
			probes[i] = outputProbes(root, c.glob)
			all = append(all, probes[i]...)
		}
		gone := map[string]bool{}
		for _, p := range ignored(all) {
			gone[filepath.ToSlash(p)] = true
		}
		kept := found[:0]
		for i, c := range found {
			if slices.ContainsFunc(probes[i], func(p string) bool { return !gone[p] }) {
				kept = append(kept, c)
			}
		}
		found = kept
	}
	if len(found) == 0 {
		return nil
	}
	slices.SortStableFunc(found, func(a, b candidate) int {
		return cmp.Or(
			cmp.Compare(b.specificity, a.specificity),
			cmp.Compare(a.out.Target, b.out.Target),
			cmp.Compare(a.out.Output, b.out.Output),
		)
	})
	out := make([]RegeneratedOutput, len(found))
	for i, c := range found {
		out[i] = c.out
	}
	return out
}

// VCSIgnored answers RegeneratedOutside's ignored question from the VCS at root, or returns
// nil when there is none to ask. A query that fails ignores nothing: listing an output the
// VCS would have hidden costs a line, while hiding a committed one is the miss this exists
// to prevent.
func VCSIgnored(ctx context.Context, root string) func([]string) []string {
	res, err := vcs.Resolve(ctx, root, "", types.VCSOptions{})
	if err != nil || res.VCS == nil {
		return nil
	}
	return func(paths []string) []string {
		if len(paths) == 0 {
			return nil
		}
		ignored, err := res.VCS.IgnoredFiles(ctx, root, paths)
		if err != nil {
			return nil
		}
		return ignored
	}
}

const regeneratedHeading = "regenerated outside the write paths, from sources inside them"

// RenderRegenerated writes outputs as the block [Terms] renders, for a command that prints
// no terms. Nothing is written for none.
func RenderRegenerated(w io.Writer, outputs []RegeneratedOutput) {
	lines := regeneratedLines(outputs)
	if len(lines) == 0 {
		return
	}
	fmt.Fprintln(w, regeneratedHeading)
	for _, l := range lines {
		fmt.Fprintf(w, "  %s\n", l)
	}
}

// regeneratedLines is one line per output, in the order RegeneratedOutside ranked them,
// capped at RegeneratedLimit, then who regenerates them, phrased for the holder.
func regeneratedLines(outputs []RegeneratedOutput) []string {
	if len(outputs) == 0 {
		return nil
	}
	var lines []string
	for _, o := range outputs[:min(len(outputs), RegeneratedLimit)] {
		lines = append(lines, fmt.Sprintf("%s (%s)", o.Output, o.Target))
	}
	if rest := outputs[min(len(outputs), RegeneratedLimit):]; len(rest) > 0 {
		targets := map[string]bool{}
		for _, o := range rest {
			targets[o.Target] = true
		}
		lines = append(lines, fmt.Sprintf("and %d more from %d target(s); -o json lists every one", len(rest), len(targets)))
	}
	return append(lines,
		"the root regenerates these after integration: never hand-edit them; if this job must regenerate them itself, ask the root to widen the write paths")
}

// outputInside reports whether the row's write paths hold every file g can name, read the
// way covers reads a write: the output's pattern is the path asked about, so
// `docs/rules/*.md` holds `docs/rules/*.md` and `docs` holds both. A deny path touching
// the output takes some of it back out.
func outputInside(row types.Job, g types.Glob) bool {
	if slices.ContainsFunc(row.DenyPaths, func(d string) bool { return types.PathsIntersect(d, g.Pattern) }) {
		return false
	}
	return slices.ContainsFunc(row.WritePaths, func(w string) bool {
		p, decl := types.SplitClaim(w)
		return decl == "" && coversPath(p, g.Pattern)
	})
}

// writtenFiles expands the row's write paths into the files they name: a glob against the
// tree, a literal directory to the files beneath it, and any other literal as itself,
// since a job may create the file it names. A file a deny path excludes whole is dropped.
func writtenFiles(root string, row types.Job) []string {
	tree := os.DirFS(root)
	var out []string
	for _, entry := range row.WritePaths {
		p, _ := types.SplitClaim(entry)
		if p = path.Clean(p); p == "" {
			continue
		}
		var files []string
		switch {
		case types.IsGlobMeta(p):
			files, _ = doublestar.Glob(tree, p, doublestar.WithFilesOnly(), doublestar.WithNoFollow())
		case isDir(root, p):
			files, _ = doublestar.Glob(tree, path.Join(p, "**"), doublestar.WithFilesOnly(), doublestar.WithNoFollow())
		default:
			files = []string{p}
		}
		for _, f := range files {
			if _, denied := matching(WholeFileDenies(row.DenyPaths, f), f); !denied && !slices.Contains(out, f) {
				out = append(out, f)
			}
		}
	}
	return out
}

func isDir(root, rel string) bool {
	info, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel)))
	return err == nil && info.IsDir()
}

// targetSources is the source footprint buildStep keys target on, short of the magusfiles
// every target shares: a magusfile edit would otherwise name every generator there is.
// declared reports a footprint the target states itself rather than its project's.
func targetSources(p *types.Project, target string) (sources []types.Glob, declared bool) {
	if refs := p.TargetInputs[target]; len(refs) > 0 {
		out := make([]types.Glob, len(refs))
		for i, ref := range refs {
			out[i] = ref.Rooted(p.Path)
		}
		return out, true
	}
	out := make([]types.Glob, len(p.Sources))
	for i, g := range p.Sources {
		out[i] = g.Root(p.Path)
	}
	return out, false
}

// sourcedBy is the source glob with the longest literal prefix among those matching a
// written file, and that prefix's length.
func sourcedBy(sources []types.Glob, written []string) (best types.Glob, specificity int, ok bool) {
	specificity = -1
	for _, g := range sources {
		if n := len(types.LiteralPrefix(g.Pattern)); n > specificity && slices.ContainsFunc(written, g.Match) {
			best, specificity = g, n
		}
	}
	return best, specificity, specificity >= 0
}

// outputProbes are the paths that stand for g when asking the VCS whether it ignores it:
// the files g matches now, or the pattern itself read as a path when it matches none yet,
// which an ignore rule on any directory above it still catches.
func outputProbes(root string, g types.Glob) []string {
	if !types.IsLiteralGlob(g.Pattern) {
		matches, _ := doublestar.Glob(os.DirFS(root), g.Pattern, doublestar.WithFilesOnly(), doublestar.WithNoFollow())
		if matches = slices.DeleteFunc(matches, g.Excludes); len(matches) > 0 {
			return matches
		}
	}
	return []string{g.Pattern}
}
