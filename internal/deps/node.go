package deps

import (
	"bufio"
	"bytes"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/types"
)

// NodePackages reads the dependencies, devDependencies and optionalDependencies of
// the package.json at manifest, at the versions lockfile pins.
//
// Lockfiles read: pnpm-lock.yaml v9, package-lock.json and npm-shrinkwrap.json v1 to
// v3, and yarn berry's yarn.lock. A yarn classic (v1) lock is not understood, nor is
// either bun lockfile: bun.lockb is binary, and the text bun.lock is not yet a lock
// candidate the typescript spell declares. Each yields no packages.
//
// peerDependencies are skipped: the project does not install them, its consumer does.
// So is every specifier that does not name a registry release (workspace:, link:,
// file:, portal:, git and URL forms, a path, a GitHub shorthand) and an npm: alias,
// whose manifest key names a package other than the one installed.
func NodePackages(manifest, lockfile string) []types.KnowledgePackage {
	declared := nodeManifest(manifest)
	if len(declared) == 0 || lockfile == "" {
		return nil
	}
	var resolve func(name, spec string) string
	switch filepath.Base(lockfile) {
	case "pnpm-lock.yaml":
		resolve = pnpmLock(manifest, lockfile)
	case "package-lock.json", "npm-shrinkwrap.json":
		resolve = npmLock(manifest, lockfile)
	case "yarn.lock":
		resolve = yarnBerryLock(lockfile)
	}
	if resolve == nil {
		return nil
	}
	var out []types.KnowledgePackage
	for _, d := range declared {
		if v := resolve(d.name, d.spec); isPinned(v) {
			out = append(out, types.KnowledgePackage{Manager: managerNode, Name: d.name, Version: v})
		}
	}
	return out
}

type nodeDependency struct{ name, spec string }

// nodeManifest returns package.json's registry dependencies sorted by name. A name
// listed in two sections is one dependency.
func nodeManifest(manifest string) []nodeDependency {
	data, err := os.ReadFile(manifest)
	if err != nil {
		return nil
	}
	var pkg struct {
		Dependencies         map[string]string `json:"dependencies"`
		DevDependencies      map[string]string `json:"devDependencies"`
		OptionalDependencies map[string]string `json:"optionalDependencies"`
	}
	if json.Unmarshal(data, &pkg) != nil {
		return nil
	}
	seen := map[string]bool{}
	var out []nodeDependency
	for _, section := range []map[string]string{pkg.Dependencies, pkg.DevDependencies, pkg.OptionalDependencies} {
		for name, spec := range section {
			if seen[name] || !isRegistrySpec(spec) {
				continue
			}
			seen[name] = true
			out = append(out, nodeDependency{name: name, spec: spec})
		}
	}
	slices.SortFunc(out, func(a, b nodeDependency) int { return strings.Compare(a.name, b.name) })
	return out
}

// isRegistrySpec reports whether a package.json specifier names a registry release.
// A semver range or dist-tag holds neither `/` nor `:`, while every path, URL, git,
// GitHub shorthand and protocol form (workspace:, link:, npm:) holds one. pnpm's
// catalog: is the exception: it names a range kept in pnpm-workspace.yaml.
func isRegistrySpec(spec string) bool {
	if strings.HasPrefix(spec, "catalog:") {
		return true
	}
	return !strings.ContainsAny(spec, "/:")
}

// lockRelDir is the manifest's directory relative to the lockfile's, in the slash form
// lockfiles key importers and nested installs by; "." when they share one.
func lockRelDir(manifest, lockfile string) (string, bool) {
	rel, err := filepath.Rel(filepath.Dir(lockfile), filepath.Dir(manifest))
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return filepath.ToSlash(rel), true
}

// pnpmLock resolves against the lockfile's importer for the manifest's directory.
// Only lockfileVersion 9 is read. A version carries its peer set in parentheses
// (`2.1.2(@bufbuild/protobuf@2.9.0)`), cut here since the node is the package, not
// one peer resolution of it.
func pnpmLock(manifest, lockfile string) func(name, spec string) string {
	data, err := os.ReadFile(lockfile)
	if err != nil {
		return nil
	}
	type dep struct {
		Version string `yaml:"version"`
	}
	type importer struct {
		Dependencies         map[string]dep `yaml:"dependencies"`
		DevDependencies      map[string]dep `yaml:"devDependencies"`
		OptionalDependencies map[string]dep `yaml:"optionalDependencies"`
	}
	var lock struct {
		LockfileVersion string              `yaml:"lockfileVersion"`
		Importers       map[string]importer `yaml:"importers"`
	}
	if yaml.Unmarshal(data, &lock) != nil || !strings.HasPrefix(lock.LockfileVersion, "9.") {
		return nil
	}
	dir, ok := lockRelDir(manifest, lockfile)
	if !ok {
		return nil
	}
	imp, ok := lock.Importers[dir]
	if !ok {
		return nil
	}
	return func(name, _ string) string {
		for _, section := range []map[string]dep{imp.Dependencies, imp.DevDependencies, imp.OptionalDependencies} {
			if d, ok := section[name]; ok {
				v, _, _ := strings.Cut(d.Version, "(")
				return v
			}
		}
		return ""
	}
}

// npmLock resolves the way node's module lookup does: the nearest node_modules/<name>
// from the manifest's directory up to the lockfile's. A lockfile with no packages map
// is v1, read from its top-level dependencies for a manifest beside it.
func npmLock(manifest, lockfile string) func(name, spec string) string {
	data, err := os.ReadFile(lockfile)
	if err != nil {
		return nil
	}
	type entry struct {
		Version  string `json:"version"`
		Link     bool   `json:"link"`
		Resolved string `json:"resolved"`
	}
	var lock struct {
		Packages     map[string]entry `json:"packages"`
		Dependencies map[string]entry `json:"dependencies"`
	}
	if json.Unmarshal(data, &lock) != nil {
		return nil
	}
	dir, ok := lockRelDir(manifest, lockfile)
	if !ok {
		return nil
	}
	// A git or file dependency records its own package.json version, so the version
	// alone would pass for a registry pin; the tarball URL is what says it is one.
	registry := func(e entry) string {
		if e.Link || (e.Resolved != "" && !strings.HasPrefix(e.Resolved, "http")) {
			return ""
		}
		return e.Version
	}
	if len(lock.Packages) == 0 {
		if dir != "." {
			return nil
		}
		return func(name, _ string) string { return registry(lock.Dependencies[name]) }
	}
	return func(name, _ string) string {
		for d := dir; ; d = path.Dir(d) {
			if e, ok := lock.Packages[path.Join(d, "node_modules", name)]; ok {
				return registry(e)
			}
			if d == "." {
				return ""
			}
		}
	}
}

// yarnBerryLock resolves against a yarn berry (v2+) lockfile, a YAML map from
// comma-joined descriptors (`"a@npm:^1.0.0, a@npm:^1.1.0"`) to a resolution. A
// declared range is matched to its descriptor; failing that, a name with exactly one
// npm resolution takes it. Workspace and patch resolutions are not npm and never match.
func yarnBerryLock(lockfile string) func(name, spec string) string {
	data, err := os.ReadFile(lockfile)
	if err != nil || isYarnClassic(data) {
		return nil
	}
	var raw map[string]yaml.Node
	if yaml.Unmarshal(data, &raw) != nil {
		return nil
	}
	if _, ok := raw["__metadata"]; !ok {
		return nil
	}
	byDescriptor := map[string]string{}
	byName := map[string][]string{}
	for key, node := range raw {
		if key == "__metadata" {
			continue
		}
		var e struct {
			Version    string `yaml:"version"`
			Resolution string `yaml:"resolution"`
		}
		if node.Decode(&e) != nil {
			continue
		}
		name, ref, ok := splitDescriptor(e.Resolution)
		if !ok || !strings.HasPrefix(ref, "npm:") {
			continue
		}
		if !slices.Contains(byName[name], e.Version) {
			byName[name] = append(byName[name], e.Version)
		}
		for _, desc := range strings.Split(key, ",") {
			byDescriptor[strings.Trim(strings.TrimSpace(desc), `"`)] = e.Version
		}
	}
	return func(name, spec string) string {
		for _, desc := range []string{name + "@npm:" + spec, name + "@" + spec} {
			if v, ok := byDescriptor[desc]; ok {
				return v
			}
		}
		if vs := byName[name]; len(vs) == 1 {
			return vs[0]
		}
		return ""
	}
}

// splitDescriptor splits `@scope/name@npm:1.2.3` at the `@` that ends the name.
func splitDescriptor(desc string) (name, ref string, ok bool) {
	i := strings.Index(strings.TrimPrefix(desc, "@"), "@")
	if i < 0 {
		return "", "", false
	}
	if strings.HasPrefix(desc, "@") {
		i++
	}
	return desc[:i], desc[i+1:], true
}

// isYarnClassic reports whether a yarn.lock is the v1 format, which is not YAML and
// names itself in its leading comment block.
func isYarnClassic(data []byte) bool {
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, "#") {
			return false
		}
		if strings.Contains(line, "yarn lockfile v1") {
			return true
		}
	}
	return false
}
