package guard

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/types"
)

// A listing (`ls <dir>`, `ls -R`, `git ls-files <dir|glob>`) is answered by the graph's file
// and dir nodes when what it would print is, entry for entry, what the graph holds. The
// proof reads the same directory the listing would, within the hook's budget. A metadata
// flag, an untracked-files question, a file the graph does not index, or a walk past the
// budget keeps the rule silent: the graph would answer a different question.

// lsMetadataFlags are ls flags whose output is about files rather than which files exist:
// sizes, times, permissions, ownership, inodes and dotfiles.
const lsMetadataFlags = "lgontuchsSiaAdfF@eT"

// lsCall is one ls invocation reduced to what decides its answer.
type lsCall struct {
	dirs      []string
	recursive bool
}

// parseLsCall reads c as a listing of directories, reporting false for a flag that asks
// about the files themselves or for a long option.
func parseLsCall(c hint.Invocation) (lsCall, bool) {
	var lc lsCall
	for _, a := range c.Args {
		switch {
		case a == "--":
			continue
		case strings.HasPrefix(a, "--"):
			return lsCall{}, false
		case len(a) > 1 && a[0] == '-':
			for _, f := range a[1:] {
				switch {
				case f == 'R':
					lc.recursive = true
				case f == '1' || f == 'p' || f == 'C' || f == 'x' || f == 'm':
				case strings.ContainsRune(lsMetadataFlags, f):
					return lsCall{}, false
				default:
					return lsCall{}, false
				}
			}
		default:
			lc.dirs = append(lc.dirs, a)
		}
	}
	if len(lc.dirs) == 0 {
		lc.dirs = []string{"."}
	}
	return lc, true
}

// translateLs answers `ls <dir>` with the query selecting the dir's file and dir nodes, and
// `ls -R <dir>` with the file nodes under it, when the proof holds.
func translateLs(deps Dependencies, dir string, c hint.Invocation) (translation, bool) {
	lc, ok := parseLsCall(c)
	if !ok || deps.scope.root == "" || allOutside(deps.scope, lc.dirs) {
		return translation{}, false
	}
	root, ok := resolvedRoot(deps.scope.root)
	if !ok {
		return translation{}, false
	}
	rels := make([]string, 0, len(lc.dirs))
	for _, d := range lc.dirs {
		abs, rel, ok := workspacePath(root, dir, d)
		if !ok {
			return translation{}, false
		}
		info, err := os.Stat(abs)
		if err != nil || !info.IsDir() {
			return translation{}, false
		}
		for _, seg := range strings.Split(rel, "/") {
			if nonCodeDirs[seg] {
				return translation{}, false
			}
		}
		rels = append(rels, rel)
	}
	if lc.recursive {
		return treeListing(deps, root, rels, "")
	}
	return dirListing(deps, root, rels)
}

// dirListing proves a flat listing: every visible file of each dir is a file node, every
// visible subdirectory holds one, and the graph holds no other child. A subdirectory is
// read off the file nodes beneath it, since the graph's dir layer is built from them.
func dirListing(deps Dependencies, root string, rels []string) (translation, bool) {
	files, ok := deps.graphIDs(context.Background(), types.KindFile)
	if !ok {
		return translation{}, false
	}
	var prefixes []string
	var answer []string
	for _, rel := range rels {
		entries, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			return translation{}, false
		}
		want := map[string]bool{}
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), ".") {
				continue
			}
			kind := types.KindFile
			if e.IsDir() {
				kind = types.KindDir
			}
			want[kind+":"+joinRel(rel, e.Name())] = true
		}
		if len(want) == 0 {
			return translation{}, false
		}
		got := map[string]bool{}
		under := joinRel(rel, "")
		for _, id := range files {
			p := strings.TrimPrefix(id, types.KindFile+":")
			rest, ok := strings.CutPrefix(p, under)
			if !ok || rest == "" || strings.HasPrefix(rest, ".") {
				continue
			}
			if child, _, nested := strings.Cut(rest, "/"); nested {
				got[types.KindDir+":"+joinRel(rel, child)] = true
			} else {
				got[id] = true
			}
		}
		if !sameKeys(want, got) {
			return translation{}, false
		}
		for id := range got {
			answer = append(answer, id)
		}
		prefixes = append(prefixes, regexp.QuoteMeta(joinRel(rel, "")))
	}
	slices.Sort(answer)
	expr := strings.Join(prefixes, "|")
	if len(prefixes) > 1 {
		expr = "(?:" + expr + ")"
	}
	return translation{
		args: []string{"query", "'id=~^(?:file|dir):" + expr + "[^/]+$'", "-o", "name"},
		why: "The graph holds a node for each of the " + countNoun(len(answer), "name") + " this listing prints, checked name for name, and `" +
			hint.Explain.With("file:<path>") + "` lists what any one defines and who depends on it.",
		answer: answer,
	}, true
}

// treeListing proves a recursive listing of the files under rels, optionally narrowed to a
// regular expression over workspace-relative paths, against the graph's file nodes.
func treeListing(deps Dependencies, root string, rels []string, narrow string) (translation, bool) {
	ids, ok := deps.graphIDs(context.Background(), types.KindFile)
	if !ok {
		return translation{}, false
	}
	var prefixes []string
	for _, rel := range rels {
		prefixes = append(prefixes, regexp.QuoteMeta(joinRel(rel, "")))
	}
	expr := strings.Join(prefixes, "|")
	if len(prefixes) > 1 {
		expr = "(?:" + expr + ")"
	}
	idRe := "^file:" + expr
	if narrow != "" {
		idRe = "^file:(?:" + expr + ")" + narrow
	} else {
		idRe += ".*$"
	}
	re, err := regexp.Compile(idRe)
	if err != nil {
		return translation{}, false
	}
	selected := map[string]bool{}
	var nodes []string
	for _, id := range ids {
		if re.MatchString(id) {
			selected[strings.TrimPrefix(id, types.KindFile+":")] = true
			nodes = append(nodes, id)
		}
	}
	if len(nodes) == 0 {
		return translation{}, false
	}
	found := map[string]bool{}
	deadline := time.Now().Add(findWalkBudget)
	for _, rel := range rels {
		if !walkSelects(root, rel, re, selected, found, deadline) {
			return translation{}, false
		}
	}
	if len(found) != len(selected) {
		return translation{}, false
	}
	slices.Sort(nodes)
	return translation{
		args: []string{"query", "kind=" + types.KindFile, "'id=~" + idRe + "'", "-o", "name"},
		why: "The graph holds a file node for each of the " + countNoun(len(nodes), "file") + " this listing prints, checked name for name, and `" +
			hint.Explain.With("file:<path>") + "` lists what any one defines and who depends on it.",
		answer: nodes,
	}, true
}

// walkSelects walks rel, adding each regular file re selects to found, and reports false at
// the first such file the graph did not select, or at the deadline. Dot-directories and the
// directories no index describes are skipped, as a listing of tracked files skips them.
func walkSelects(root, rel string, re *regexp.Regexp, selected, found map[string]bool, deadline time.Time) bool {
	entries := 0
	err := filepath.WalkDir(filepath.Join(root, filepath.FromSlash(rel)), func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entries++; entries%256 == 0 && time.Now().After(deadline) {
			return errFindBudget
		}
		r, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		r = filepath.ToSlash(r)
		if d.IsDir() {
			if r != rel && (strings.HasPrefix(d.Name(), ".") || nonCodeDirs[d.Name()]) {
				return fs.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() || !re.MatchString(types.KindFile+":"+r) {
			return nil
		}
		if !selected[r] {
			return errFindMismatch
		}
		found[r] = true
		return nil
	})
	return err == nil
}

// translateLsFiles answers `git ls-files <dir|glob>...` with the file nodes it would print.
// Any flag asks something else (untracked, modified, staged, stage numbers), and a pathspec
// naming one file asks whether it is tracked; both stay silent.
func translateLsFiles(deps Dependencies, dir string, c hint.Invocation) (translation, bool) {
	g := parseGit(c.Args)
	if g.sub != "ls-files" || len(g.dirs) > 0 || g.opaque || deps.scope.root == "" {
		return translation{}, false
	}
	var specs []string
	for _, a := range g.rest {
		switch {
		case a == "--":
		case strings.HasPrefix(a, "-"):
			return translation{}, false
		default:
			specs = append(specs, a)
		}
	}
	if len(specs) == 0 || allOutside(deps.scope, specs) {
		return translation{}, false
	}
	root, ok := resolvedRoot(deps.scope.root)
	if !ok {
		return translation{}, false
	}
	var rels, globs []string
	for _, spec := range specs {
		if strings.HasPrefix(spec, ":") {
			return translation{}, false
		}
		if strings.ContainsAny(spec, "*?[") {
			base, glob := globBase(spec)
			_, rel, ok := workspacePath(root, dir, base)
			if !ok {
				return translation{}, false
			}
			rels = append(rels, rel)
			globs = append(globs, gitGlobRegexp(glob))
			continue
		}
		abs, rel, ok := workspacePath(root, dir, spec)
		if !ok {
			return translation{}, false
		}
		if info, err := os.Stat(abs); err != nil || !info.IsDir() {
			return translation{}, false
		}
		rels = append(rels, rel)
		globs = append(globs, ".*")
	}
	// One narrowing expression serves every pathspec only when they agree on it.
	narrow := slices.Compact(slices.Clone(globs))
	if len(narrow) != 1 {
		return translation{}, false
	}
	tail := narrow[0] + "$"
	return treeListing(deps, root, slices.Compact(rels), tail)
}

// globBase splits a pathspec into the directory before its first wildcard and the glob
// below it: `spells/**/spell.buzz` is `spells` and `**/spell.buzz`.
func globBase(spec string) (base, glob string) {
	i := strings.IndexAny(spec, "*?[")
	slash := strings.LastIndexByte(spec[:i], '/')
	if slash < 0 {
		return ".", spec
	}
	return spec[:slash], spec[slash+1:]
}

// gitGlobRegexp is a git pathspec glob as a regexp over the path below its base. Without
// the glob magic git's `*` crosses `/`, so `*.go` is every Go file at any depth.
func gitGlobRegexp(glob string) string {
	var b strings.Builder
	for i := 0; i < len(glob); i++ {
		switch c := glob[i]; c {
		case '*':
			// `**/` matches no directory as well as any number of them.
			if strings.HasPrefix(glob[i:], "**/") {
				b.WriteString(`(?:.*/)?`)
				i += 2
				continue
			}
			b.WriteString(`.*`)
			for i+1 < len(glob) && glob[i+1] == '*' {
				i++
			}
		case '?':
			b.WriteString(`[^/]`)
		case '[':
			end := strings.IndexByte(glob[i:], ']')
			if end < 0 {
				b.WriteString(regexp.QuoteMeta(string(c)))
				continue
			}
			b.WriteString(glob[i : i+end+1])
			i += end
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	return b.String()
}

// joinRel joins a workspace-relative directory and a name, "." being the root.
func joinRel(rel, name string) string {
	if rel == "." {
		return name
	}
	if name == "" {
		return rel + "/"
	}
	return rel + "/" + name
}

func sameKeys(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}
