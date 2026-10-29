package workspace

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/bmatcuk/doublestar/v4"

	"github.com/egladman/magus/internal/spell"
	"github.com/egladman/magus/project"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
)

// magusfileName is the file a discovered project's source is written to, in its directory.
const magusfileName = "magusfile.buzz"

// discoveryInput is what [collectDiscovery] read, passed whole to [mergeDiscovery] so the
// walk's notes reach the report.
type discoveryInput struct {
	// Queries holds one query per asked spell, keyed by spell name.
	Queries map[string]spells.DiscoveryQuery
	// Notes are the walk's rule-resolved choices: directories pruned for some spells
	// and not others.
	Notes []discoveryFinding
}

// spellAnswer is one spell's decoded discover_projects answer.
type spellAnswer struct {
	Spell  spells.Descriptor
	Claims []spells.DiscoveredProject
}

// discoveryResult is the merged projection: one project per claimed directory, and the report.
type discoveryResult struct {
	// Projects is sorted by path. A directory that already has a magusfile is never one.
	Projects []discoveryProject
	// Notes are disagreements a rule resolved; each names the line that overrides it.
	Notes []discoveryFinding
	// Gaps are what discovery could not decide or wire; each names the setting that does.
	Gaps []discoveryFinding
}

// discoveryProject is one directory's merged claims and the magusfile they render to.
type discoveryProject struct {
	Path string
	// Name is empty when the path names the project, including when spells disagree.
	Name string
	// Spells is every spell bound here, sorted by name.
	Spells []string
	// DependsOn holds workspace-relative project paths, sorted.
	DependsOn []string
	// Targets is in phase order ([spells.DiscoveryTargets]), then ci when composed.
	Targets []discoveryTarget
	// Source is the rendered magusfile.buzz.
	Source string
}

// Magusfile is the workspace-relative path Source is written to.
func (p discoveryProject) Magusfile() string { return path.Join(p.Path, magusfileName) }

// discoveryTarget is one lifecycle target. Steps run in order; ci has none and needs
// Needs instead.
type discoveryTarget struct {
	Name  string
	Steps []discoveryStep
	Needs []string
}

// discoveryStep is one spell op a target calls.
type discoveryStep struct {
	Spell string
	Op    string
}

// discoveryFinding is one report line.
type discoveryFinding struct {
	// Path is the file or directory the finding is about.
	Path   string
	What   string
	Why    string
	Settle string
}

// String is the report line: `<path>: <what> (<why>); settle: <settle>`.
func (f discoveryFinding) String() string {
	return fmt.Sprintf("%s: %s (%s); settle: %s", f.Path, f.What, f.Why, f.Settle)
}

// collectDiscovery walks root once and builds each asked spell's query: every file a
// spell's required globs match, with markers' content read. It prunes dot directories,
// the fixed ignore set, secondary checkouts and the union of every asked spell's ignore
// dirs; a pruned directory that some asked spells ignore and others do not is noted.
//
// A glob is matched against the path from every directory down, because each directory
// is a candidate project root: "Cargo.toml" and "**/Cargo.toml" both match a/Cargo.toml.
// Content is read, at any depth, for a glob whose basename has no wildcard.
func collectDiscovery(root string, asked []spells.Descriptor) (discoveryInput, error) {
	ignoredBy := ignoringSpells(asked)
	files := map[string][]spells.DiscoveryFile{}
	pruned := map[string][]string{}
	err := filepath.WalkDir(root, func(abs string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel := filepath.ToSlash(strings.TrimPrefix(strings.TrimPrefix(abs, root), string(filepath.Separator)))
		if d.IsDir() {
			if abs == root {
				return nil
			}
			name := d.Name()
			if by := ignoredBy[name]; len(by) > 0 {
				if len(by) < len(asked) {
					pruned[name] = append(pruned[name], rel)
				}
				return fs.SkipDir
			}
			if project.SkipsWalkDir(name, abs) {
				return fs.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		for _, s := range asked {
			f, ok, err := collectFile(abs, rel, s.Needs)
			if err != nil {
				return err
			}
			if ok {
				files[s.Name] = append(files[s.Name], f)
			}
		}
		return nil
	})
	if err != nil {
		return discoveryInput{}, fmt.Errorf("discovery: walk %s: %w", root, err)
	}
	in := discoveryInput{Queries: map[string]spells.DiscoveryQuery{}}
	for _, s := range asked {
		got := files[s.Name]
		slices.SortFunc(got, func(a, b spells.DiscoveryFile) int { return strings.Compare(a.Path, b.Path) })
		in.Queries[s.Name] = spells.DiscoveryQuery{Root: root, Files: got}
	}
	in.Notes = prunedNotes(pruned, ignoredBy, asked)
	return in, nil
}

// ignoringSpells is, per directory name, the asked spells that ignore it.
func ignoringSpells(asked []spells.Descriptor) map[string][]string {
	ignoredBy := map[string][]string{}
	for _, s := range asked {
		for _, dir := range s.IgnoreDirs {
			ignoredBy[dir] = append(ignoredBy[dir], s.Name)
		}
	}
	return ignoredBy
}

// prunedNotes is a finding per directory name some asked spells ignore and others do not,
// naming where it was pruned.
func prunedNotes(pruned, ignoredBy map[string][]string, asked []spells.Descriptor) []discoveryFinding {
	var notes []discoveryFinding
	for _, name := range slices.Sorted(maps.Keys(pruned)) {
		by := ignoredBy[name]
		var others []string
		for _, s := range asked {
			if !slices.Contains(by, s.Name) {
				others = append(others, s.Name)
			}
		}
		notes = append(notes, discoveryFinding{
			Path:   strings.Join(pruned[name], ", "),
			What:   "not collected",
			Why:    fmt.Sprintf("spell %s ignores %s, so %s never saw a claim inside", strings.Join(by, ", "), name, strings.Join(others, ", ")),
			Settle: "a magusfile in that directory; a magusfile wins",
		})
	}
	return notes
}

func collectFile(abs, rel string, globs []string) (spells.DiscoveryFile, bool, error) {
	var matched, marker bool
	for _, g := range globs {
		if strings.HasPrefix(g, "!") {
			continue
		}
		if matchFromAnyDir(g, rel) {
			matched = true
			marker = marker || !hasGlobMeta(path.Base(g))
		}
	}
	for _, g := range globs {
		if rest, ok := strings.CutPrefix(g, "!"); ok && matchFromAnyDir(rest, rel) {
			matched = false
		}
	}
	if !matched {
		return spells.DiscoveryFile{}, false, nil
	}
	f := spells.DiscoveryFile{Path: rel}
	if marker {
		b, err := os.ReadFile(abs)
		if err != nil {
			return spells.DiscoveryFile{}, false, fmt.Errorf("discovery: read marker %s: %w", rel, err)
		}
		content := string(b)
		f.Content = &content
	}
	return f, true, nil
}

func matchFromAnyDir(glob, rel string) bool {
	for suffix := rel; ; {
		if doublestar.MatchUnvalidated(glob, suffix) {
			return true
		}
		_, rest, found := strings.Cut(suffix, "/")
		if !found {
			return false
		}
		suffix = rest
	}
}

func hasGlobMeta(s string) bool { return strings.ContainsAny(s, `*?[{\`) }

// mergeDiscovery validates each answer against its spell, merges the claims per
// directory, and renders each project's magusfile. magusfileDirs are the directories
// that already hold one: a magusfile wins, so claims there are dropped and noted.
//
// A contract violation (a claim [spell.ValidateDiscovered] refuses, an answer from a
// spell that was not asked, a value the renderer cannot write as a Buzz string) fails
// the merge. Every disagreement between spells is reported, as a note when a rule
// resolved it and as a gap when nothing could.
func mergeDiscovery(in discoveryInput, answers []spellAnswer, magusfileDirs []string) (discoveryResult, error) {
	m := merger{
		formed:     map[string]*mergedProject{},
		magusfiles: map[string][]string{},
		notes:      slices.Clone(in.Notes),
	}
	for _, dir := range magusfileDirs {
		m.magusfiles[dir] = nil
	}
	sorted := slices.SortedFunc(slices.Values(answers), func(a, b spellAnswer) int { return strings.Compare(a.Spell.Name, b.Spell.Name) })
	for _, a := range sorted {
		if _, ok := in.Queries[a.Spell.Name]; !ok {
			return discoveryResult{}, fmt.Errorf("discovery: spell %q answered, but it was not asked", a.Spell.Name)
		}
		if err := spell.ValidateDiscovered(a.Spell, a.Claims); err != nil {
			return discoveryResult{}, err
		}
	}
	m.form(sorted)
	m.attachMembers(sorted)
	m.noteMagusfileWins()
	m.resolveNames()
	m.resolveEdges()
	return m.render()
}

type mergedProject struct {
	path    string
	spells  []string
	names   map[string]string
	deps    []string
	spans   []span
	targets map[string][]discoveryStep
	gaps    []spells.DiscoveryGap
	gapBy   []string
	name    string
}

type span struct {
	spell, dir string
}

type merger struct {
	formed map[string]*mergedProject
	// magusfiles maps each directory holding a magusfile to the spells that claimed it.
	magusfiles map[string][]string
	notes      []discoveryFinding
	gaps       []discoveryFinding
}

func (m *merger) form(answers []spellAnswer) {
	for _, a := range answers {
		for _, c := range a.Claims {
			if c.Role != spells.DiscoveryRoleProject {
				continue
			}
			if _, ok := m.magusfiles[c.Path]; ok {
				m.magusfiles[c.Path] = append(m.magusfiles[c.Path], a.Spell.Name)
				continue
			}
			p := m.formed[c.Path]
			if p == nil {
				p = &mergedProject{path: c.Path, names: map[string]string{}, targets: map[string][]discoveryStep{}}
				m.formed[c.Path] = p
			}
			p.add(a.Spell.Name, c)
		}
	}
}

func (m *merger) attachMembers(answers []spellAnswer) {
	var orphans []string
	for _, a := range answers {
		for _, c := range a.Claims {
			if c.Role != spells.DiscoveryRoleMember {
				continue
			}
			owner, ok := m.owner(c.Path)
			if !ok {
				owner = "."
				m.formed["."] = &mergedProject{path: ".", names: map[string]string{}, targets: map[string][]discoveryStep{}}
				orphans = append(orphans, a.Spell.Name+" at "+c.Path)
			}
			if _, isMagusfile := m.magusfiles[owner]; isMagusfile {
				m.magusfiles[owner] = append(m.magusfiles[owner], a.Spell.Name)
				continue
			}
			m.formed[owner].add(a.Spell.Name, c)
		}
	}
	if len(orphans) > 0 {
		m.notes = append(m.notes, discoveryFinding{
			Path:   ".",
			What:   "root project formed",
			Why:    "no project owns the member claims of " + strings.Join(orphans, ", "),
			Settle: "a magusfile in the directory that should own them",
		})
	}
}

// owner is the longest-prefix project, formed or with a magusfile, containing dir.
func (m *merger) owner(dir string) (string, bool) {
	for d := dir; ; d = path.Dir(d) {
		if _, ok := m.formed[d]; ok {
			return d, true
		}
		if _, ok := m.magusfiles[d]; ok {
			return d, true
		}
		if d == "." {
			return "", false
		}
	}
}

func (p *mergedProject) add(spellName string, c spells.DiscoveredProject) {
	if !slices.Contains(p.spells, spellName) {
		p.spells = append(p.spells, spellName)
	}
	if c.Name != "" {
		p.names[spellName] = c.Name
	}
	p.deps = append(p.deps, c.DependsOn...)
	for _, dir := range c.Spans {
		p.spans = append(p.spans, span{spell: spellName, dir: dir})
	}
	for name, ops := range c.Targets {
		for _, op := range ops {
			step := discoveryStep{Spell: spellName, Op: types.Normalize(op)}
			if !slices.Contains(p.targets[name], step) {
				p.targets[name] = append(p.targets[name], step)
			}
		}
	}
	for _, g := range c.NotInferred {
		p.gaps = append(p.gaps, g)
		p.gapBy = append(p.gapBy, spellName)
	}
}

func (m *merger) noteMagusfileWins() {
	for _, dir := range slices.Sorted(maps.Keys(m.magusfiles)) {
		claimed := slices.Compact(slices.Sorted(slices.Values(m.magusfiles[dir])))
		if len(claimed) == 0 {
			continue
		}
		m.notes = append(m.notes, discoveryFinding{
			Path:   path.Join(dir, magusfileName),
			What:   "discovered claims not rendered",
			Why:    "spell " + strings.Join(claimed, ", ") + " claimed this directory, and its magusfile wins",
			Settle: "bind them there: magus\\project({\"spells\": [" + strings.Join(claimed, ", ") + "]})",
		})
	}
}

func (m *merger) resolveNames() {
	for _, p := range m.sortedProjects() {
		distinct := slices.Compact(slices.Sorted(maps.Values(p.names)))
		if len(distinct) == 1 {
			p.name = distinct[0]
		}
		if len(distinct) > 1 {
			var said []string
			for _, s := range slices.Sorted(maps.Keys(p.names)) {
				said = append(said, fmt.Sprintf("%s says %q", s, p.names[s]))
			}
			m.notes = append(m.notes, discoveryFinding{
				Path:   p.path,
				What:   "named by its path",
				Why:    "spells disagree on the name: " + strings.Join(said, ", "),
				Settle: fmt.Sprintf("magus\\project({\"name\": ...}) in %s", path.Join(p.path, magusfileName)),
			})
		}
	}
}

// resolveEdges keeps each declared dependency on a known project and turns each span
// over a nested project into one. Declared edges go first, so a span never displaces
// one; an edge that would close a cycle is dropped and reported.
func (m *merger) resolveEdges() {
	edges := map[string][]string{}
	for _, p := range m.sortedProjects() {
		for _, dep := range slices.Compact(slices.Sorted(slices.Values(p.deps))) {
			switch {
			case dep == p.path:
				m.gaps = append(m.gaps, m.edgeGap(p.path, dep, "a claim names its own project"))
			case !m.isProject(dep):
				m.gaps = append(m.gaps, m.edgeGap(p.path, dep, "no project is there"))
			case reaches(edges, dep, p.path):
				m.gaps = append(m.gaps, m.edgeGap(p.path, dep, dep+" already reaches "+p.path+", so the edge would close a cycle"))
			default:
				edges[p.path] = append(edges[p.path], dep)
			}
		}
	}
	for _, p := range m.sortedProjects() {
		for _, s := range p.spans {
			for _, nested := range m.projectsUnder(s.dir) {
				if nested == p.path || slices.Contains(edges[p.path], nested) {
					continue
				}
				why := fmt.Sprintf("spell %s spans %s, which %s claims", s.spell, s.dir, nested)
				if reaches(edges, nested, p.path) {
					m.gaps = append(m.gaps, m.edgeGap(p.path, nested, why+", but "+nested+" already reaches "+p.path+", so the edge would close a cycle"))
					continue
				}
				edges[p.path] = append(edges[p.path], nested)
				m.notes = append(m.notes, discoveryFinding{
					Path:   p.path,
					What:   "depends on " + nested,
					Why:    why,
					Settle: "depends_on in " + path.Join(p.path, magusfileName),
				})
			}
		}
	}
	for _, p := range m.formed {
		p.deps = slices.Sorted(slices.Values(edges[p.path]))
	}
}

func (m *merger) edgeGap(from, to, why string) discoveryFinding {
	return discoveryFinding{
		Path:   from,
		What:   "no depends_on " + to,
		Why:    why,
		Settle: "an explicit depends_on in " + path.Join(from, magusfileName),
	}
}

func (m *merger) isProject(dir string) bool {
	_, formed := m.formed[dir]
	_, magusfile := m.magusfiles[dir]
	return formed || magusfile
}

func (m *merger) projectsUnder(dir string) []string {
	all := slices.Concat(slices.Collect(maps.Keys(m.formed)), slices.Collect(maps.Keys(m.magusfiles)))
	slices.Sort(all)
	var out []string
	for _, p := range all {
		if p == dir || strings.HasPrefix(p, dir+"/") {
			out = append(out, p)
		}
	}
	return out
}

func reaches(edges map[string][]string, from, to string) bool {
	seen := map[string]bool{from: true}
	todo := []string{from}
	for len(todo) > 0 {
		cur := todo[len(todo)-1]
		todo = todo[:len(todo)-1]
		if cur == to {
			return true
		}
		for _, next := range edges[cur] {
			if !seen[next] {
				seen[next] = true
				todo = append(todo, next)
			}
		}
	}
	return false
}

func (m *merger) sortedProjects() []*mergedProject {
	out := make([]*mergedProject, 0, len(m.formed))
	for _, k := range slices.Sorted(maps.Keys(m.formed)) {
		out = append(out, m.formed[k])
	}
	return out
}

// ciNeeds is the starter magusfile's ci: lint, build and test, whichever exist.
var ciNeeds = []string{"lint", "build", "test"}

func (m *merger) render() (discoveryResult, error) {
	d := discoveryResult{}
	for _, p := range m.sortedProjects() {
		slices.Sort(p.spells)
		dp := discoveryProject{Path: p.path, Name: p.name, Spells: p.spells, DependsOn: p.deps}
		var needs []string
		for _, name := range spells.DiscoveryTargets() {
			steps := p.targets[name]
			if len(steps) == 0 {
				continue
			}
			slices.SortStableFunc(steps, func(a, b discoveryStep) int { return strings.Compare(a.Spell, b.Spell) })
			dp.Targets = append(dp.Targets, discoveryTarget{Name: name, Steps: steps})
			if slices.Contains(ciNeeds, name) {
				needs = append(needs, name)
			}
		}
		if len(needs) > 0 {
			dp.Targets = append(dp.Targets, discoveryTarget{Name: types.TargetCI, Needs: needs})
		}
		source, lines, err := renderMagusfile(dp)
		if err != nil {
			return discoveryResult{}, err
		}
		dp.Source = source
		d.Projects = append(d.Projects, dp)
		m.reportProject(dp, p, lines)
	}
	d.Notes = m.notes
	d.Gaps = m.gaps
	return d, nil
}

func (m *merger) reportProject(dp discoveryProject, p *mergedProject, lines map[string]int) {
	file := dp.Magusfile()
	for _, t := range dp.Targets {
		if order := spellOrder(t.Steps); len(order) > 1 {
			m.notes = append(m.notes, discoveryFinding{
				Path:   dp.Path,
				What:   t.Name + " runs " + strings.Join(order, " then "),
				Why:    "several spells contribute " + t.Name + ", and they run in spell-name order",
				Settle: fmt.Sprintf("reorder the calls at %s:%d", file, lines[t.Name]),
			})
		}
	}
	switch {
	case len(dp.Targets) == 0:
		m.gaps = append(m.gaps, discoveryFinding{
			Path:   dp.Path,
			What:   "no lifecycle target",
			Why:    "spell " + strings.Join(dp.Spells, ", ") + " formed the project and inferred none",
			Settle: "write the targets in " + file,
		})
	case !slices.ContainsFunc(dp.Targets, func(t discoveryTarget) bool { return t.Name == types.TargetCI }):
		m.gaps = append(m.gaps, discoveryFinding{
			Path:   dp.Path,
			What:   "no ci",
			Why:    "none of " + strings.Join(ciNeeds, ", ") + " was inferred",
			Settle: "write ci in " + file,
		})
	}
	for i, g := range p.gaps {
		m.gaps = append(m.gaps, discoveryFinding{
			Path:   g.File,
			What:   g.Why,
			Why:    "spell " + p.gapBy[i] + " saw it and could not infer a target",
			Settle: "a target in " + file,
		})
	}
}

func spellOrder(steps []discoveryStep) []string {
	var out []string
	for _, s := range steps {
		if !slices.Contains(out, s.Spell) {
			out = append(out, s.Spell)
		}
	}
	return out
}

var (
	buzzIdentifier    = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	notIdentifierRune = regexp.MustCompile(`[^A-Za-z0-9_]`)
)

// renderMagusfile writes p as Buzz source and returns the 1-based line each target's
// function starts on.
func renderMagusfile(p discoveryProject) (string, map[string]int, error) {
	var b strings.Builder
	line := 1
	emit := func(format string, args ...any) {
		s := fmt.Sprintf(format, args...)
		b.WriteString(s)
		b.WriteByte('\n')
		line += strings.Count(s, "\n") + 1
	}
	handles := spellHandles(p)

	emit(`import "magus";`)
	emit("")
	for _, imp := range spellImports(p.Spells, handles) {
		emit("%s", imp)
	}
	emit("")
	emit("magus\\project({")
	emit(`    "spells": [%s],`, strings.Join(mapNames(p.Spells, handles), ", "))
	if p.Name != "" {
		name, err := buzzString(p.Name)
		if err != nil {
			return "", nil, fmt.Errorf("discovery: %s: name: %w", p.Path, err)
		}
		emit(`    "name": %s,`, name)
	}
	if len(p.DependsOn) > 0 {
		deps, err := dependsOnLiterals(p)
		if err != nil {
			return "", nil, err
		}
		emit(`    "depends_on": [%s],`, strings.Join(deps, ", "))
	}
	emit("});")

	lines := map[string]int{}
	for _, t := range p.Targets {
		emit("")
		lines[t.Name] = line
		emit("export fun %s(ctx: magus\\Context, args: [str]) > void {", t.Name)
		for _, s := range t.Steps {
			op, err := buzzString(s.Op)
			if err != nil {
				return "", nil, fmt.Errorf("discovery: %s: %s: op: %w", p.Path, t.Name, err)
			}
			emit("    %s[%s](ctx);", handles[s.Spell], op)
		}
		if len(t.Needs) > 0 {
			emit("    ctx.needs(%s);", strings.Join(t.Needs, ", "))
		}
		emit("}")
	}
	return b.String(), lines, nil
}

// spellImports is the import line of each spell, under its handle when that is not its name.
func spellImports(names []string, handles map[string]string) []string {
	out := make([]string, 0, len(names))
	for _, name := range names {
		if handles[name] == name {
			out = append(out, fmt.Sprintf(`import "%s";`, spells.ModulePath(name)))
		} else {
			out = append(out, fmt.Sprintf(`import "%s" as %s;`, spells.ModulePath(name), handles[name]))
		}
	}
	return out
}

// dependsOnLiterals is each of p's dependencies as a Buzz string literal of its path.
func dependsOnLiterals(p discoveryProject) ([]string, error) {
	deps := make([]string, 0, len(p.DependsOn))
	for _, dep := range p.DependsOn {
		lit, err := buzzString(dependsOnPath(p.Path, dep))
		if err != nil {
			return nil, fmt.Errorf("discovery: %s: depends_on: %w", p.Path, err)
		}
		deps = append(deps, lit)
	}
	return deps, nil
}

// spellHandles names each spell's import binding. A spell name that is not an
// identifier, or that a target function or the magus module would shadow, is aliased.
func spellHandles(p discoveryProject) map[string]string {
	taken := map[string]bool{"magus": true, "ctx": true, "args": true}
	for _, t := range p.Targets {
		taken[t.Name] = true
	}
	out := map[string]string{}
	for _, name := range p.Spells {
		handle := name
		if !buzzIdentifier.MatchString(handle) || taken[handle] {
			handle = notIdentifierRune.ReplaceAllString(name, "_") + "_spell"
		}
		for taken[handle] {
			handle += "_"
		}
		taken[handle] = true
		out[name] = handle
	}
	return out
}

func mapNames(names []string, handles map[string]string) []string {
	out := make([]string, 0, len(names))
	for _, n := range names {
		out = append(out, handles[n])
	}
	return out
}

// dependsOnPath spells a dependency the way hand-written magusfiles do: repo-relative
// from the root, dot-relative from a nested project, where a bare path would read as
// repo-relative but "." would read as the project itself.
func dependsOnPath(from, dep string) string {
	if from == "." {
		return dep
	}
	up := strings.Repeat("../", strings.Count(from, "/")+1)
	if dep == "." {
		return strings.TrimSuffix(up, "/")
	}
	return up + dep
}

var errNotBuzzString = errors.New("holds a quote, a backslash, a brace or a control character, which a Buzz string literal would interpret")

// buzzString quotes s, refusing rather than escaping: braces interpolate in Buzz
// strings, and no claim value has a reason to hold one.
func buzzString(s string) (string, error) {
	if strings.ContainsFunc(s, func(r rune) bool { return r < 0x20 || r == 0x7f || strings.ContainsRune(`"\{}`, r) }) {
		return "", fmt.Errorf("%q %w", s, errNotBuzzString)
	}
	return `"` + s + `"`, nil
}
