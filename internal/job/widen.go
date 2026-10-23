package job

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/types"
)

// defaultMaxProvedWidenings is how many proved widenings one attempt takes.
const defaultMaxProvedWidenings = 5

// maxWideningCommand bounds the `job apply` line the guard serves, so a refusal carrying
// it stays under a kilobyte.
const maxWideningCommand = 640

// maxWideningSource is the largest file a proof reads; a larger one proves nothing.
const maxWideningSource = 1 << 20

// wideningLimit is the cap on an attempt's proved widenings and where it came from.
type wideningLimit struct {
	Max    int
	Source string
}

// DefaultWideningLimit is the cap when the workspace names none.
func DefaultWideningLimit() wideningLimit {
	return wideningLimit{Max: defaultMaxProvedWidenings, Source: "magus's default; magus.yaml cannot set jobs.max_proved_widenings yet"}
}

// wideningFacts is what the tree says about one job's change, read before the store's lock
// so the proof under it reads nothing. Build it with [ObserveWidening].
type wideningFacts struct {
	seen        Observed
	files       map[string]sourceFacts
	packages    map[string]goPackage
	regenerated []RegeneratedOutput
	// outputsKnown says a workspace answered for the outputs: none read proves no output.
	outputsKnown bool
}

// sourceFacts is one file as a proof reads it. pkg is empty for a file that is not Go,
// which a proof then reads as text.
type sourceFacts struct {
	read     bool
	pkg      string
	imports  []string
	decls    map[string][2]int
	uses     map[string]bool
	literals []string
	text     string
}

// goPackage is a Go package the job changed inside its write paths: its name and each
// top-level declaration's file and lines.
type goPackage struct {
	dir, name string
	decls     map[string]declSite
}

type declSite struct {
	file  string
	lines [2]int
}

// ObserveWidening reads the facts a proof of asked needs: the job's diff since its
// checkpoint, the asked files and every file the job changed inside its write paths, the Go
// packages those sit in, and the outputs projects regenerate from them. Nil projects leaves
// every output proof unproved. A diff that cannot be read leaves the refs, test and output
// proofs unproved too.
func ObserveWidening(ctx context.Context, root string, row types.Job, asked []string, projects []*types.Project) wideningFacts {
	seen, _ := CheckpointObserver(root, nil)(ctx, row)
	return readWideningFacts(WorkTree(root, row), row, seen, asked, projects)
}

func readWideningFacts(tree string, row types.Job, seen Observed, asked []string, projects []*types.Project) wideningFacts {
	facts := wideningFacts{seen: seen, files: map[string]sourceFacts{}, packages: map[string]goPackage{}}
	changed := changedInside(row, seen)
	for _, rel := range append(slices.Clone(asked), changed...) {
		rel = path.Clean(rel)
		if _, ok := facts.files[rel]; !ok {
			facts.files[rel] = readSource(tree, rel)
		}
	}
	for _, rel := range changed {
		if dir := path.Dir(rel); path.Ext(rel) == ".go" {
			if _, ok := facts.packages[dir]; !ok {
				facts.packages[dir] = readPackage(tree, dir)
			}
		}
	}
	if projects != nil && seen.ChangedKnown {
		facts.outputsKnown = true
		facts.regenerated = RegeneratedOutside(tree, types.Job{WritePaths: changed}, projects, nil)
	}
	return facts
}

// changedInside is what the job's diff changed inside its write paths, sorted.
func changedInside(row types.Job, seen Observed) []string {
	var out []string
	for _, f := range seen.Changed {
		if slices.ContainsFunc(row.WritePaths, func(w string) bool { return covers(w, f) }) {
			out = append(out, path.Clean(f))
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

func readSource(tree, rel string) sourceFacts {
	full := filepath.Join(tree, filepath.FromSlash(rel))
	if info, err := os.Stat(full); err != nil || !info.Mode().IsRegular() || info.Size() > maxWideningSource {
		return sourceFacts{}
	}
	body, err := os.ReadFile(full)
	if err != nil {
		return sourceFacts{}
	}
	if path.Ext(rel) != ".go" {
		return sourceFacts{read: true, text: string(body)}
	}
	return parseGo(rel, body)
}

// parseGo reads a Go file's package, imports, top-level declarations, the identifiers and
// `x.Name` selectors it uses, and its string literals. A file that does not parse is read
// as text.
func parseGo(rel string, body []byte) sourceFacts {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, rel, body, parser.SkipObjectResolution)
	if err != nil || f == nil {
		return sourceFacts{read: true, text: string(body)}
	}
	s := sourceFacts{read: true, pkg: f.Name.Name, decls: map[string][2]int{}, uses: map[string]bool{}}
	lines := func(n ast.Node) [2]int { return [2]int{fset.Position(n.Pos()).Line, fset.Position(n.End()).Line} }
	for _, imp := range f.Imports {
		if p, err := strconv.Unquote(imp.Path.Value); err == nil {
			s.imports = append(s.imports, p)
		}
	}
	for _, d := range f.Decls {
		switch d := d.(type) {
		case *ast.FuncDecl:
			s.decls[d.Name.Name] = lines(d)
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				switch spec := spec.(type) {
				case *ast.TypeSpec:
					s.decls[spec.Name.Name] = lines(spec)
				case *ast.ValueSpec:
					for _, n := range spec.Names {
						s.decls[n.Name] = lines(spec)
					}
				}
			}
		}
	}
	ast.Inspect(f, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.SelectorExpr:
			if x, ok := n.X.(*ast.Ident); ok {
				s.uses[x.Name+"."+n.Sel.Name] = true
			}
		case *ast.Ident:
			s.uses[n.Name] = true
		case *ast.BasicLit:
			if lit, err := strconv.Unquote(n.Value); err == nil && n.Kind == token.STRING {
				s.literals = append(s.literals, lit)
			}
		}
		return true
	})
	return s
}

// readPackage is the Go package in dir as its non-test files declare it. A directory the
// diff emptied keeps its base name, the name its callers import it by.
func readPackage(tree, dir string) goPackage {
	p := goPackage{dir: dir, name: path.Base(dir), decls: map[string]declSite{}}
	entries, err := os.ReadDir(filepath.Join(tree, filepath.FromSlash(dir)))
	if err != nil {
		return p
	}
	named := false
	for _, e := range entries {
		rel := path.Join(dir, e.Name())
		if e.IsDir() || path.Ext(rel) != ".go" || types.IsTestPath(rel) {
			continue
		}
		src := readSource(tree, rel)
		if src.pkg == "" {
			continue
		}
		if !named {
			p.name, named = src.pkg, true
		}
		for name, at := range src.decls {
			p.decls[name] = declSite{file: rel, lines: at}
		}
	}
	return p
}

// proveWidening is the sentence that proves rel in scope of row by e, or why e does not.
// It reads only facts, so the store calls it under its lock against the row it holds.
func proveWidening(row types.Job, rel string, e types.WideningEvidence, facts wideningFacts) (string, error) {
	rel = path.Clean(strings.TrimSpace(rel))
	if e.Kind != types.WideningReleased && !facts.seen.ChangedKnown {
		return "", fmt.Errorf("magus could not read what the job changed (%s), so nothing it changed proves %s",
			cmp.Or(facts.seen.RegionsReason, "no diff was read"), rel)
	}
	switch e.Kind {
	case types.WideningRefs:
		return proveRefs(rel, e.Subject, facts)
	case types.WideningTest:
		return proveTest(row, rel, path.Clean(e.Subject), facts)
	case types.WideningOutput:
		return proveOutput(rel, e.Subject, facts)
	case types.WideningReleased:
		return proveReleased(row, rel)
	}
	return "", fmt.Errorf("%q is not a proof", e.String())
}

func proveRefs(rel, subject string, facts wideningFacts) (string, error) {
	asked := facts.files[rel]
	switch {
	case !asked.read:
		return "", fmt.Errorf("%s could not be read", rel)
	case asked.pkg == "":
		return "", fmt.Errorf("a refs proof reads Go source, and %s is not Go", rel)
	}
	pkg, name, qualified := strings.Cut(subject, ".")
	if !qualified {
		pkg, name = "", subject
	}
	for _, dir := range slices.Sorted(maps.Keys(facts.packages)) {
		p := facts.packages[dir]
		if qualified {
			if p.name != pkg || !asked.uses[subject] || !slices.ContainsFunc(asked.imports, func(i string) bool { return i == dir || strings.HasSuffix(i, "/"+dir) }) {
				continue
			}
		} else if dir != path.Dir(rel) || !asked.uses[name] {
			continue
		}
		if how, ok := p.changed(name, facts.seen); ok {
			return fmt.Sprintf("%s references %s, which %s", rel, subject, how), nil
		}
	}
	return "", fmt.Errorf("no package the job changed inside its write paths declares %s where %s uses it, or the job's diff left it alone", subject, rel)
}

// changed says how the job's diff changed name in p: removed it from the package, or
// touched the lines that declare it.
func (p goPackage) changed(name string, seen Observed) (string, bool) {
	site, declared := p.decls[name]
	if !declared {
		return fmt.Sprintf("the job's diff removed from package %s (%s)", p.name, p.dir), true
	}
	if !seen.RegionsKnown {
		return "", false
	}
	for _, r := range seen.Regions {
		if path.Clean(r.File.Path) != site.file {
			continue
		}
		overlaps := r.Side == types.RegionNew && r.Lines[0] <= site.lines[1] && r.Lines[1] >= site.lines[0]
		if overlaps || declaredName(r.Declaration) == name {
			return "the job's diff changes in " + site.file, true
		}
	}
	return "", false
}

var declaredNamePattern = regexp.MustCompile(`^(?:func\s*(?:\([^)]*\)\s*)?|type\s+|var\s+|const\s+)([A-Za-z_][A-Za-z0-9_]*)`)

// declaredName is the name a diff driver's declaration line declares, "" for a block
// opener or a line naming none.
func declaredName(decl string) string {
	m := declaredNamePattern.FindStringSubmatch(strings.TrimSpace(decl))
	if m == nil {
		return ""
	}
	return m[1]
}

func proveTest(row types.Job, rel, subject string, facts wideningFacts) (string, error) {
	if types.IsTestPath(subject) || !slices.Contains(changedInside(row, facts.seen), subject) {
		return "", fmt.Errorf("%s is not a file the job changed inside its write paths, other than a test", subject)
	}
	dir := path.Dir(subject)
	beside := types.IsTestPath(rel) && path.Dir(rel) == dir || strings.HasPrefix(rel, dir+"/testdata/")
	if !beside {
		return "", fmt.Errorf("%s is not a test beside %s: a _test file in %s or a file under %s/testdata", rel, subject, dir, dir)
	}
	asked := facts.files[rel]
	if !asked.read {
		return "", fmt.Errorf("%s could not be read", rel)
	}
	if named, ok := mentions(asked, facts.files[subject]); ok {
		return fmt.Sprintf("%s tests %s: it names %s", rel, subject, named), nil
	}
	return "", fmt.Errorf("%s names nothing %s defines", rel, subject)
}

// mentions is the first thing src defines that asked names: a declaration, or for a test
// that is not Go, a string literal of at least five characters holding a letter.
func mentions(asked, src sourceFacts) (string, bool) {
	for _, name := range slices.Sorted(maps.Keys(src.decls)) {
		if asked.pkg != "" && asked.uses[name] || asked.pkg == "" && containsWord(asked.text, name) {
			return name, true
		}
	}
	if asked.pkg != "" {
		return "", false
	}
	for _, lit := range slices.Sorted(slices.Values(src.literals)) {
		if len(lit) >= 5 && strings.ContainsFunc(lit, unicode.IsLetter) && strings.Contains(asked.text, lit) {
			return strconv.Quote(lit), true
		}
	}
	return "", false
}

func containsWord(text, word string) bool {
	for i := strings.Index(text, word); i >= 0; {
		end := i + len(word)
		if (i == 0 || !isIdentRune(rune(text[i-1]))) && (end == len(text) || !isIdentRune(rune(text[end]))) {
			return true
		}
		next := strings.Index(text[i+1:], word)
		if next < 0 {
			return false
		}
		i += next + 1
	}
	return false
}

func isIdentRune(r rune) bool { return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) }

func proveOutput(rel, target string, facts wideningFacts) (string, error) {
	if !facts.outputsKnown {
		return "", errors.New("no workspace answered for its targets' outputs, so no output is proved")
	}
	for _, o := range facts.regenerated {
		parts := strings.Split(o.Output, " !")
		if o.Target == target && (types.Glob{Pattern: parts[0], Except: parts[1:]}).Match(rel) {
			return fmt.Sprintf("%s is an output of %s, which reads %s, a source the job changed", rel, target, o.Source), nil
		}
	}
	return "", fmt.Errorf("%s is not an output %s regenerates from what the job changed", rel, target)
}

func proveReleased(row types.Job, rel string) (string, error) {
	for _, r := range slices.Backward(row.Releases) {
		if !coversPath(path.Clean(r.Path), rel) || r.ReleasedAt < row.Registered {
			continue
		}
		if r.Revoked {
			return "", fmt.Errorf("the orchestrator revoked %s, and a holder never takes back a revoked path", r.Path)
		}
		return fmt.Sprintf("this attempt's holder released it at %s", time.Unix(r.ReleasedAt, 0).UTC().Format(time.RFC3339)), nil
	}
	return "", fmt.Errorf("this attempt's holder never released %s", rel)
}

// checkWideningPath refuses rel to row whatever proves it: a path it holds already, one
// its deny paths cover, and one another live job holds.
func checkWideningPath(row types.Job, rel string, rows []types.Job) error {
	switch {
	case slices.ContainsFunc(row.WritePaths, func(w string) bool { return covers(w, rel) }):
		return fmt.Errorf("%s is already a write path of %s", rel, row.ID)
	case slices.ContainsFunc(row.DenyPaths, func(d string) bool { return covers(d, rel) }):
		return fmt.Errorf("%s is a deny path of %s, and only whoever forked the job lifts one", rel, row.ID)
	}
	ancestors := types.JobAncestors(rows, row.ID)
	for _, other := range rows {
		if other.ID == row.ID || !Editing(other) || slices.ContainsFunc(ancestors, func(a types.Job) bool { return a.ID == other.ID }) {
			continue
		}
		if slices.ContainsFunc(other.WritePaths, func(w string) bool { return covers(w, rel) }) {
			return fmt.Errorf("[%s] %s is held by live job %s, and a proof never takes a path another job holds", string(types.WritePathFileShared), rel, other.ID)
		}
	}
	return nil
}

// ProposeWidening is the first proof of rel for row the facts hold, tried refs, test,
// output, then released, each kind's subjects in sorted order, and false when rel is no
// path a proof may add or none proves it.
func ProposeWidening(row types.Job, rel string, facts wideningFacts, rows []types.Job) (types.JobWidenRequest, string, bool) {
	rel = path.Clean(rel)
	if checkWideningPath(row, rel, rows) != nil {
		return types.JobWidenRequest{}, "", false
	}
	var tries []types.WideningEvidence
	asked := facts.files[rel]
	for _, use := range slices.Sorted(maps.Keys(asked.uses)) {
		if strings.Contains(use, ".") || facts.packages[path.Dir(rel)].decls[use].file != "" {
			tries = append(tries, types.WideningEvidence{Kind: types.WideningRefs, Subject: use})
		}
	}
	for _, f := range changedInside(row, facts.seen) {
		tries = append(tries, types.WideningEvidence{Kind: types.WideningTest, Subject: f})
	}
	for _, o := range facts.regenerated {
		tries = append(tries, types.WideningEvidence{Kind: types.WideningOutput, Subject: o.Target})
	}
	tries = append(tries, types.WideningEvidence{Kind: types.WideningReleased})
	for _, e := range tries {
		if proof, err := proveWidening(row, rel, e, facts); err == nil {
			return types.JobWidenRequest{Path: rel, Because: e.String()}, proof, true
		}
	}
	return types.JobWidenRequest{}, "", false
}

// WideningCommand is the one `magus job apply` line that widens job id by reqs, and false
// when it would run past [maxWideningCommand].
func WideningCommand(id string, reqs []types.JobWidenRequest) (string, bool) {
	record, err := json.Marshal(types.Declaration{Schema: types.Schema{Version: types.JobSchemaVersion}, ID: id, Widen: reqs})
	if err != nil {
		return "", false
	}
	line := hint.JobApply.With("-f", "-") + " <<<" + shellQuote(string(record))
	return line, len(line) <= maxWideningCommand
}

// Widen adds reqs to job id's write paths, each proved from facts against the row the
// store holds under its lock, and records each proof on the row. Nothing is written
// unless every path is proved: see [authorizeWidening] for who may widen and what no
// proof adds.
// dryRun proves and authorizes against the store as read, and writes nothing.
func (s *Store) Widen(ctx context.Context, id string, reqs []types.JobWidenRequest, facts wideningFacts, limit wideningLimit, dryRun bool) (types.Job, error) {
	actor := s.Actor()
	if err := holderWidens(actor, id); err != nil {
		return types.Job{}, err
	}
	if len(reqs) == 0 {
		return types.Job{}, widenRefusal(actor, id, "the record widens nothing")
	}
	by := trail.StampOrigin(ctx, types.Origin{})
	if !dryRun {
		return s.mutate(ctx, id, asWidening, func(cur *types.Job, exists bool, now int64) error {
			if !exists {
				return widenRefusal(actor, id, "nothing declares that job")
			}
			return grantWidening(actor, cur, reqs, facts, limit, now, by)
		})
	}
	rows, err := s.List()
	if err != nil {
		return types.Job{}, err
	}
	i := slices.IndexFunc(rows, func(r types.Job) bool { return r.ID == id })
	if i < 0 {
		return types.Job{}, widenRefusal(actor, id, "nothing declares that job")
	}
	next := rows[i].Clone()
	if err := grantWidening(actor, &next, reqs, facts, limit, time.Now().Unix(), by); err != nil {
		return types.Job{}, err
	}
	return next, authorizeRow(actor, id, asWidening, rows[i], next, true, rows)
}

// grantWidening proves each of reqs against cur and appends the path and its proof, or
// refuses the first that is past limit, outside the checkout, held or denied by cur, or
// unproved. Another job's hold is authorizeWidening's, which reads the rows.
func grantWidening(actor Actor, cur *types.Job, reqs []types.JobWidenRequest, facts wideningFacts, limit wideningLimit, now int64, by types.Origin) error {
	if len(cur.Widened)+len(reqs) > limit.Max {
		return widenRefusal(actor, cur.ID, fmt.Sprintf("%s holds %d proved widening(s) and this adds %d, past jobs.max_proved_widenings, %d (%s)",
			cur.ID, len(cur.Widened), len(reqs), limit.Max, limit.Source))
	}
	for _, req := range reqs {
		e, err := types.ParseWideningEvidence(req.Because)
		if err != nil {
			return widenRefusal(actor, cur.ID, err.Error())
		}
		rel := path.Clean(strings.TrimSpace(req.Path))
		if filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, "../") {
			return widenRefusal(actor, cur.ID, rel+" is outside the job's checkout")
		}
		if err := checkWideningPath(*cur, rel, nil); err != nil {
			return widenRefusal(actor, cur.ID, err.Error())
		}
		proof, err := proveWidening(*cur, rel, e, facts)
		if err != nil {
			return widenRefusal(actor, cur.ID, err.Error())
		}
		cur.WritePaths = append(cur.WritePaths, rel)
		cur.Widened = append(cur.Widened, types.JobWidening{Path: rel, Evidence: e, Proof: proof, At: now, By: by})
	}
	return nil
}

// authorizeWidening grades a proved widening: the holder's own running or exited job, its
// write paths extended by exactly the paths a proof was recorded for, and none of them a
// path [checkWideningPath] refuses. An unbound actor is held to the same paths.
func authorizeWidening(actor Actor, id string, prev, next types.Job, exists bool, rows []types.Job) error {
	if err := holderWidens(actor, id); err != nil {
		return err
	}
	switch {
	case !exists:
		return widenRefusal(actor, id, "nothing declares that job")
	case prev.State != types.StateRunning && prev.State != types.StateExited:
		return widenRefusal(actor, id, fmt.Sprintf("%s is %s, and only a running or exited job widens", id, cmp.Or(string(prev.State), "unstated")))
	}
	if fields := changedFields(prev, next); !slices.Equal(fields, []string{"write_paths"}) {
		return widenRefusal(actor, id, "a widening adds write paths and changes nothing else, and this one changes "+strings.Join(fields, ", "))
	}
	n, w := len(prev.WritePaths), len(prev.Widened)
	if len(next.WritePaths) < n || !slices.Equal(next.WritePaths[:n], prev.WritePaths) ||
		len(next.Widened) < w || !slices.Equal(next.Widened[:w], prev.Widened) || len(next.Widened)-w != len(next.WritePaths)-n {
		return widenRefusal(actor, id, "a widening appends one proved write path per proof and drops none")
	}
	for k, p := range next.WritePaths[n:] {
		if next.Widened[w+k].Path != p {
			return widenRefusal(actor, id, p+" carries no proof")
		}
		if err := checkWideningPath(prev, p, rows); err != nil {
			return widenRefusal(actor, id, err.Error())
		}
	}
	return nil
}

// holderWidens refuses a bound actor widening any job but the one it holds.
func holderWidens(actor Actor, id string) error {
	switch {
	case actor.Unstamped:
		return refuse(actor, id, "a caller magus cannot name widens no job")
	case actor.Bound() && id != actor.Lease:
		return refuse(actor, id, "a holder widens only its own job, never a child's, a sibling's or an ancestor's")
	}
	return nil
}

func widenRefusal(actor Actor, id, rule string) error {
	if actor.Bound() {
		return &RefusedError{Lease: id, Actor: actor, Rule: rule, Remedy: "ask whoever forked the job to widen it"}
	}
	return fmt.Errorf("job: %s: %s", id, rule)
}
