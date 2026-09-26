package project

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
)

// Discover walks root and returns a *types.Workspace. Only directories with a
// magusfile are registered; explicit spell registration in the magusfile
// is required (auto-detection via spell markers has been retired).
func Discover(_ context.Context, root string) (*types.Workspace, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("magus/project: abs %q: %w", root, err)
	}
	abs, err = filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, fmt.Errorf("magus/project: eval-symlinks %q: %w", abs, err)
	}

	if c, ok := loadWSCache(abs); ok && c.valid() {
		return restoreFromCache(abs, c), nil
	}

	ws := &types.Workspace{Root: abs, Projects: map[string]*types.Project{}}
	dirMtimes := make(map[string]int64)

	walkErr := filepath.WalkDir(abs, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}
		base := d.Name()
		if path != abs {
			if IsIgnoreDir(base) || vcs.IsSecondaryCheckout(path) {
				return fs.SkipDir
			}
		}
		if info, serr := d.Info(); serr == nil {
			dirMtimes[path] = info.ModTime().UnixNano()
		}
		rel := projectPath(abs, path)
		if hasDeclaration(path) {
			ws.Projects[rel] = &types.Project{Path: rel, Dir: path, Origin: types.OriginMagusfile}
		}
		return nil
	})
	if walkErr != nil {
		return nil, fmt.Errorf("magus/project: walk %q: %w", abs, walkErr)
	}

	saveWSCache(abs, ws, dirMtimes)

	return ws, nil
}

func projectPath(root, dir string) string {
	if dir == root {
		return "."
	}
	rel, err := filepath.Rel(root, dir)
	if err != nil {
		return dir
	}
	return filepath.ToSlash(rel)
}

// hasDeclaration reports whether dir has a declaration file or matching
// declaration-dir glob for any registered spell.
func hasDeclaration(dir string) bool {
	for _, s := range defaultRegistry.All() {
		for _, f := range s.DeclarationFiles() {
			if _, err := os.Stat(filepath.Join(dir, f)); err == nil {
				return true
			}
		}
		for _, glob := range s.DeclarationDirGlobs() {
			matches, err := filepath.Glob(filepath.Join(dir, glob))
			if err == nil && len(matches) > 0 {
				return true
			}
		}
	}
	return false
}

type extraArgsKey struct{}

// WithExtraArgs returns a context carrying extra args for spell invocations.
func WithExtraArgs(ctx context.Context, args []string) context.Context {
	return context.WithValue(ctx, extraArgsKey{}, args)
}

// ExtraArgs returns the extra args stored by WithExtraArgs, or nil.
func ExtraArgs(ctx context.Context) []string {
	v, _ := ctx.Value(extraArgsKey{}).([]string)
	return v
}

// OpNarrowing rewrites the argv of one spell op wherever a run invokes it, after the op's
// own args, charms and the call site's args are assembled and before it spawns. A sized
// gate narrows a test op's package list this way, so the target body around the op, its
// env and flags included, runs as written.
type OpNarrowing struct {
	// Op is the op's name as the spell lists it (`go-test`).
	Op string
	// Bin is the op's binary, so a same-named op of another spell is left alone.
	Bin string
	// Rewrite returns the argv to run. dir is the op's working directory and env the
	// overlay its call site passed.
	Rewrite func(ctx context.Context, dir string, env map[string]string, args []string) []string
}

type opNarrowingKey struct{}

// WithOpNarrowing returns a context whose op invocations n rewrites.
func WithOpNarrowing(ctx context.Context, n OpNarrowing) context.Context {
	return context.WithValue(ctx, opNarrowingKey{}, n)
}

// Narrowed reports whether ctx carries an op narrowing: the run executes less than its
// targets declare, so a check a target body makes over its whole suite has nothing
// whole to check.
func Narrowed(ctx context.Context) bool {
	n, ok := ctx.Value(opNarrowingKey{}).(OpNarrowing)
	return ok && n.Rewrite != nil
}

// NarrowOp returns args rewritten by the narrowing on ctx when it names op and bin, and
// args unchanged otherwise.
func NarrowOp(ctx context.Context, op, bin, dir string, env map[string]string, args []string) []string {
	n, ok := ctx.Value(opNarrowingKey{}).(OpNarrowing)
	if !ok || n.Rewrite == nil || n.Op != op || n.Bin != bin {
		return args
	}
	return n.Rewrite(ctx, dir, env, args)
}
