package deps

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/types"
)

// PythonPackages reads the dependencies the pyproject.toml at manifest declares, at
// the versions lockfile pins, under their PEP 503 normalized names.
//
// Names come from [project].dependencies and [project.optional-dependencies] (PEP
// 621), [dependency-groups] (PEP 735), and the [tool.poetry] dependencies,
// dev-dependencies and group tables a pre-PEP 621 poetry project keeps instead.
// Lockfiles read: uv.lock, poetry.lock, pdm.lock and Pipfile.lock. Each lists the whole
// closure, so an entry is kept only when the manifest names it. A direct reference
// (`name @ url`), a poetry path, git or url table, and a lock entry from a directory,
// path, git or url source are skipped: none is a registry release.
//
// A lock may pin two versions of one name when its resolution forks on a marker;
// both are returned.
func PythonPackages(manifest, lockfile string) []types.KnowledgePackage {
	declared := pythonManifest(manifest)
	if len(declared) == 0 || lockfile == "" {
		return nil
	}
	var locked map[string][]string
	switch filepath.Base(lockfile) {
	case "uv.lock", "poetry.lock", "pdm.lock":
		locked = pythonTOMLLock(lockfile)
	case "Pipfile.lock":
		locked = pipfileLock(lockfile)
	}
	var out []types.KnowledgePackage
	for _, name := range declared {
		for _, v := range locked[name] {
			out = append(out, types.KnowledgePackage{Manager: managerPython, Name: name, Version: v})
		}
	}
	return out
}

// pep508Name is the distribution name a PEP 508 requirement string starts with.
var pep508Name = regexp.MustCompile(`^\s*([A-Za-z0-9](?:[A-Za-z0-9._-]*[A-Za-z0-9])?)\s*(\[[^\]]*\])?\s*(@)?`)

// requirementName is a PEP 508 requirement's normalized name, or "" for a direct
// reference, whose version no index resolves.
func requirementName(req string) string {
	m := pep508Name.FindStringSubmatch(req)
	if len(m) < 4 || m[3] != "" {
		return ""
	}
	return normalizePython(m[1])
}

// pythonManifest returns the normalized names pyproject.toml declares, sorted.
func pythonManifest(manifest string) []string {
	data, err := os.ReadFile(manifest)
	if err != nil {
		return nil
	}
	type poetryDeps struct {
		Dependencies map[string]any `toml:"dependencies"`
	}
	var py struct {
		Project struct {
			Dependencies         []string            `toml:"dependencies"`
			OptionalDependencies map[string][]string `toml:"optional-dependencies"`
		} `toml:"project"`
		DependencyGroups map[string][]any `toml:"dependency-groups"`
		Tool             struct {
			Poetry struct {
				Dependencies    map[string]any        `toml:"dependencies"`
				DevDependencies map[string]any        `toml:"dev-dependencies"`
				Group           map[string]poetryDeps `toml:"group"`
			} `toml:"poetry"`
		} `toml:"tool"`
	}
	if toml.Unmarshal(data, &py) != nil {
		return nil
	}
	names := map[string]bool{}
	addRequirement := func(req string) {
		if n := requirementName(req); n != "" {
			names[n] = true
		}
	}
	for _, r := range py.Project.Dependencies {
		addRequirement(r)
	}
	for _, reqs := range py.Project.OptionalDependencies {
		for _, r := range reqs {
			addRequirement(r)
		}
	}
	for _, reqs := range py.DependencyGroups {
		for _, r := range reqs {
			// A table is an {include-group = "..."}, which names a group, not a package.
			if s, ok := r.(string); ok {
				addRequirement(s)
			}
		}
	}
	addPoetry := func(deps map[string]any) {
		for name, spec := range deps {
			if strings.EqualFold(name, "python") || !poetryRegistrySpec(spec) {
				continue
			}
			names[normalizePython(name)] = true
		}
	}
	addPoetry(py.Tool.Poetry.Dependencies)
	addPoetry(py.Tool.Poetry.DevDependencies)
	for _, g := range py.Tool.Poetry.Group {
		addPoetry(g.Dependencies)
	}
	out := make([]string, 0, len(names))
	for n := range names {
		out = append(out, n)
	}
	slices.Sort(out)
	return out
}

// poetryRegistrySpec reports whether a poetry dependency resolves from an index. A
// string is a version constraint and a list is several; a table is a registry
// dependency unless it names a path, git repository or url.
func poetryRegistrySpec(spec any) bool {
	t, ok := spec.(map[string]any)
	if !ok {
		return true
	}
	for _, k := range []string{"path", "git", "url"} {
		if _, ok := t[k]; ok {
			return false
		}
	}
	return true
}

// pythonTOMLLock reads the [[package]] array uv.lock, poetry.lock and pdm.lock share,
// keyed by normalized name. uv marks a registry release with source.registry; poetry
// marks anything else with a source.type; pdm marks it with a path, git or url key.
func pythonTOMLLock(lockfile string) map[string][]string {
	data, err := os.ReadFile(lockfile)
	if err != nil {
		return nil
	}
	uv := filepath.Base(lockfile) == "uv.lock"
	var lock struct {
		Package []map[string]any `toml:"package"`
	}
	if toml.Unmarshal(data, &lock) != nil {
		return nil
	}
	out := map[string][]string{}
	for _, p := range lock.Package {
		name, _ := p["name"].(string)
		version, _ := p["version"].(string)
		if name == "" || !isPinned(version) {
			continue
		}
		source, _ := p["source"].(map[string]any)
		switch {
		case uv:
			if _, ok := source["registry"]; !ok {
				continue
			}
		case source != nil:
			if t, _ := source["type"].(string); t == "directory" || t == "git" || t == "url" || t == "file" {
				continue
			}
		}
		if hasAny(p, "path", "git", "url") {
			continue
		}
		n := normalizePython(name)
		if !slices.Contains(out[n], version) {
			out[n] = append(out[n], version)
		}
	}
	for _, vs := range out {
		slices.Sort(vs)
	}
	return out
}

// pipfileLock reads Pipfile.lock's default and develop maps, whose versions are
// `==`-pinned. An entry with a path, git or file member is not from an index.
func pipfileLock(lockfile string) map[string][]string {
	data, err := os.ReadFile(lockfile)
	if err != nil {
		return nil
	}
	var lock struct {
		Default map[string]map[string]any `json:"default"`
		Develop map[string]map[string]any `json:"develop"`
	}
	if json.Unmarshal(data, &lock) != nil {
		return nil
	}
	out := map[string][]string{}
	for _, section := range []map[string]map[string]any{lock.Default, lock.Develop} {
		for name, e := range section {
			if hasAny(e, "path", "git", "file") {
				continue
			}
			v, _ := e["version"].(string)
			v = strings.TrimPrefix(v, "==")
			if !isPinned(v) {
				continue
			}
			n := normalizePython(name)
			if !slices.Contains(out[n], v) {
				out[n] = append(out[n], v)
			}
		}
	}
	for _, vs := range out {
		slices.Sort(vs)
	}
	return out
}

func hasAny(m map[string]any, keys ...string) bool {
	for _, k := range keys {
		if _, ok := m[k]; ok {
			return true
		}
	}
	return false
}
