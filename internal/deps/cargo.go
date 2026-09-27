package deps

import (
	"cmp"
	"os"
	"slices"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"github.com/egladman/magus/types"
)

// cargoSections are the dependency tables Cargo.toml holds at top level and again
// under every [target.<cfg>].
var cargoSections = []string{"dependencies", "dev-dependencies", "build-dependencies"}

// CargoPackages reads the dependencies the Cargo.toml at manifest declares, at the
// versions the Cargo.lock at lockfile pins.
//
// Names come from [dependencies], [dev-dependencies] and [build-dependencies], the same
// under every [target.<cfg>], and [workspace.dependencies] for a virtual workspace root
// (one with [workspace] and no [package]). A `package = "..."` rename reports the real
// crate. A `workspace = true` entry is named by its key, so a rename declared only in
// the workspace root is not seen. A path or git dependency is skipped.
//
// A lock entry with no source is a path crate and skipped. Two entries with one name
// (two majors in the closure) are both returned. A crate named under
// [patch.crates-io] is Replaced: its version is the patch's, which is what builds. A
// patch to a local path leaves the lock entry sourceless, so that crate is dropped, as
// GoModule drops a local replace.
func CargoPackages(manifest, lockfile string) []types.KnowledgePackage {
	declared, patched := cargoManifest(manifest)
	if len(declared) == 0 || lockfile == "" {
		return nil
	}
	data, err := os.ReadFile(lockfile)
	if err != nil {
		return nil
	}
	var lock struct {
		Package []struct {
			Name    string `toml:"name"`
			Version string `toml:"version"`
			Source  string `toml:"source"`
		} `toml:"package"`
	}
	if toml.Unmarshal(data, &lock) != nil {
		return nil
	}
	var out []types.KnowledgePackage
	for _, p := range lock.Package {
		if p.Source == "" || !declared[p.Name] || !isPinned(p.Version) {
			continue
		}
		out = append(out, types.KnowledgePackage{
			Manager:  managerCargo,
			Name:     p.Name,
			Version:  p.Version,
			Replaced: patched[p.Name],
		})
	}
	slices.SortFunc(out, func(a, b types.KnowledgePackage) int {
		return cmp.Or(strings.Compare(a.Name, b.Name), strings.Compare(a.Version, b.Version))
	})
	return out
}

// cargoManifest returns the registry crates Cargo.toml declares and the crates its
// [patch.crates-io] redirects, both by real crate name.
func cargoManifest(manifest string) (declared, patched map[string]bool) {
	data, err := os.ReadFile(manifest)
	if err != nil {
		return nil, nil
	}
	var m map[string]any
	if toml.Unmarshal(data, &m) != nil {
		return nil, nil
	}
	declared = map[string]bool{}
	add := func(table any) {
		deps, _ := table.(map[string]any)
		for key, spec := range deps {
			if name, ok := cargoCrate(key, spec); ok {
				declared[name] = true
			}
		}
	}
	for _, s := range cargoSections {
		add(m[s])
	}
	targets, _ := m["target"].(map[string]any)
	for _, t := range targets {
		tt, _ := t.(map[string]any)
		for _, s := range cargoSections {
			add(tt[s])
		}
	}
	if ws, ok := m["workspace"].(map[string]any); ok && m["package"] == nil {
		add(ws["dependencies"])
	}

	patched = map[string]bool{}
	patch, _ := m["patch"].(map[string]any)
	cratesIO, _ := patch["crates-io"].(map[string]any)
	for key, spec := range cratesIO {
		name := key
		if t, ok := spec.(map[string]any); ok {
			if real, ok := t["package"].(string); ok && real != "" {
				name = real
			}
		}
		patched[name] = true
	}
	return declared, patched
}

// cargoCrate resolves one dependency entry to its real crate name, or false for a path
// or git dependency.
func cargoCrate(key string, spec any) (string, bool) {
	t, ok := spec.(map[string]any)
	if !ok {
		return key, true
	}
	if hasAny(t, "path", "git") {
		return "", false
	}
	if real, ok := t["package"].(string); ok && real != "" {
		return real, true
	}
	return key, true
}
