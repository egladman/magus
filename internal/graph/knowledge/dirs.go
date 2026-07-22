package knowledge

import (
	"maps"
	"path"
	"slices"
	"strconv"
	"strings"

	"github.com/egladman/magus/types"
)

// DirsShardName is the singleton shard holding directory aggregate attrs.
const DirsShardName = "@dirs"

// assembleDirs folds aggregate metadata onto each directory node: how many files it
// holds transitively, the summed git churn (commit counts) across those files, and the
// set of languages present. The directory nodes themselves are minted structurally by
// containsChain in each path-bearing shard (buzz, docs, symbols); this pass emits the
// SAME dir IDs carrying only these attrs, which fold onto the structural nodes on merge
// - the typed-partial-node pattern, order-independent like the @runtime shard.
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

	s := Shard{Name: DirsShardName}
	for _, d := range slices.Sorted(maps.Keys(byDir)) {
		a := byDir[d]
		attrs := map[string]string{
			AttrDirFiles:   strconv.Itoa(a.files),
			AttrDirCommits: strconv.Itoa(a.commits),
		}
		if len(a.langs) > 0 {
			attrs[AttrLanguages] = strings.Join(slices.Sorted(maps.Keys(a.langs)), ",")
		}
		s.Nodes = append(s.Nodes, types.KnowledgeNode{ID: dirID(d), Kind: types.KindDir, Label: d, Source: d, Attrs: attrs})
	}
	return s
}

// languageFromPath maps a file path to a language token by its extension. It is the
// deterministic, OS-agnostic classifier the directory aggregate uses; the canonical
// names match the "language" attr the buzz and symbol shards set on file nodes. An
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
