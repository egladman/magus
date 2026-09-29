package doctor

import (
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/spell"
	remotespell "github.com/egladman/magus/internal/spell/remote"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
)

// checkSpellOverrides compares each workspace copy that replaces a built-in with the
// built-in this binary ships, through the origin stamp `magus spell pull` writes.
//
// Advice at most, and never a Fix: the copy is the workspace's to edit, and whether to
// take a change the built-in made since is judgment. A copy that does not load is
// already MGS1044 when the workspace loads.
func (r *runner) checkSpellOverrides() types.Check {
	const name = "spell-overrides"
	// r.ws.Root() is the resolved workspace root; r.root can be empty on this path.
	details, advice := spellOverrideFindings(r.ws.Root(), r.opts.cfg.Spells)
	switch {
	case len(details) == 0:
		return types.Check{Name: name, Status: types.CheckOK, Message: "no workspace copy replaces a built-in spell"}
	case advice == 0:
		return types.Check{Name: name, Status: types.CheckOK, Message: fmt.Sprintf("%d built-in spell(s) replaced by a workspace copy", len(details)), Details: details}
	}
	return types.Check{
		Name: name, Status: types.CheckAdvice, Details: details,
		Message: fmt.Sprintf("%d of %d workspace copies of built-in spells differ from what their stamp says", advice, len(details)),
	}
}

// spellOverrideFindings reads one line per declared override of a built-in, and how
// many of them are advice.
func spellOverrideFindings(root string, cfg config.SpellsConfig) (details []string, advice int) {
	for _, importPath := range slices.Sorted(maps.Keys(cfg.Imports)) {
		decl := cfg.Imports[importPath]
		builtin, ok := strings.CutPrefix(importPath, spells.ModulePrefix)
		if !ok || decl.Path == "" {
			continue
		}
		dir, ok := spell.ShippedDir(builtin)
		if !ok {
			continue // MGS1044 when the workspace loads
		}
		line, isAdvice := describeFork(filepath.Join(root, filepath.FromSlash(decl.Path)), decl.Path, importPath, builtin, dir)
		details = append(details, line)
		if isAdvice {
			advice++
		}
	}
	return details, advice
}

func describeFork(forkDir, rel, importPath, name, dir string) (string, bool) {
	f, err := remotespell.ReadFork(forkDir, name, dir)
	switch {
	case err != nil:
		return fmt.Sprintf("%s replaces %s but could not be compared: %v", rel, importPath, err), true
	case f.Origin.Digest == "":
		return fmt.Sprintf("%s replaces %s with no origin stamp, so what it started from is unknown; `magus spell pull %s <empty dir>` writes the current copy to diff against",
			rel, importPath, importPath), true
	case f.Origin.Digest != f.Shipped.Digest:
		return fmt.Sprintf("%s changed since %s was pulled (%s -> %s); `magus spell pull %s <empty dir>` writes the current copy to diff against",
			importPath, rel, shortDigest(f.Origin.Digest.String()), shortDigest(f.Shipped.Digest.String()), importPath), true
	case !f.Edited:
		return fmt.Sprintf("%s is identical to the built-in %s; drop the override", rel, importPath), true
	}
	return fmt.Sprintf("%s replaces %s (pulled from this binary's copy)", rel, importPath), false
}

// shortDigest keeps a digest's algorithm and first 12 hex characters.
func shortDigest(d string) string {
	if algo, hex, ok := strings.Cut(d, ":"); ok && len(hex) > 12 {
		return algo + ":" + hex[:12]
	}
	return d
}
