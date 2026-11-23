//go:build !wasm

package std

import (
	"context"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/edit"
	"github.com/egladman/magus/types"
)

// Splice is the "splice" host module, the Buzz door to internal/edit.
// No method raises: a refusal is data in the value returned, and
// diff and apply return failures in their Result.
var Splice = Module{
	Name: "splice",
	Doc:  "Explicit file edits: plan literal and regex replacements and new files, grade them under the lease, preview the diff and apply the plan.",
	Methods: []Method{
		{
			Name:    "replace",
			Doc:     "A change replacing old with new in path. old must occur exactly once when planned.",
			Args:    []Arg{{Name: "path"}, {Name: "old"}, {Name: "new"}},
			Returns: []Ret{{Object: "EditChange"}},
			Impl:    SpliceReplace,
		},
		{
			Name:    "replace_all",
			Doc:     "A change replacing every occurrence of old with new in path. old must occur at least once when planned. old inside an occurrence of new is not replaced, so `a -> ab` applies once.",
			Args:    []Arg{{Name: "path"}, {Name: "old"}, {Name: "new"}},
			Returns: []Ret{{Object: "EditChange"}},
			Impl:    SpliceReplaceAll,
		},
		{
			Name:    "replace_regex",
			Doc:     "A change replacing every match of the RE2 pattern in path with template, expanded with $1 and ${name}. A pattern that does not compile is a refusal plan reports.",
			Args:    []Arg{{Name: "path"}, {Name: "pattern"}, {Name: "template"}},
			Returns: []Ret{{Object: "EditChange"}},
			Impl:    SpliceReplaceRegex,
		},
		{
			Name:    "create",
			Doc:     "A change creating path holding content. path must not exist when planned, unless it already holds exactly content.",
			Args:    []Arg{{Name: "path"}, {Name: "content"}},
			Returns: []Ret{{Object: "EditChange"}},
			Impl:    SpliceCreate,
		},
		{
			Name:    "blocks",
			Doc:     "Parse a block document: `## <path>` sections of fenced old, old all, pattern, new and create blocks. name labels its refusals. A document with a refusal carries no changes.",
			Args:    []Arg{{Name: "text"}, {Name: "name", Optional: true, Default: "<document>"}},
			Returns: []Ret{{Object: "EditDocument"}},
			Impl:    SpliceBlocks,
		},
		{
			Name:    "plan",
			Doc:     "Resolve changes against the tree and grade every file under the lease, writing nothing. Paths resolve against the working directory as fs paths do. Refusals, and changes the tree already holds, are fields of the plan.",
			Args:    []Arg{{Name: "changes", Object: "[EditChange]"}},
			Returns: []Ret{{Object: "SplicePlan"}},
			Impl:    SplicePlan,
		},
		{
			Name:    "diff",
			Doc:     "Render a plan as a unified diff. The Result carries the complete patch or the refusal. A created file diffs against nothing. context is the unchanged lines around each hunk, 3 by default.",
			Args:    []Arg{{Name: "plan", Object: "SplicePlan"}, {Name: "context", Optional: true, Default: 3}},
			Returns: []Ret{{Object: `result\Result::<str>`}},
			Impl:    SpliceDiff,
		},
		{
			Name:    "apply",
			Doc:     "Plan the changes again and publish each file. A refusal prevents publication. A write failure triggers restoration of preceding writes and the Result reports any restoration failure. A plan whose every change the tree holds succeeds and writes nothing.",
			Args:    []Arg{{Name: "plan", Object: "SplicePlan"}},
			Returns: []Ret{{Object: `result\Result::<EditApplied>`}},
			Impl:    SpliceApply,
		},
	},
}

// SpliceReplace is splice\replace.
func SpliceReplace(_ context.Context, path, old, repl string) (types.EditChange, error) {
	return types.EditChange{Path: path, Old: old, New: repl, Count: 1}, nil
}

// SpliceReplaceAll is splice\replaceAll.
func SpliceReplaceAll(_ context.Context, path, old, repl string) (types.EditChange, error) {
	return types.EditChange{Path: path, Old: old, New: repl, Count: -1}, nil
}

// SpliceReplaceRegex is splice\replaceRegex.
func SpliceReplaceRegex(_ context.Context, path, pattern, template string) (types.EditChange, error) {
	return types.EditChange{Path: path, Old: pattern, New: template, Count: -1, Regex: true}, nil
}

// SpliceCreate is splice\create.
func SpliceCreate(_ context.Context, path, content string) (types.EditChange, error) {
	return types.EditChange{Path: path, New: content, Create: true}, nil
}

// SpliceBlocks is splice\blocks.
func SpliceBlocks(_ context.Context, text, name string) (types.EditDocument, error) {
	return edit.ParseBlocks(name, []byte(text)), nil
}

// editRun is one expansion and resolution of a plan's changes.
type editRun struct {
	expansion edit.Expansion
	plan      *edit.Plan
	refused   []types.EditRefusal
}

// planEdits expands changes against the working directory and
// resolves them with grade. A path the read policy denies is
// refused before anything reads it.
func planEdits(ctx context.Context, changes []types.EditChange, grade func(root string) edit.Option) editRun {
	root, err := EffectiveCurrentWorkingDirectory(ctx)
	if err != nil {
		return editRun{plan: edit.Resolve("", edit.Request{}), refused: []types.EditRefusal{
			{Reason: fmt.Sprintf("no working directory to resolve paths against: %v", err)}}}
	}
	var denied []types.EditRefusal
	readable := make([]types.EditChange, 0, len(changes))
	for _, c := range changes {
		if err := ctx.Err(); err != nil {
			return editRun{refused: []types.EditRefusal{{Path: c.Path, Reason: err.Error()}}}
		}
		if !filepath.IsAbs(c.Path) && c.Path != "" {
			if err := checkRead(ctx, filepath.Join(root, filepath.FromSlash(c.Path))); err != nil {
				denied = append(denied, types.EditRefusal{Path: c.Path, Reason: err.Error()})
				continue
			}
		}
		readable = append(readable, c)
	}
	x := edit.Expand(ctx, root, readable, committedFiles(ctx, root))
	if len(changes) > 0 && len(readable) == 0 {
		x.Refused = nil
	}
	p := edit.Resolve(root, edit.Request{Sites: x.Sites, Creates: x.Creates}, grade(root))
	return editRun{expansion: x, plan: p, refused: slices.Concat(denied, x.Refused, p.Refused())}
}

// committedFiles reads files under root at the committed revision
// of the repository holding root, or is nil outside one.
func committedFiles(ctx context.Context, root string) edit.CommittedFile {
	drv, _, err := resolveVCSAt(ctx, root)
	if err != nil {
		return func(string) (string, error) { return "", fmt.Errorf("resolve the VCS holding %s: %w", root, err) }
	}
	if drv == nil {
		return nil
	}
	return func(rel string) (string, error) {
		top, err := drv.Root(ctx, root)
		if err != nil {
			return "", err
		}
		// A driver's paths are relative to the real top level, and
		// root may reach it through a symlink.
		realTop, err := filepath.EvalSymlinks(top)
		if err != nil {
			return "", err
		}
		realRoot, err := filepath.EvalSymlinks(root)
		if err != nil {
			return "", err
		}
		path, err := filepath.Rel(realTop, filepath.Join(realRoot, filepath.FromSlash(rel)))
		if err != nil {
			return "", err
		}
		return drv.ReadFileAt(ctx, top, "", filepath.ToSlash(path))
	}
}

// leaseGrade grades a file as an fs write of it is graded: the
// sandbox write check, then the edit judge on an existing file.
func leaseGrade(ctx context.Context) func(root string) edit.Option {
	return func(root string) edit.Option {
		return edit.WithGrade(func(rel string, _, after []byte) (string, string) {
			abs := filepath.Join(root, filepath.FromSlash(rel))
			if err := checkWrite(ctx, abs); err != nil {
				return "", err.Error()
			}
			if err := checkEdit(ctx, abs, replacing(after)); err != nil {
				return "", err.Error()
			}
			return "", ""
		})
	}
}

// SplicePlan is splice\plan. Every outcome, a refusal included, is
// the returned plan.
func SplicePlan(ctx context.Context, changes []types.EditChange) (types.EditPlan, error) {
	run := planEdits(ctx, changes, leaseGrade(ctx))
	out := types.EditPlan{
		Changes: changes,
		Files:   []types.EditedFile{},
		Spans:   []types.EditSpan{},
		Already: run.expansion.Already,
		Refused: run.refused,
	}
	if len(run.refused) > 0 {
		return out, nil
	}
	out.Digest = run.plan.Digest()
	out.Files = run.plan.Files()
	if spans := run.plan.Spans(); spans != nil {
		out.Spans = spans
	}
	out.MakesDirs = run.plan.MakesDirs()
	out.NothingToDo = len(out.Files) == 0
	return out, nil
}

// SpliceDiff is splice\diff. It renders the tree as it reads now, so
// a file changed since the plan diffs as it would be written.
func SpliceDiff(ctx context.Context, plan types.EditPlan, contextLines int) (types.Result[string], error) {
	if len(plan.Refused) > 0 {
		return types.Fail[string](editRefused(plan.Refused)), nil
	}
	type contents struct{ before, after []byte }
	seen := map[string]contents{}
	capture := func(string) edit.Option {
		return edit.WithGrade(func(rel string, before, after []byte) (string, string) {
			seen[rel] = contents{before, after}
			return "", ""
		})
	}
	if run := planEdits(ctx, plan.Changes, capture); len(run.refused) > 0 {
		return types.Fail[string](editRefused(run.refused)), nil
	}
	var b strings.Builder
	for _, rel := range slices.Sorted(maps.Keys(seen)) {
		c := seen[rel]
		patch, err := DiffUnified(ctx, string(c.before), string(c.after), rel, "edited", contextLines)
		if err != nil {
			return types.Fail[string](types.Diagnostic{Message: fmt.Sprintf("%s: no diff: %v", rel, err)}), nil
		}
		b.WriteString(patch)
	}
	return types.Ok(b.String()), nil
}

// SpliceApply is splice\apply. The error it returns is always nil: a
// failure is the Result's.
func SpliceApply(ctx context.Context, plan types.EditPlan) (types.Result[types.EditApplied], error) {
	return applyPlan(ctx, plan), nil
}

func applyPlan(ctx context.Context, plan types.EditPlan) types.Result[types.EditApplied] {
	if len(plan.Refused) > 0 {
		return types.Fail[types.EditApplied](editRefused(plan.Refused))
	}
	run := planEdits(ctx, plan.Changes, leaseGrade(ctx))
	if len(run.refused) > 0 {
		return types.Fail[types.EditApplied](editRefused(run.refused))
	}
	applied := types.EditApplied{Written: []types.EditedFile{}, Already: len(run.expansion.Already)}
	if files := run.plan.Files(); len(files) == 0 {
		// The tree holds every change, so a plan made before it did
		// is done rather than stale.
		applied.NothingToDo = true
		return types.Ok(applied)
	}
	if now := run.plan.Digest(); now != plan.Digest {
		return types.Fail[types.EditApplied](editRefused([]types.EditRefusal{{Reason: fmt.Sprintf(
			"the tree changed since the plan was made (digest %s, now %s): plan again", plan.Digest, now)}}))
	}
	if err := run.plan.Apply(); err != nil {
		return types.Fail[types.EditApplied](types.Diagnostic{Message: err.Error()})
	}
	applied.Written = run.plan.Files()
	return types.Ok(applied)
}

func editRefused(refused []types.EditRefusal) types.Diagnostic {
	lines := make([]string, len(refused))
	for i, r := range refused {
		lines[i] = edit.FormatRefusal(r)
	}
	return types.Diagnostic{
		Code:    string(types.EditPlanRefused),
		Message: strings.Join(lines, "\n"),
		URL:     types.CodeURL(types.EditPlanRefused),
		Hint:    "fix each refusal, then plan again: nothing was written",
	}
}
