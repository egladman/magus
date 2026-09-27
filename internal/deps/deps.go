// Package deps reads a project's declared third-party dependencies out of its
// manifest, at the versions that manifest resolves to.
//
// It PARSES rather than shelling out, and that is a deliberate split rather than a
// shortcut. The knowledge graph rebuilds implicitly and its extracted shards are
// remote-shareable on the strength of being deterministic, so an inventory step that
// needed a toolchain on PATH, a populated module cache, or the network would make a
// graph build fail for reasons that have nothing to do with the workspace. Asking the
// ecosystem's own tool (`go list -m all`, `pnpm list --json`) is the more correct
// answer to a different question (where a package sits ON DISK), and that question is
// only ever asked about a package that is already there.
//
// Every reader shares one contract: direct dependencies only, never the network, and
// best-effort, so an unreadable or unparsable file contributes nothing and fails
// nothing. go.mod pins exact versions, so it is read alone. Every other manifest holds
// ranges, so its reader takes the lockfile spells.Manifest.LockCandidates resolved and
// reports the version the LOCK pins for the names the MANIFEST declares.
package deps

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"golang.org/x/mod/modfile"

	"github.com/egladman/magus/types"
)

// The package-manager names recorded on package nodes. Each must equal the manager
// segment its ecosystem's SCIP indexer writes into monikers, so a package node and the
// symbols ingested for that same dependency agree on which namespace they are in.
const (
	// managerGo matches scip-go, read off this repository's own indexes.
	managerGo = "gomod"
	// managerNode matches scip-typescript, read off this repository's own indexes.
	managerNode = "npm"
	// managerPython matches scip-python: ScipSymbol.package in
	// sourcegraph/scip-python packages/pyright-scip/src/ScipSymbol.ts builds
	// `scip-python python ${name} ${version} `. Read from that source on 2026-09-27;
	// this repository has no Python index to check it against.
	managerPython = "python"
	// managerCargo matches rust-analyzer: crates/rust-analyzer/src/cli/scip.rs builds
	// scip_types::Package{manager: "cargo".to_owned(), ...}. Read from that source on
	// 2026-09-27; this repository has no Rust index to check it against.
	managerCargo = "cargo"
)

// Reader is how one kind of manifest becomes packages.
type Reader struct {
	// Manager is the package-manager name on every package Read returns.
	Manager string
	// Locks names the lockfile basenames Read understands. Empty means the manifest
	// pins exact versions itself and Read ignores its lockfile argument.
	Locks []string
	// Read takes the manifest's path and its resolved lockfile's path ("" when none
	// was found) and returns the direct dependencies, or nil.
	Read func(manifest, lockfile string) []types.KnowledgePackage
}

// Readers holds a Reader for every manifest a shipped spell declares, keyed by the
// manifest's basename (spells.Manifest.Value). TestEverySpellManifestHasAReader fails
// when a spell declares a manifest or lock candidate that is neither read here nor
// allowlisted with a reason.
var Readers = map[string]Reader{
	"go.mod": {Manager: managerGo, Read: func(manifest, _ string) []types.KnowledgePackage {
		return GoModule(manifest)
	}},
	"package.json": {
		Manager: managerNode,
		Locks:   []string{"pnpm-lock.yaml", "package-lock.json", "npm-shrinkwrap.json", "yarn.lock"},
		Read:    NodePackages,
	},
	"pyproject.toml": {
		Manager: managerPython,
		Locks:   []string{"uv.lock", "poetry.lock", "pdm.lock", "Pipfile.lock"},
		Read:    PythonPackages,
	},
	"Cargo.toml": {Manager: managerCargo, Locks: []string{"Cargo.lock"}, Read: CargoPackages},
}

// UnderstandsLock reports whether r reads the lockfile at path, judged by basename.
func (r Reader) UnderstandsLock(path string) bool {
	return slices.Contains(r.Locks, filepath.Base(path))
}

// GoModule reads the require block of the go.mod at path.
//
// Replace directives are applied rather than reported alongside: a replaced module
// builds as its replacement, so recording the original requirement would describe
// something that is not on disk. A replacement pointing at a local directory has no
// version at all and is DROPPED: there is no pin to record, and a local path is not
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
			// A replacement with no version is a filesystem path; that is precisely how
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
			Manager:  managerGo,
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

// pep503Separators is the run PEP 503 collapses to one hyphen.
var pep503Separators = regexp.MustCompile(`[-_.]+`)

// normalizePython is the PEP 503 form of a Python distribution name, the form PyPI
// keys a project by, so `Pillow`, `pillow` and `PILLOW` are one project.
func normalizePython(name string) string {
	return strings.ToLower(pep503Separators.ReplaceAllString(name, "-"))
}

// isPinned reports whether a lockfile's version is a registry release. A lock records
// a link, file or git dependency's location in the version slot; none of those starts
// with a digit, and every version npm, PyPI and crates.io publish does.
func isPinned(version string) bool {
	return version != "" && version[0] >= '0' && version[0] <= '9'
}
