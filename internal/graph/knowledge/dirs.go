package knowledge

import (
	"cmp"
	"maps"
	"path"
	"slices"
	"strconv"
	"strings"

	"github.com/egladman/magus/types"
)

// dirsShardName is the singleton shard holding directory aggregate attrs.
const dirsShardName = "@dirs"

// assembleDirs folds aggregate metadata onto each directory node: how many files it
// holds transitively, the summed git churn (commit counts) across those files, and the
// set of languages present. containsChain in each path-bearing shard (buzz, docs,
// symbols) and packageDirs mint the dir nodes; this pass emits the SAME dir IDs carrying
// only these attrs, which fold onto them on merge, order-independent like @runtime.
//
// Every input is deterministic and OS-agnostic (git commit counts, extension-derived
// languages, slash-relative workspace paths), so the shard is remote-shareable. It does
// NOT read filesystem timestamps: creation/access times are not portable across
// Windows/macOS/Linux, and mtime changes on every checkout, which would make the graph
// churn and break its deterministic contract. Git history (on the file nodes) is the
// portable, stable source for "when/who changed this".
//
// leafPaths is every path-bearing node's workspace-relative path; churnByPath is the
// per-file commit count from the VCS scan (0 when a file has no recorded history).
func assembleDirs(projects []types.TargetGraphProject, leafPaths []string, churnByPath map[string]int) Shard {
	type agg struct {
		files   int
		commits int
		langs   map[string]bool
	}
	byDir := map[string]*agg{}
	for _, leaf := range leafPaths {
		owner, ok := owningProjectPath(leaf, projects)
		if !ok {
			continue
		}
		lang := languageFromPath(leaf)
		churn := churnByPath[leaf]
		for d := path.Dir(leaf); d != "." && d != "/" && d != "" && d != owner; d = path.Dir(d) {
			a := byDir[d]
			if a == nil {
				a = &agg{langs: map[string]bool{}}
				byDir[d] = a
			}
			a.files++
			a.commits += churn
			if lang != "" {
				a.langs[lang] = true
			}
		}
	}

	s := Shard{Name: dirsShardName}
	for _, d := range slices.Sorted(maps.Keys(byDir)) {
		a := byDir[d]
		attrs := map[string]string{
			AttrDirFiles:   strconv.Itoa(a.files),
			AttrDirCommits: strconv.Itoa(a.commits),
		}
		if len(a.langs) > 0 {
			attrs[AttrDirLanguages] = strings.Join(slices.Sorted(maps.Keys(a.langs)), ",")
		}
		s.Nodes = append(s.Nodes, types.KnowledgeNode{ID: dirID(d), Kind: types.KindDir, Label: d, Source: d, Attrs: attrs})
	}
	return s
}

// packageDirsShardName is the singleton shard holding a dir node for every source package
// directory, whether or not any other shard minted one.
const packageDirsShardName = "@packagedirs"

// packageLanguages maps a source extension to the language its symbol indexer reports.
// JavaScript is typescript because scip-typescript indexes it.
var packageLanguages = map[string]string{
	".go": "go",
	".ts": "typescript", ".tsx": "typescript", ".mts": "typescript", ".cts": "typescript",
	".js": "typescript", ".jsx": "typescript", ".mjs": "typescript", ".cjs": "typescript",
	".py": "python",
	".rs": "rust",
}

// packageLanguage is the indexed language of a non-test source file, or "".
func packageLanguage(p string) string {
	if IsTestPath(p) {
		return ""
	}
	return packageLanguages[strings.ToLower(path.Ext(p))]
}

// findPackageSources skips what findBuzzFiles skips except gen: a generated package is
// still one the hand-written ones import.
func findPackageSources(root string) []string { return WalkTree(root).packageSources() }

func (w *TreeWalk) packageSources() []string {
	return w.scan(walkPackages, func(f treeFile) bool { return packageLanguage(path.Base(f.rel)) != "" })
}

func assemblePackageDirs(w *TreeWalk, projects []types.TargetGraphProject, layers map[string]string) Shard {
	return packageDirs(w.packageSources(), projects, layers)
}

// packageDirs mints a dir node for every directory holding a source file and every
// directory between it and its owning project. A source dir carries AttrLanguage (the
// majority, ties to the lexically first); every dir carries its declared AttrLayer. It
// never reads the symbol index, so a machine that never ran a scip op gets the same dirs.
func packageDirs(sources []string, projects []types.TargetGraphProject, layers map[string]string) Shard {
	counts := map[string]map[string]int{}
	owner := map[string]string{}
	for _, src := range sources {
		lang := packageLanguage(src)
		p, ok := owningProjectPath(src, projects)
		if lang == "" || !ok {
			continue
		}
		d := path.Dir(src)
		if counts[d] == nil {
			counts[d] = map[string]int{}
		}
		counts[d][lang]++
		owner[d] = p
	}

	s := Shard{Name: packageDirsShardName}
	nodes := map[string]types.KnowledgeNode{}
	edges := map[[2]string]bool{}
	for d, langs := range counts {
		nodes[dirID(d)] = types.KnowledgeNode{ID: dirID(d), Kind: types.KindDir, Label: d, Source: d,
			Attrs: map[string]string{types.AttrLanguage: majorityLanguage(langs)}}
	}
	for d := range counts {
		p := owner[d]
		if d == p {
			edges[[2]string{projectID(p), dirID(d)}] = true
			continue
		}
		dn, de := containsChain(p, d, dirID(d))
		for _, n := range dn {
			if _, ok := nodes[n.ID]; !ok {
				nodes[n.ID] = n
			}
		}
		for _, e := range de {
			edges[[2]string{e.Source, e.Target}] = true
		}
	}
	for _, id := range slices.Sorted(maps.Keys(nodes)) {
		n := nodes[id]
		if layer, ok := types.ResolveLayer(layers, n.Source); ok {
			if n.Attrs == nil {
				n.Attrs = map[string]string{}
			}
			n.Attrs[types.AttrLayer] = layer
		}
		s.Nodes = append(s.Nodes, n)
	}
	for _, k := range slices.SortedFunc(maps.Keys(edges), func(a, b [2]string) int {
		return cmp.Or(cmp.Compare(a[0], b[0]), cmp.Compare(a[1], b[1]))
	}) {
		s.Edges = append(s.Edges, extractedEdge(k[0], k[1], types.RelationContains, strings.TrimPrefix(k[1], types.KindDir+":")))
	}
	return s
}

func majorityLanguage(counts map[string]int) string {
	best := ""
	for _, lang := range slices.Sorted(maps.Keys(counts)) {
		if best == "" || counts[lang] > counts[best] {
			best = lang
		}
	}
	return best
}

// unionLayers joins every project's "layers" into the one map types.ResolveLayer reads. A
// directory sits in one layer, so one declared under two names, in any projects, is
// LayerDeclarationInvalid.
func unionLayers(byProject map[string]map[string]string) (map[string]string, error) {
	out := map[string]string{}
	declaredBy := map[string]string{}
	for _, p := range slices.Sorted(maps.Keys(byProject)) {
		for _, dir := range slices.Sorted(maps.Keys(byProject[p])) {
			name := byProject[p][dir]
			if prev, ok := out[dir]; ok && prev != name {
				return nil, types.DiagnosticErrorf(types.LayerDeclarationInvalid,
					`magus.project: "layers"[%q]: declared as %q by project %s and as %q by project %s; a directory sits in one layer`,
					dir, prev, declaredBy[dir], name, p)
			}
			out[dir], declaredBy[dir] = name, p
		}
	}
	return out, nil
}

// languageFromPath maps a file path to a language token by its extension. It is the
// deterministic, OS-agnostic classifier the directory aggregate uses; the canonical
// names line up with the "language" attr the buzz and symbol shards set on file nodes
// (though the dir attr is a SET under its own dir_languages key, not that single value). An
// unrecognized extension falls back to the bare extension so nothing is silently lost;
// an extensionless path yields "".
func languageFromPath(p string) string {
	ext := strings.ToLower(strings.TrimPrefix(path.Ext(p), "."))
	switch ext {
	case "go":
		return "go"
	case "buzz":
		return "buzz"
	case "ts", "tsx":
		return "typescript"
	case "js", "jsx", "mjs", "cjs":
		return "javascript"
	case "rs":
		return "rust"
	case "py":
		return "python"
	case "md", "markdown":
		return "markdown"
	default:
		return ext
	}
}
