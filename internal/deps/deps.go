// Package deps reads a project's declared third-party dependencies out of its
// manifest, at the versions that manifest resolves to.
//
// It PARSES rather than shelling out, and that is a deliberate split rather than a
// shortcut. The knowledge graph rebuilds implicitly and its extracted shards are
// remote-shareable on the strength of being deterministic, so an inventory step that
// needed a toolchain on PATH, a populated module cache, or the network would make a
// graph build fail for reasons that have nothing to do with the workspace. Asking the
// ecosystem's own tool (`go list -m all`, `pnpm list --json`) is the more correct
// answer to a different question - where a package sits ON DISK - and that question is
// only ever asked about a package that is already there.
//
// Go is the ecosystem this can serve from the manifest alone: go.mod require lines are
// exact versions, never ranges. An ecosystem whose manifest holds ranges (npm) needs
// its lockfile read instead, which is what spells.Manifest.LockCandidates leads to and
// what a second reader here will do.
package deps

import (
	"os"
	"path/filepath"

	"golang.org/x/mod/modfile"

	"github.com/egladman/magus/types"
)

// ManagerGo is the package-manager name recorded for Go modules. It matches the
// manager segment SCIP monikers use ("gomod"), so a package node and the symbols
// ingested for that same dependency agree on which namespace they are in.
const ManagerGo = "gomod"

// GoModule reads the require block of the go.mod at path.
//
// Replace directives are applied rather than reported alongside: a replaced module
// builds as its replacement, so recording the original requirement would describe
// something that is not on disk. A replacement pointing at a local directory has no
// version at all and is DROPPED - there is no pin to record, and a local path is not
// a third-party dependency in any sense this graph means.
//
// A missing or unparsable go.mod yields no packages and no error. Every caller is a
// best-effort graph loader for which a malformed manifest is one project's absent
// data, not a failed build.
func GoModule(path string) []types.KnowledgePackage {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	f, err := modfile.Parse(filepath.Base(path), data, nil)
	if err != nil {
		return nil
	}

	// Keyed by module path only: a replace may drop the version ("replace X => Y v2"
	// matches every version of X), so the version cannot participate in the lookup.
	type replacement struct {
		version string
		local   bool
	}
	replaced := make(map[string]replacement, len(f.Replace))
	for _, r := range f.Replace {
		if r == nil {
			continue
		}
		replaced[r.Old.Path] = replacement{
			version: r.New.Version,
			// A replacement with no version is a filesystem path - that is precisely how
			// the go.mod grammar distinguishes the two forms.
			local: r.New.Version == "",
		}
	}

	out := make([]types.KnowledgePackage, 0, len(f.Require))
	for _, r := range f.Require {
		if r == nil || r.Mod.Path == "" {
			continue
		}
		pkg := types.KnowledgePackage{
			Manager:  ManagerGo,
			Name:     r.Mod.Path,
			Version:  r.Mod.Version,
			Indirect: r.Indirect,
		}
		if rep, ok := replaced[r.Mod.Path]; ok {
			if rep.local {
				continue
			}
			pkg.Version = rep.version
			pkg.Replaced = true
		}
		if pkg.Version == "" {
			continue
		}
		out = append(out, pkg)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
