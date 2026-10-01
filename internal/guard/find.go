package guard

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/types"
)

// A file-finding call (`find`, `fd`, `rg --files`, `ls`, `git ls-files`) is answered by the
// graph's file nodes when every file it would print is one: the proof walks what the call
// would walk, within the hook's budget, and serves a query selecting exactly those files,
// as a pattern when one selects them and enumerated when they are few. The ids come from
// the last build whether or not it is current, since the walk itself checks the disk. A
// metadata flag, a predicate the walk does not model, a file the graph does not index, or a
// walk past the budget keeps the rule silent: the graph would answer a different question.

// findWalkBudget bounds the tree walk a listing is proved over. The guard runs before every
// command, so a walk past it keeps the rule silent rather than slowing the call.
const findWalkBudget = 150 * time.Millisecond

// trackedBudget bounds asking version control for its tracked files, which starts a process.
const trackedBudget = 300 * time.Millisecond

var (
	errFindBudget   = errors.New("guard: listing walk over budget")
	errFindMismatch = errors.New("guard: listing prints an entry the graph does not hold")
)

// indexedFiles is the graph's file ids for a proof that checks the disk itself: the last
// build's, current or not, or GraphIDs' for a caller that wires only that.
func (d Dependencies) indexedFiles(ctx context.Context) ([]string, bool) {
	if d.IndexedIDs != nil {
		if ids, ok := d.IndexedIDs(ctx, types.KindFile); ok {
			return ids, true
		}
	}
	return d.graphIDs(ctx, types.KindFile)
}

// trackedFiles is every file version control tracks under root, workspace-relative, or false
// when it cannot say.
func (d Dependencies) trackedFiles(ctx context.Context, root string) ([]string, bool) {
	if d.TrackedFiles == nil {
		return nil, false
	}
	ctx, cancel := context.WithTimeout(ctx, trackedBudget)
	defer cancel()
	return d.TrackedFiles(ctx, root)
}

// walkRoot is one directory a listing walks: workspace-relative, and as the call spelled
// it, which is how find prints the paths -path matches.
type walkRoot struct{ rel, printed string }

// walkSpec is the walk a file-finding call performs.
type walkSpec struct {
	roots    []walkRoot
	maxDepth int  // 0 for unbounded; 1 lists a root's own entries
	hidden   bool // walks dot-entries; fd, rg and git skip them
	keep     func(name, printed string) bool
	// dirHit reports a directory the call would print; nil for a call that prints files only.
	dirHit func(name, printed string) bool
}

// walkFiles returns the workspace-relative files spec selects, reporting false at a
// directory the call would print, past the budget, or when it selects nothing.
func walkFiles(root string, spec walkSpec) ([]string, bool) {
	var found []string
	entries := 0
	deadline := time.Now().Add(findWalkBudget)
	for _, r := range spec.roots {
		abs := filepath.Join(root, filepath.FromSlash(r.rel))
		err := filepath.WalkDir(abs, func(q string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entries++; entries%256 == 0 && time.Now().After(deadline) {
				return errFindBudget
			}
			sub, err := filepath.Rel(abs, q)
			if err != nil {
				return err
			}
			sub = filepath.ToSlash(sub)
			if sub == "." {
				return nil
			}
			depth := strings.Count(sub, "/") + 1
			printed := r.printed + "/" + sub
			if !spec.hidden && (strings.HasPrefix(d.Name(), ".") || nonCodeDirs[d.Name()]) {
				if d.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
			if d.IsDir() {
				if spec.dirHit != nil && spec.dirHit(d.Name(), printed) {
					return errFindMismatch
				}
				if spec.maxDepth > 0 && depth >= spec.maxDepth {
					return fs.SkipDir
				}
				return nil
			}
			if d.Type().IsRegular() && spec.keep(d.Name(), printed) {
				found = append(found, joinRel(r.rel, sub))
			}
			return nil
		})
		if err != nil {
			return nil, false
		}
	}
	slices.Sort(found)
	found = slices.Compact(found)
	return found, len(found) > 0
}

// fileAnswer is the query a listing is served: idRe when its selection over ids is exactly
// found, else found enumerated when it is short enough to read. It reports false when a
// found file is no graph node, or when neither form is exact and readable.
func fileAnswer(ids, found []string, idRe string) (args, answer []string, ok bool) {
	held := make(map[string]bool, len(ids))
	for _, id := range ids {
		held[id] = true
	}
	want := make(map[string]bool, len(found))
	for _, rel := range found {
		id := types.KindFile + ":" + rel
		if !held[id] {
			return nil, nil, false
		}
		want[id] = true
		answer = append(answer, id)
	}
	if idRe != "" {
		if re, err := regexp.Compile(idRe); err == nil {
			got := map[string]bool{}
			for _, id := range ids {
				if re.MatchString(id) {
					got[id] = true
				}
			}
			if sameKeys(got, want) {
				return []string{"query", "kind=" + types.KindFile, "'id=~" + idRe + "'", "-o", "name"}, answer, true
			}
		}
	}
	if len(found) > answerCap {
		return nil, nil, false
	}
	quoted := make([]string, len(found))
	for i, rel := range found {
		quoted[i] = regexp.QuoteMeta(rel)
	}
	return []string{"query", "kind=" + types.KindFile, "'id=~^file:(?:" + strings.Join(quoted, "|") + ")$'", "-o", "name"}, answer, true
}

// listingTranslation words a proven listing's deny.
func listingTranslation(args, answer []string, call string) translation {
	return translation{
		args: args,
		why: "The graph holds a file node for each of the " + countNoun(len(answer), "file") + " this " + call + " prints, checked name for name, and `" +
			hint.Explain.With("file:<path>") + "` lists what any one defines and who depends on it.",
		answer: answer,
		proven: true,
	}
}

// listingRoots resolves a call's directory operands, reporting false for one outside the
// workspace, missing, or not a directory.
func listingRoots(root, dir string, operands []string) ([]walkRoot, bool) {
	roots := make([]walkRoot, 0, len(operands))
	for _, p := range operands {
		abs, rel, ok := workspacePath(root, dir, p)
		if !ok {
			return nil, false
		}
		if info, err := os.Stat(abs); err != nil || !info.IsDir() {
			return nil, false
		}
		printed := strings.TrimSuffix(p, "/")
		if printed == "" {
			printed = p
		}
		roots = append(roots, walkRoot{rel: rel, printed: printed})
	}
	return roots, true
}

// rootPrefix is the id-regex alternation selecting the files under roots, with at most
// maxDepth directory levels when bounded.
func rootPrefix(roots []walkRoot, maxDepth int) string {
	prefixes := make([]string, 0, len(roots))
	for _, r := range roots {
		prefix := ""
		if r.rel != "." {
			prefix = regexp.QuoteMeta(r.rel) + "/"
		}
		if maxDepth > 0 {
			prefix += `(?:[^/]+/){0,` + strconv.Itoa(maxDepth-1) + `}`
		} else {
			prefix += `(?:.*/)?`
		}
		prefixes = append(prefixes, prefix)
	}
	expr := strings.Join(slices.Compact(prefixes), "|")
	if len(prefixes) > 1 {
		expr = "(?:" + expr + ")"
	}
	return expr
}

// findCall is one find invocation reduced to what decides its answer.
type findCall struct {
	paths    []string
	name     string
	pathGlob string
	notNames []string
	notPaths []string
	files    bool // -type f
	maxDepth int  // 0 for unbounded
}

// parseFindCall reads c as a name or path search, or reports false for any other
// predicate, which asks something the file nodes do not say.
func parseFindCall(c hint.Invocation) (findCall, bool) {
	fc := findCall{}
	args := c.Args
	for len(args) > 0 && !strings.HasPrefix(args[0], "-") && args[0] != "(" && args[0] != "!" {
		fc.paths = append(fc.paths, args[0])
		args = args[1:]
	}
	if len(fc.paths) == 0 {
		fc.paths = []string{"."}
	}
	// find negates a class with `[!x]`, which path.Match and a regexp read as a literal
	// `!`, so the proof would agree with itself on the complement.
	glob := func(i int) (string, bool) {
		if i >= len(args) || strings.Contains(args[i], "[!") {
			return "", false
		}
		_, err := path.Match(args[i], "")
		return args[i], err == nil
	}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-name":
			g, ok := glob(i + 1)
			if !ok || fc.name != "" || strings.Contains(g, "/") {
				return findCall{}, false
			}
			fc.name = g
			i++
		case "-path", "-wholename":
			g, ok := glob(i + 1)
			if !ok || fc.pathGlob != "" {
				return findCall{}, false
			}
			fc.pathGlob = g
			i++
		case "-not", "!":
			if i+2 >= len(args) {
				return findCall{}, false
			}
			g, ok := glob(i + 2)
			if !ok {
				return findCall{}, false
			}
			switch args[i+1] {
			case "-name":
				fc.notNames = append(fc.notNames, g)
			case "-path", "-wholename":
				fc.notPaths = append(fc.notPaths, g)
			default:
				return findCall{}, false
			}
			i += 2
		case "-type":
			if i+1 >= len(args) || args[i+1] != "f" {
				return findCall{}, false
			}
			fc.files = true
			i++
		case "-maxdepth":
			if i+1 >= len(args) {
				return findCall{}, false
			}
			n, err := strconv.Atoi(args[i+1])
			if err != nil || n < 1 {
				return findCall{}, false
			}
			fc.maxDepth = n
			i++
		case "-print":
		default:
			return findCall{}, false
		}
	}
	return fc, fc.name != "" || fc.pathGlob != ""
}

// findPathMatch is find's -path match: the whole printed path against a glob whose `*`
// crosses `/`.
func findPathMatch(glob string) func(string) bool {
	re, err := regexp.Compile("^" + strings.ReplaceAll(gitGlobRegexp(glob), `(?:.*/)?`, `.*`) + "$")
	if err != nil {
		return func(string) bool { return false }
	}
	return re.MatchString
}

// translateFind answers a find of names or paths under the workspace with the query
// selecting the same file nodes.
func translateFind(deps Dependencies, dir string, c hint.Invocation) (translation, bool) {
	fc, ok := parseFindCall(c)
	if !ok || deps.scope.root == "" || allOutside(deps.scope, fc.paths) {
		return translation{}, false
	}
	root, ok := resolvedRoot(deps.scope.root)
	if !ok {
		return translation{}, false
	}
	roots, ok := listingRoots(root, dir, fc.paths)
	if !ok {
		return translation{}, false
	}
	positive := func(name, printed string) bool {
		if fc.name != "" {
			if matched, _ := path.Match(fc.name, name); !matched {
				return false
			}
		}
		if fc.pathGlob != "" && !findPathMatch(fc.pathGlob)(printed) {
			return false
		}
		for _, g := range fc.notNames {
			if matched, _ := path.Match(g, name); matched {
				return false
			}
		}
		for _, g := range fc.notPaths {
			if findPathMatch(g)(printed) {
				return false
			}
		}
		return true
	}
	spec := walkSpec{roots: roots, maxDepth: fc.maxDepth, hidden: true, keep: positive}
	if !fc.files {
		spec.dirHit = positive
	}
	found, ok := walkFiles(root, spec)
	if !ok {
		return translation{}, false
	}
	ids, ok := deps.indexedFiles(context.Background())
	if !ok {
		return translation{}, false
	}
	idRe := ""
	if fc.pathGlob == "" && len(fc.notNames) == 0 && len(fc.notPaths) == 0 {
		idRe = `^file:` + rootPrefix(roots, fc.maxDepth) + globRegexp(fc.name) + `$`
	}
	args, answer, ok := fileAnswer(ids, found, idRe)
	if !ok {
		return translation{}, false
	}
	return listingTranslation(args, answer, "find"), true
}

// fdCall is one fd invocation reduced to what decides its answer.
type fdCall struct {
	pattern  string
	glob     bool
	fixed    bool
	exts     []string
	files    bool
	maxDepth int
	folded   bool // case-insensitive, by -i or by fd's smart case
	paths    []string
}

// parseFdCall reads c as an fd name search, or reports false for a flag it does not model:
// hidden or ignored files, full-path matching, an exclude, or an exec.
func parseFdCall(c hint.Invocation) (fdCall, bool) {
	var fc fdCall
	sensitive, insensitive := false, false
	var operands []string
	value := func(i *int, a, long, short string) (string, bool, bool) {
		if v, ok := strings.CutPrefix(a, long+"="); ok {
			return v, true, true
		}
		if a == long || a == short {
			if *i+1 >= len(c.Args) {
				return "", true, false
			}
			*i++
			return c.Args[*i], true, true
		}
		if short != "" && strings.HasPrefix(a, short) && len(a) > len(short) && !strings.HasPrefix(a, "--") {
			return a[len(short):], true, true
		}
		return "", false, true
	}
	for i := 0; i < len(c.Args); i++ {
		a := c.Args[i]
		if a == "--" {
			operands = append(operands, c.Args[i+1:]...)
			break
		}
		if v, matched, ok := value(&i, a, "--type", "-t"); matched {
			if !ok || (v != "f" && v != "file") {
				return fdCall{}, false
			}
			fc.files = true
			continue
		}
		if v, matched, ok := value(&i, a, "--extension", "-e"); matched {
			if !ok || v == "" {
				return fdCall{}, false
			}
			fc.exts = append(fc.exts, strings.ToLower(strings.TrimPrefix(v, ".")))
			continue
		}
		if v, matched, ok := value(&i, a, "--max-depth", "-d"); matched {
			n, err := strconv.Atoi(v)
			if !ok || err != nil || n < 1 {
				return fdCall{}, false
			}
			fc.maxDepth = n
			continue
		}
		switch a {
		case "-g", "--glob":
			fc.glob = true
		case "-F", "--fixed-strings":
			fc.fixed = true
		case "-s", "--case-sensitive":
			sensitive = true
		case "-i", "--ignore-case":
			insensitive = true
		default:
			if strings.HasPrefix(a, "-") {
				return fdCall{}, false
			}
			operands = append(operands, a)
		}
	}
	if len(operands) > 0 {
		fc.pattern, fc.paths = operands[0], operands[1:]
	}
	if len(fc.paths) == 0 {
		fc.paths = []string{"."}
	}
	fc.folded = insensitive || (!sensitive && strings.ToLower(fc.pattern) == fc.pattern)
	return fc, fc.pattern != "" || len(fc.exts) > 0
}

// translateFd answers an fd name search with the query selecting the same file nodes. fd
// skips hidden entries and ignored trees; a file it would skip that the walk reads is no
// graph node, so the proof fails rather than overstating.
func translateFd(deps Dependencies, dir string, c hint.Invocation) (translation, bool) {
	fc, ok := parseFdCall(c)
	if !ok || deps.scope.root == "" || allOutside(deps.scope, fc.paths) {
		return translation{}, false
	}
	root, ok := resolvedRoot(deps.scope.root)
	if !ok {
		return translation{}, false
	}
	roots, ok := listingRoots(root, dir, fc.paths)
	if !ok {
		return translation{}, false
	}
	nameExpr := ""
	switch {
	case fc.pattern == "":
	case fc.glob:
		nameExpr = globRegexp(fc.pattern)
	case fc.fixed:
		nameExpr = `[^/]*` + regexp.QuoteMeta(fc.pattern) + `[^/]*`
	default:
		nameExpr = `[^/]*(?:` + fc.pattern + `)[^/]*`
	}
	if fc.folded && nameExpr != "" {
		nameExpr = "(?i:" + nameExpr + ")"
	}
	nameRe, err := regexp.Compile("^" + cmpOr(nameExpr, `.*`) + "$")
	if err != nil {
		return translation{}, false
	}
	hasExt := func(name string) bool {
		if len(fc.exts) == 0 {
			return true
		}
		ext := strings.ToLower(strings.TrimPrefix(path.Ext(name), "."))
		return slices.Contains(fc.exts, ext)
	}
	keep := func(name, _ string) bool { return nameRe.MatchString(name) && hasExt(name) }
	spec := walkSpec{roots: roots, maxDepth: fc.maxDepth, keep: keep}
	// With -e fd matches files only, as it does with -t f.
	if !fc.files && len(fc.exts) == 0 {
		spec.dirHit = keep
	}
	found, ok := walkFiles(root, spec)
	if !ok {
		return translation{}, false
	}
	ids, ok := deps.indexedFiles(context.Background())
	if !ok {
		return translation{}, false
	}
	idRe := ""
	switch {
	case len(fc.exts) == 0:
		idRe = `^file:` + rootPrefix(roots, fc.maxDepth) + nameExpr + `$`
	case fc.pattern == "":
		quoted := make([]string, len(fc.exts))
		for i, e := range fc.exts {
			quoted[i] = regexp.QuoteMeta(e)
		}
		idRe = `^file:` + rootPrefix(roots, fc.maxDepth) + `[^/]*\.(?i:` + strings.Join(quoted, "|") + `)$`
	}
	args, answer, ok := fileAnswer(ids, found, idRe)
	if !ok {
		return translation{}, false
	}
	return listingTranslation(args, answer, "fd"), true
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// rgFilesCall is one `rg --files` invocation reduced to what decides its answer.
type rgFilesCall struct {
	paths    []string
	globs    []string // basename globs a file must match one of
	notGlobs []string // basename globs a file must match none of
}

// parseRgFiles reads c as `rg --files`, reporting false without --files or with a flag it
// does not model: hidden or ignored files, a type exclusion, a glob naming a directory.
func parseRgFiles(c hint.Invocation) (rgFilesCall, bool) {
	var rc rgFilesCall
	listing := false
	addGlob := func(g string) bool {
		neg := strings.HasPrefix(g, "!")
		g = strings.TrimPrefix(g, "!")
		if strings.Contains(g, "/") || g == "" {
			return false
		}
		if _, err := path.Match(g, ""); err != nil {
			return false
		}
		if neg {
			rc.notGlobs = append(rc.notGlobs, g)
		} else {
			rc.globs = append(rc.globs, g)
		}
		return true
	}
	for i := 0; i < len(c.Args); i++ {
		a := c.Args[i]
		flag, val, hasVal := strings.Cut(a, "=")
		switch {
		case a == "--files":
			listing = true
		case flag == "-g" || flag == "--glob":
			if !hasVal {
				if i+1 >= len(c.Args) {
					return rgFilesCall{}, false
				}
				i++
				val = c.Args[i]
			}
			if !addGlob(val) {
				return rgFilesCall{}, false
			}
		case flag == "-t" || flag == "--type":
			if !hasVal {
				if i+1 >= len(c.Args) {
					return rgFilesCall{}, false
				}
				i++
				val = c.Args[i]
			}
			g, known := rgTypeGlobs[val]
			if !known || !addGlob(g) {
				return rgFilesCall{}, false
			}
		case strings.HasPrefix(a, "-"):
			return rgFilesCall{}, false
		default:
			rc.paths = append(rc.paths, a)
		}
	}
	if len(rc.paths) == 0 {
		rc.paths = []string{"."}
	}
	return rc, listing
}

// translateRgFiles answers `rg --files` with the query selecting the same file nodes.
func translateRgFiles(deps Dependencies, dir string, c hint.Invocation) (translation, bool) {
	rc, ok := parseRgFiles(c)
	if !ok || deps.scope.root == "" || allOutside(deps.scope, rc.paths) {
		return translation{}, false
	}
	root, ok := resolvedRoot(deps.scope.root)
	if !ok {
		return translation{}, false
	}
	roots, ok := listingRoots(root, dir, rc.paths)
	if !ok {
		return translation{}, false
	}
	matchAny := func(globs []string, name string) bool {
		return slices.ContainsFunc(globs, func(g string) bool {
			matched, _ := path.Match(g, name)
			return matched
		})
	}
	keep := func(name, _ string) bool {
		return (len(rc.globs) == 0 || matchAny(rc.globs, name)) && !matchAny(rc.notGlobs, name)
	}
	found, ok := walkFiles(root, walkSpec{roots: roots, keep: keep})
	if !ok {
		return translation{}, false
	}
	ids, ok := deps.indexedFiles(context.Background())
	if !ok {
		return translation{}, false
	}
	idRe := ""
	switch {
	case len(rc.notGlobs) > 0:
	case len(rc.globs) == 0:
		idRe = `^file:` + rootPrefix(roots, 0) + `[^/]+$`
	case len(rc.globs) == 1:
		idRe = `^file:` + rootPrefix(roots, 0) + globRegexp(rc.globs[0]) + `$`
	}
	args, answer, ok := fileAnswer(ids, found, idRe)
	if !ok {
		return translation{}, false
	}
	return listingTranslation(args, answer, "rg --files"), true
}

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
	roots, ok := listingRoots(root, dir, lc.dirs)
	if !ok {
		return translation{}, false
	}
	for _, r := range roots {
		for _, seg := range strings.Split(r.rel, "/") {
			if nonCodeDirs[seg] {
				return translation{}, false
			}
		}
	}
	if lc.recursive {
		found, ok := walkFiles(root, walkSpec{roots: roots, keep: func(string, string) bool { return true }})
		if !ok {
			return translation{}, false
		}
		ids, ok := deps.indexedFiles(context.Background())
		if !ok {
			return translation{}, false
		}
		args, answer, ok := fileAnswer(ids, found, `^file:`+rootPrefix(roots, 0)+`[^/]+$`)
		if !ok {
			return translation{}, false
		}
		return listingTranslation(args, answer, "listing"), true
	}
	rels := make([]string, len(roots))
	for i, r := range roots {
		rels[i] = r.rel
	}
	return dirListing(deps, root, rels)
}

// dirListing proves a flat listing: every visible file of each dir is a file node, every
// visible subdirectory holds one, and the graph holds no other child. A subdirectory is
// read off the file nodes beneath it, since the graph's dir layer is built from them.
func dirListing(deps Dependencies, root string, rels []string) (translation, bool) {
	files, ok := deps.indexedFiles(context.Background())
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
		proven: true,
	}, true
}

// lsFilesSpec is a `git ls-files` pathspec as a match over workspace-relative paths and the
// id-regex fragment selecting the same paths.
type lsFilesSpec struct {
	match func(rel string) bool
	expr  string
}

// parseLsFilesSpecs reads the pathspecs of `git ls-files`, reporting false for a flag (the
// untracked, modified, staged and stage-number listings), pathspec magic, or a pathspec
// naming one file, which asks whether that file is tracked.
func parseLsFilesSpecs(root, dir string, rest []string) ([]lsFilesSpec, bool) {
	var specs []lsFilesSpec
	for _, a := range rest {
		switch {
		case a == "--":
		case strings.HasPrefix(a, "-"), strings.HasPrefix(a, ":"):
			return nil, false
		case strings.ContainsAny(a, "*?["):
			base, glob := globBase(a)
			_, rel, ok := workspacePath(root, dir, base)
			if !ok {
				return nil, false
			}
			expr := regexp.QuoteMeta(joinRel(rel, "")) + gitGlobRegexp(glob)
			re, err := regexp.Compile("^" + expr + "$")
			if err != nil {
				return nil, false
			}
			specs = append(specs, lsFilesSpec{match: re.MatchString, expr: expr})
		default:
			abs, rel, ok := workspacePath(root, dir, a)
			if !ok {
				return nil, false
			}
			if info, err := os.Stat(abs); err != nil || !info.IsDir() {
				return nil, false
			}
			under := joinRel(rel, "")
			specs = append(specs, lsFilesSpec{match: func(p string) bool { return strings.HasPrefix(p, under) }, expr: regexp.QuoteMeta(under) + ".*"})
		}
	}
	if len(specs) == 0 {
		// The whole checkout, from the call's directory.
		_, rel, ok := workspacePath(root, dir, ".")
		if !ok {
			return nil, false
		}
		under := joinRel(rel, "")
		specs = append(specs, lsFilesSpec{match: func(p string) bool { return strings.HasPrefix(p, under) }, expr: regexp.QuoteMeta(under) + ".*"})
	}
	return specs, true
}

// pathFilter is the search a `git ls-files` is piped into, read as a match over the paths
// it prints, and an id-regex fragment selecting them when the pattern carries no anchor.
func pathFilter(next []hint.Invocation) (match func(string) bool, expr string, piped, ok bool) {
	if len(next) == 0 || !hint.IsSearchTool(path.Base(next[0].Name)) {
		return func(string) bool { return true }, "", false, true
	}
	c := next[0]
	folded := hasFlag(c.Args, 'i', "ignore-case")
	args := slices.DeleteFunc(slices.Clone(c.Args), func(a string) bool { return a == "-i" || a == "--ignore-case" })
	sc, parsed := parseSearchCall(hint.Invocation{Name: c.Name, Args: args})
	if !parsed || len(sc.paths) > 0 {
		return nil, "", true, false
	}
	alts, altsOK := sc.alternatives()
	if !altsOK {
		return nil, "", true, false
	}
	body := "(?:" + strings.Join(alts, ")|(?:") + ")"
	if sc.word {
		body = `\b(?:` + body + `)\b`
	}
	if folded {
		body = "(?i:" + body + ")"
	}
	re, err := regexp.Compile(body)
	if err != nil {
		return nil, "", true, false
	}
	if !strings.ContainsAny(strings.Join(sc.patterns, ""), "^$") {
		expr = ".*" + body + ".*"
	}
	return re.MatchString, expr, true, true
}

// translateLsFiles answers `git ls-files <dir|glob>...`, and the same piped into a search
// of the paths it prints, with the file nodes it would print. Version control names the
// tracked files; the graph indexes only some of them and never an untracked one (measured
// here 2026-09-30: 2,689 of 4,940 tracked, none untracked; Markdown under changes/ and docs/
// is most of the rest), so the deny names every tracked file the graph does not hold, and
// stays silent when those are too many to name. With no answer from version control, a walk
// of a named directory stands in for it, and the whole checkout is not attempted.
func translateLsFiles(deps Dependencies, dir string, c hint.Invocation, next []hint.Invocation) (translation, bool) {
	g := parseGit(c.Args)
	if g.sub != "ls-files" || len(g.dirs) > 0 || g.opaque || g.configured || deps.scope.root == "" {
		return translation{}, false
	}
	root, ok := resolvedRoot(deps.scope.root)
	if !ok {
		return translation{}, false
	}
	specs, ok := parseLsFilesSpecs(root, dir, g.rest)
	if !ok {
		return translation{}, false
	}
	filter, filterExpr, piped, ok := pathFilter(next)
	if !ok {
		return translation{}, false
	}
	if _, rel, ok := workspacePath(root, dir, "."); !ok || (piped && rel != ".") {
		// Piped paths print relative to the call's directory; the filter reads them as printed.
		return translation{}, false
	}
	selects := func(rel string) bool {
		return filter(rel) && slices.ContainsFunc(specs, func(s lsFilesSpec) bool { return s.match(rel) })
	}
	ids, ok := deps.indexedFiles(context.Background())
	if !ok {
		return translation{}, false
	}
	var indexed []string
	for _, id := range ids {
		if rel := strings.TrimPrefix(id, types.KindFile+":"); selects(rel) {
			indexed = append(indexed, rel)
		}
	}
	if len(indexed) == 0 {
		return translation{}, false
	}
	tracked, ok := deps.trackedFiles(context.Background(), root)
	var excluded []string
	if ok {
		held := map[string]bool{}
		for _, t := range tracked {
			if selects(t) {
				held[t] = true
			}
		}
		for _, rel := range indexed {
			if !held[rel] {
				// An indexed file version control does not track: the graph would answer more.
				return translation{}, false
			}
			delete(held, rel)
		}
		for t := range held {
			excluded = append(excluded, t)
		}
		slices.Sort(excluded)
		if len(excluded) > answerCap {
			return translation{}, false
		}
	} else {
		if piped || len(g.rest) == 0 {
			return translation{}, false
		}
		var roots []walkRoot
		for _, a := range g.rest {
			if a == "--" || strings.ContainsAny(a, "*?[") {
				return translation{}, false
			}
			r, rootsOK := listingRoots(root, dir, []string{a})
			if !rootsOK {
				return translation{}, false
			}
			roots = append(roots, r...)
		}
		found, walked := walkFiles(root, walkSpec{roots: roots, keep: func(string, string) bool { return true }})
		if !walked || !slices.Equal(found, indexed) {
			return translation{}, false
		}
	}
	exprs := make([]string, len(specs))
	for i, s := range specs {
		exprs[i] = s.expr
	}
	idRe := ""
	switch {
	case !piped:
		idRe = "^file:(?:" + strings.Join(slices.Compact(exprs), "|") + ")$"
	case filterExpr != "" && len(specs) == 1 && strings.HasSuffix(specs[0].expr, ".*"):
		idRe = "^file:" + strings.TrimSuffix(specs[0].expr, ".*") + filterExpr + "$"
	}
	args, answer, ok := fileAnswer(ids, indexed, idRe)
	if !ok {
		return translation{}, false
	}
	call := "listing"
	if piped {
		call = "filtered listing"
	}
	tr := listingTranslation(args, answer, call)
	if piped {
		tr.consumed = 1
	}
	if len(excluded) > 0 {
		tr.reach = "for every matching file the graph indexes"
		tr.why += " Version control also tracks " + countNoun(len(excluded), "matching file") +
			" the graph does not index, which the query leaves out: " + strings.Join(excluded, ", ") + "."
	}
	return tr, true
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
