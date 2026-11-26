package job

import (
	"cmp"
	"context"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/bmatcuk/doublestar/v4"

	"github.com/egladman/magus/libs/safetyscissors/job"
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
	// InPlace marks a file the target rewrites where it stands (editsExistingSource).
	InPlace bool `json:"in_place,omitempty" yaml:"in_place,omitempty"`
}

// Project is the project of the producing target.
func (o RegeneratedOutput) Project() string {
	project, _, _ := strings.Cut(o.Target, ":")
	return project
}

// Holds reports whether file is one the declared output names.
func (o RegeneratedOutput) Holds(file string) bool {
	pattern, except, _ := strings.Cut(o.Output, " !")
	g := types.Glob{Pattern: pattern}
	if except != "" {
		g.Except = strings.Split(except, " !")
	}
	return g.Match(file)
}

// RegeneratedDelivered are the outputs `job wait` may note in a diff instead of
// refusing. In-place updates are left out: they rewrite someone else's source.
// Nothing is read unless a changed path falls outside the write paths.
func RegeneratedDelivered(root string, row job.Job, changed []string, ws Workspace) []RegeneratedOutput {
	if row.ReadOnly || row.Integrating() || ws.Unavailable != "" {
		return nil
	}
	outside := slices.ContainsFunc(changed, func(p string) bool {
		_, inside := job.Matching(row.Paths.Write, p)
		return !inside && !types.IsMagusMaintained(p)
	})
	if !outside {
		return nil
	}
	outputs := RegeneratedOutside(root, row, ws.Projects, ws.Ignored)
	return slices.DeleteFunc(outputs, func(o RegeneratedOutput) bool { return o.InPlace })
}

// RegeneratedLimit is how many outputs a text rendering lists before it counts the rest.
const RegeneratedLimit = 12

// RegeneratedOutside lists the outputs and in-place updates (ctx.writesFiles) outside the
// row's write paths whose target reads a file the row may write. A read-only row gets nil.
// A target reads its ctx.readsFiles, else its project's sources. Ignored outputs are dropped,
// judged by a few probe files (see [outputProbes]); ignored may be nil. Most specific source
// first. Declarations only.
func RegeneratedOutside(root string, row job.Job, projects []*types.Project, ignored func([]string) ([]string, error)) []RegeneratedOutput {
	if row.ReadOnly || len(row.Paths.Write) == 0 {
		return nil
	}
	type candidate struct {
		out         RegeneratedOutput
		glob        types.Glob
		specificity int
	}
	type declared struct {
		glob    types.Glob
		inPlace bool
	}
	var (
		written []string
		walked  bool
		found   []candidate
	)
	for _, p := range projects {
		for _, name := range slices.Sorted(maps.Keys(targetsWriting(p))) {
			var outside []declared
			add := func(g types.Glob, inPlace bool) {
				if !outputInside(row, g) && !slices.ContainsFunc(outside, func(o declared) bool { return o.glob.Compare(g) == 0 }) {
					outside = append(outside, declared{g, inPlace})
				}
			}
			for _, ref := range p.TargetOutputs[name] {
				add(ref.Rooted(p.Path), false)
			}
			for _, ref := range p.TargetUpdates[name] {
				add(ref.Rooted(p.Path), true)
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
					out:         RegeneratedOutput{Output: g.glob.String(), Target: fmt.Sprintf("%s:%s", p.Path, name), Source: source.String(), InPlace: g.inPlace},
					glob:        g.glob,
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
		// A failed query ignores nothing: listing an output the VCS would have hidden costs a
		// line, while hiding a committed one is the miss this exists to prevent.
		hidden, _ := ignored(all)
		for _, p := range hidden {
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

func targetsWriting(p *types.Project) map[string]struct{} {
	names := map[string]struct{}{}
	for name := range p.TargetOutputs {
		names[name] = struct{}{}
	}
	for name := range p.TargetUpdates {
		names[name] = struct{}{}
	}
	return names
}

// VCSIgnored answers RegeneratedOutside's ignored question from the VCS at root, or returns
// nil when there is none to ask. A query that fails returns its error, and each caller
// decides what an unanswered question means.
func VCSIgnored(ctx context.Context, root string) func([]string) ([]string, error) {
	res, err := vcs.Resolve(ctx, root, "", types.VCSOptions{})
	if err != nil || res.VCS == nil {
		return nil
	}
	return func(paths []string) ([]string, error) {
		if len(paths) == 0 {
			return nil, nil
		}
		ignored, err := res.VCS.IgnoredFiles(ctx, root, paths)
		if err != nil {
			return nil, fmt.Errorf("%s could not say which paths it ignores: %w", res.Name, err)
		}
		return ignored, nil
	}
}

const regeneratedHeading = "regenerated outside the write paths, from sources inside them"

// renderRegenerated writes outputs as the block [Terms] renders. Nothing is written for none.
func renderRegenerated(w io.Writer, outputs []RegeneratedOutput) {
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
		if o.InPlace {
			lines = append(lines, fmt.Sprintf("%s (%s, rewrites in place)", o.Output, o.Target))
			continue
		}
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
		"never hand-edit them: regenerate them with their target. `job wait` notes one in the diff instead of refusing it, and the integrator regenerates it again; unstage what the target named beside one writes")
}

// outputInside reports whether the row's write paths hold every file g can name, read the
// way covers reads a write: the output's pattern is the path asked about, so
// `docs/rules/*.md` holds `docs/rules/*.md` and `docs` holds both. A deny path touching
// the output takes some of it back out.
func outputInside(row job.Job, g types.Glob) bool {
	if slices.ContainsFunc(row.Paths.Deny, func(d string) bool { return job.PathsIntersect(d, g.Pattern) }) {
		return false
	}
	return slices.ContainsFunc(row.Paths.Write, func(w string) bool {
		p, decl := job.SplitClaim(w)
		return decl == "" && job.CoversPath(p, g.Pattern)
	})
}

// writtenFiles expands the row's write paths into the files they name: a glob against the
// tree, a literal directory to the files beneath it, and any other literal as itself,
// since a job may create the file it names. A file a deny path excludes whole is dropped.
func writtenFiles(root string, row job.Job) []string {
	tree := os.DirFS(root)
	var out []string
	for _, entry := range row.Paths.Write {
		p, _ := job.SplitClaim(entry)
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
			if _, denied := job.Matching(WholeFileDenies(row.Paths.Deny, f), f); !denied && !slices.Contains(out, f) {
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
		if n := len(job.LiteralPrefix(g.Pattern)); n > specificity && slices.ContainsFunc(written, g.Match) {
			best, specificity = g, n
		}
	}
	return best, specificity, specificity >= 0
}

// outputProbes are the paths that stand for g when asking the VCS whether it ignores it:
// the first outputProbeLimit files g matches within outputProbeDirs directories, or the
// pattern itself read as a path when that finds none, which an ignore rule on any
// directory above it, or on its file name, still catches.
func outputProbes(root string, g types.Glob) []string {
	if !types.IsLiteralGlob(g.Pattern) {
		var matches []string
		tree := &boundedDirs{FS: os.DirFS(root), left: outputProbeDirs}
		_ = doublestar.GlobWalk(tree, g.Pattern, func(p string, _ fs.DirEntry) error {
			if g.Excludes(p) {
				return nil
			}
			if matches = append(matches, p); len(matches) == outputProbeLimit {
				return fs.SkipAll
			}
			return nil
		}, doublestar.WithFilesOnly(), doublestar.WithNoFollow())
		if len(matches) > 0 {
			return matches
		}
	}
	return []string{g.Pattern}
}

// outputProbeLimit is how many of an output's files stand for it. An ignore rule covers a
// generated tree whole or not at all in practice, and matching every file of a glob such
// as docs/gen/** walked thousands of files on every fork and describe that listed it.
const outputProbeLimit = 16

// outputProbeDirs bounds the directories one probe reads: a glob matching few files under
// a wide tree, such as **/gen/*.go, otherwise walked the whole checkout to find them.
const outputProbeDirs = 64

// boundedDirs is an fs.FS that lists at most left directories. GlobWalk skips a directory
// it cannot list, so a spent budget ends the walk without failing it.
type boundedDirs struct {
	fs.FS
	left int
}

func (b *boundedDirs) ReadDir(name string) ([]fs.DirEntry, error) {
	if b.left <= 0 {
		return nil, fs.ErrPermission
	}
	b.left--
	return fs.ReadDir(b.FS, name)
}
