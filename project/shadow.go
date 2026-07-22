package project

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/egladman/magus/vcs"
)

// ShadowConflict is one spell import defined at two levels in ancestor-descendant
// relation, so root-wins resolution (see the spell import walk) makes the deeper
// definition dead. It is the footgun the shadow ward guards: an author placed a
// spell next to a nested project expecting it to be used, but a spell of the same
// import path higher up silently wins.
type ShadowConflict struct {
	Import   string // the import path an author writes, e.g. "spells/hello"
	Winner   string // absolute path to the canonical (root-most) spell source
	Shadowed string // absolute path to the dead (deeper) spell source
}

// SpellShadows scans the workspace rooted at root and returns every spell-import
// shadow: a "spells/<name>" defined at both an ancestor directory and a descendant
// directory. Sibling subtrees that reuse a name (web/spells/x and api/spells/x) are
// NOT shadows, since no project's root-to-leaf path sees both; only an
// ancestor-descendant pair is. Results are sorted by (Import, Shadowed) for a
// stable diagnostic. The scan skips the usual ignore dirs (.git, node_modules, ...).
func SpellShadows(root string) ([]ShadowConflict, error) {
	// importPath -> the directories ("levels") that define it. A level is the dir
	// CONTAINING the spells/ dir, so ancestry between levels mirrors project nesting.
	levels := map[string][]string{}

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // unreadable entry: skip, do not fail the whole scan
		}
		if d.IsDir() {
			if IsIgnoreDir(d.Name()) || vcs.IsSecondaryCheckout(path) {
				return filepath.SkipDir
			}
			if d.Name() == "spells" {
				level := filepath.Dir(path)
				for _, imp := range spellImportsIn(path) {
					levels[imp] = append(levels[imp], level)
				}
				return filepath.SkipDir // a spells/ dir holds spells, not nested projects to rescan
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	var out []ShadowConflict
	for imp, dirs := range levels {
		if len(dirs) < 2 {
			continue
		}
		// Root-most level wins for the whole workspace, so any deeper level that has
		// an ancestor among the defining levels is shadowed by the root-most ancestor.
		sort.Strings(dirs) // ancestors sort before their descendants (path prefix)
		for _, d := range dirs {
			if anc, ok := rootMostAncestor(d, dirs); ok {
				out = append(out, ShadowConflict{
					Import:   imp,
					Winner:   spellSource(anc, imp),
					Shadowed: spellSource(d, imp),
				})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Import != out[j].Import {
			return out[i].Import < out[j].Import
		}
		return out[i].Shadowed < out[j].Shadowed
	})
	return out, nil
}

// spellImportsIn returns the import paths a spells/ directory defines: "spells/<name>"
// for each <name>/spell.buzz subdir and each flat <name>.buzz file.
func spellImportsIn(spellsDir string) []string {
	entries, err := os.ReadDir(spellsDir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			if _, err := os.Stat(filepath.Join(spellsDir, e.Name(), "spell.buzz")); err == nil {
				out = append(out, "spells/"+e.Name())
			}
			continue
		}
		if name, ok := strings.CutSuffix(e.Name(), ".buzz"); ok {
			out = append(out, "spells/"+name)
		}
	}
	return out
}

// spellSource returns the on-disk path of import imp defined at level, preferring
// the directory form (spells/<name>/spell.buzz) and falling back to the flat file.
func spellSource(level, imp string) string {
	dirForm := filepath.Join(level, filepath.FromSlash(imp), "spell.buzz")
	if _, err := os.Stat(dirForm); err == nil {
		return dirForm
	}
	return filepath.Join(level, filepath.FromSlash(imp)+".buzz")
}

// rootMostAncestor returns the root-most directory in levels that is a strict path
// ancestor of dir, or ok=false when dir has no ancestor among them (it is a
// top-most definition, the winner rather than a shadow).
func rootMostAncestor(dir string, levels []string) (string, bool) {
	best, found := "", false
	for _, l := range levels {
		if l == dir {
			continue
		}
		if strings.HasPrefix(dir, l+string(filepath.Separator)) {
			if !found || len(l) < len(best) {
				best, found = l, true
			}
		}
	}
	return best, found
}
