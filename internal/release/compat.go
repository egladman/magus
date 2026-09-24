package release

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/interp"
	"github.com/egladman/magus/internal/interp/bindings"
	"github.com/egladman/magus/libs/diagnostics"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"

	// Without the engine no magusfile is evaluated, and a tree that loads nothing
	// reports zero breakage.
	_ "github.com/egladman/magus/internal/interp/engine/buzz"
)

// Bump is the size of a release relative to the previous one.
type Bump int

const (
	BumpPatch Bump = iota
	BumpMinor
	BumpMajor
)

func (b Bump) String() string {
	switch b {
	case BumpPatch:
		return "patch"
	case BumpMinor:
		return "minor"
	case BumpMajor:
		return "major"
	}
	return fmt.Sprintf("Bump(%d)", int(b))
}

// CompatReport is what loading the previous release's tree with this build found.
// Codes holds one entry per failure, sorted, so a code repeats once per file it
// broke. Uncoded holds the failures that carry no diagnostic code.
type CompatReport struct {
	Base    string
	Codes   []diagnostics.Code
	Uncoded []string
}

// CheckCompat loads the workspace as it was at baseTag with this build and reports
// every load failure. The error return is for failing to run the check at all; a
// tree that does not load is a finding, not an error.
//
// The tree is the revision exported through the VCS layer, so it carries no VCS
// metadata, and workspace providers are skipped because an exported revision has no
// installed toolchain for them to run.
func CheckCompat(ctx context.Context, repoRoot, baseTag string) (CompatReport, error) {
	if !interp.Available() {
		return CompatReport{}, errors.New("release: compat check needs the Buzz interpreter linked")
	}
	scratch, err := os.MkdirTemp("", "magus-compat-")
	if err != nil {
		return CompatReport{}, fmt.Errorf("release: %w", err)
	}
	defer os.RemoveAll(scratch)
	tree := filepath.Join(scratch, "tree")
	if err := exportTo(ctx, repoRoot, baseTag, tree); err != nil {
		return CompatReport{}, err
	}

	report := CompatReport{Base: baseTag}
	if err := checkSpells(ctx, tree, &report); err != nil {
		return CompatReport{}, err
	}
	cfg, err := config.LoadFile(filepath.Join(tree, "magus.yaml"), false)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		report.add(err)
		return report.sorted(), nil
	}
	// A private cache keeps the old tree from reading or seeding the live one, and
	// the shared daemon would load it with its own build instead of this one.
	cfg.Cache.Dir = filepath.Join(scratch, "cache")
	cfg.Daemon.Enabled = false
	ws, err := magus.Inspect(ctx, tree, magus.WithLoadedConfig(cfg), magus.WithoutWorkspaceProviders())
	if c, ok := ws.(io.Closer); ok {
		_ = c.Close()
	}
	report.add(err)
	return report.sorted(), nil
}

// checkSpells compiles every spells/**/spell.buzz in tree. The workspace load
// logs a broken spell and skips it, so without this a spell only counts when a
// magusfile's import of it fails, and then under the importer's code.
func checkSpells(ctx context.Context, tree string, report *CompatReport) error {
	return filepath.WalkDir(tree, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || d.Name() != "spell.buzz" {
			return err
		}
		rel, err := filepath.Rel(tree, path)
		if err != nil {
			return err
		}
		if slices.Contains(strings.Split(filepath.ToSlash(rel), "/"), "spells") {
			report.add(bindings.CheckSpellFile(ctx, path))
		}
		return nil
	})
}

// Judge refuses a bump the report does not allow. A patch must load cleanly. A minor
// may break only in ways announced by code. A major is never refused.
func (r CompatReport) Judge(b Bump, announced []diagnostics.Code) error {
	var bad []string
	switch b {
	case BumpMajor:
		return nil
	case BumpPatch:
		bad = countCodes(r.Codes)
	case BumpMinor:
		bad = countCodes(slices.DeleteFunc(slices.Clone(r.Codes), func(c diagnostics.Code) bool {
			return slices.Contains(announced, c)
		}))
	default:
		return fmt.Errorf("release: unknown bump %v", b)
	}
	// An uncoded failure cannot be announced, so no minor covers it.
	for _, u := range r.Uncoded {
		bad = append(bad, "uncoded: "+u)
	}
	if len(bad) == 0 {
		return nil
	}
	return fmt.Errorf("release: a %s release must load %s, but it fails:\n  %s",
		b, r.Base, strings.Join(bad, "\n  "))
}

// countCodes renders codes as "CODE xN", one line per distinct code in code order.
func countCodes(codes []diagnostics.Code) []string {
	codes = slices.Sorted(slices.Values(codes))
	var out []string
	for i := 0; i < len(codes); {
		j := i
		for j < len(codes) && codes[j] == codes[i] {
			j++
		}
		out = append(out, fmt.Sprintf("%s x%d", codes[i], j-i))
		i = j
	}
	return out
}

func (r CompatReport) sorted() CompatReport {
	slices.Sort(r.Codes)
	slices.Sort(r.Uncoded)
	return r
}

// add records the innermost coded error on each branch of err, since that is the
// root cause a breaking entry names. A wrapper such as the stale binary note would
// otherwise hide it.
func (r *CompatReport) add(err error) {
	codes, uncoded := classify(err)
	r.Codes = append(r.Codes, codes...)
	r.Uncoded = append(r.Uncoded, uncoded...)
}

func classify(err error) (codes []diagnostics.Code, uncoded []string) {
	if err == nil {
		return nil, nil
	}
	if multi, ok := err.(interface{ Unwrap() []error }); ok {
		for _, e := range multi.Unwrap() {
			c, u := classify(e)
			codes = append(codes, c...)
			uncoded = append(uncoded, u...)
		}
		return codes, uncoded
	}
	if d, ok := err.(*diagnostics.Error); ok { //nolint:errorlint // one node at a time; As would skip to the outermost code
		if c, u := classify(d.Unwrap()); len(c) > 0 {
			return c, u
		}
		return []diagnostics.Code{d.Code}, nil
	}
	if inner := errors.Unwrap(err); inner != nil {
		c, u := classify(inner)
		if len(c) == 0 && len(u) == 1 {
			// Keep the outer text: it names the file the leaf is about.
			return nil, []string{err.Error()}
		}
		return c, u
	}
	// A leaf may still carry a code through an As method, as the Buzz checker does.
	var d *diagnostics.Error
	if errors.As(err, &d) {
		return []diagnostics.Code{d.Code}, nil
	}
	return nil, []string{err.Error()}
}

// exportTo writes rev of the repository at repoRoot into dir through the backend that
// claims it.
func exportTo(ctx context.Context, repoRoot, rev, dir string) error {
	res, err := vcs.Resolve(ctx, repoRoot, "", types.VCSOptions{})
	if err != nil {
		return fmt.Errorf("release: %w", err)
	}
	if res.VCS == nil {
		return errors.New("release: version control is disabled, so there is no revision to export")
	}
	if err := res.VCS.ExportRevision(ctx, repoRoot, rev, dir); err != nil {
		return fmt.Errorf("release: export %s: %w", rev, err)
	}
	return nil
}
