package knowledge

import (
	"cmp"
	"fmt"
	"maps"
	"path"
	"slices"
	"strings"
	"unicode"

	"github.com/egladman/magus/types"
)

// The layout lens reports how a directory a change creates sits against the directories beside
// it: how many files it holds, whether one of them is a test, and whether its name carries a
// digit. Like the conformance lens, the norm is counted from the tree, the change's own new
// directories never count toward it, and a finding is silent unless the siblings clear the same
// cohort and share gates. A directory that already existed is never a subject: its shape was
// decided before this change, and reporting it would be a backlog rather than advice.

// LayoutChange describes the change [Graph.Layout] observes.
type LayoutChange struct {
	// Added holds the workspace-relative paths the change adds. A directory is new when every
	// file the graph places under it is one of them.
	Added map[string]bool
	// Generated holds the workspace-relative paths that are generated output. A generated file
	// is neither counted nor able to make a directory old.
	Generated map[string]bool
	// MinCohort and MinShare are the silence gates, as in [ConformanceChange]. Zero takes the
	// defaults, 5 and 0.8.
	MinCohort int
	MinShare  float64
}

// layoutLanguage is what the lens knows about one language: the name a finding uses for it and
// how its test files are named. A language missing from layoutLanguages holds no source for
// this lens, so a directory of it is never a subject and never a sibling.
type layoutLanguage struct {
	label    string
	suffixes []string
	prefixes []string
}

func (l layoutLanguage) isTest(base string) bool {
	for _, s := range l.suffixes {
		if strings.HasSuffix(base, s) {
			return true
		}
	}
	for _, p := range l.prefixes {
		if strings.HasPrefix(base, p) {
			return true
		}
	}
	return false
}

// layoutLanguages is keyed by languageFromPath's token, so a directory's language is read the
// way the dir_languages attr reads it.
var layoutLanguages = map[string]layoutLanguage{
	"go":         {label: "Go", suffixes: []string{"_test.go"}},
	"typescript": {label: "TypeScript", suffixes: []string{".test.ts", ".test.tsx", ".spec.ts", ".spec.tsx"}},
	"javascript": {label: "JavaScript", suffixes: []string{".test.js", ".test.jsx", ".spec.js", ".spec.jsx", ".test.mjs"}},
	"python":     {label: "Python", suffixes: []string{"_test.py"}, prefixes: []string{"test_"}},
}

// dirShape is one directory's own files, not its subdirectories'.
type dirShape struct {
	dir, language string
	files, tests  int
}

func (d dirShape) base() string { return path.Base(d.dir) }

func (d dirShape) digit() bool { return strings.IndexFunc(d.base(), unicode.IsDigit) >= 0 }

// Layout runs the package-files, package-tests and package-name checks over every directory
// the change creates, keyed by that directory. A directory is a subject when it directly holds
// source of exactly one language the lens knows; its siblings are the directories of that
// language under the same parent that the change did not create.
//
// Each finding is a [types.Check] with Status advice and Evidence measured: counts of files the
// graph holds, never a rule. At most conformanceMaxFindings are returned across the change,
// ordered by directory and check so two runs over one graph agree byte for byte.
func (g *Graph) Layout(c LayoutChange) map[string][]types.Check {
	if len(c.Added) == 0 {
		return nil
	}
	cohort, share := cmp.Or(c.MinCohort, conformanceMinCohort), cmp.Or(c.MinShare, conformanceMinShare)
	skip := func(file string) bool {
		if c.Generated[file] {
			return true
		}
		for _, seg := range strings.Split(path.Dir(file), "/") {
			if seg == "testdata" || seg == "vendor" || seg == "node_modules" {
				return true
			}
		}
		return false
	}

	old := map[string]bool{}
	direct := map[string][]string{}
	for _, n := range g.nodes {
		if n.Kind != types.KindFile && n.Kind != types.KindDoc {
			continue
		}
		file, _, _ := strings.Cut(n.Source, ":")
		if file == "" || skip(file) {
			continue
		}
		dir := path.Dir(file)
		if !slices.Contains(direct[dir], file) {
			direct[dir] = append(direct[dir], file)
		}
		if c.Added[file] {
			continue
		}
		for d := dir; d != "." && d != "/" && !old[d]; d = path.Dir(d) {
			old[d] = true
		}
	}

	shapes := map[string]dirShape{}
	for dir, files := range direct {
		if dir == "." {
			continue
		}
		s := dirShape{dir: dir}
		for _, f := range files {
			lang := languageFromPath(f)
			l, ok := layoutLanguages[lang]
			if !ok {
				continue
			}
			if s.language != "" && s.language != lang {
				s.language = ""
				break
			}
			s.language = lang
			if l.isTest(path.Base(f)) {
				s.tests++
			} else {
				s.files++
			}
		}
		if s.language != "" && s.files > 0 {
			shapes[dir] = s
		}
	}

	var found []layoutFinding
	for _, dir := range slices.Sorted(maps.Keys(shapes)) {
		subj := shapes[dir]
		if old[dir] {
			continue
		}
		var siblings []dirShape
		for _, s := range shapes {
			if old[s.dir] && s.language == subj.language && path.Dir(s.dir) == path.Dir(dir) {
				siblings = append(siblings, s)
			}
		}
		if len(siblings) < cohort {
			continue
		}
		slices.SortFunc(siblings, func(a, b dirShape) int { return cmp.Compare(a.dir, b.dir) })
		importers := g.importingDirs(dir, direct[dir])
		for _, chk := range layoutChecks {
			if !chk.breaks(subj) {
				continue
			}
			var kept, outliers []dirShape
			for _, s := range siblings {
				if chk.breaks(s) {
					outliers = append(outliers, s)
				} else {
					kept = append(kept, s)
				}
			}
			if float64(len(kept)) < share*float64(len(siblings)) {
				continue
			}
			lang := layoutLanguages[subj.language].label
			check := types.Check{
				Name: chk.name, Status: types.CheckAdvice, Evidence: types.EvidenceMeasured,
				Message: fmt.Sprintf("`%s`: %s, %s and %s; %d of %d %s directories beside it %s, as %s do",
					dir, count(subj.files, lang+" file"), count(subj.tests, "test file"),
					count(importers, "importing directory"), len(kept), len(siblings), lang, chk.norm,
					strings.Join(nearest(kept, subj.base()), " and ")),
			}
			if len(outliers) > 0 {
				names := make([]string, len(outliers))
				for i, o := range outliers {
					names[i] = o.dir
				}
				check.Details = []string{"not: " + strings.Join(capList(names, conformanceExamples), ", ")}
			}
			found = append(found, layoutFinding{dir: dir, check: check})
		}
	}
	if len(found) == 0 {
		return nil
	}
	out := map[string][]types.Check{}
	for _, f := range found[:min(len(found), conformanceMaxFindings)] {
		out[f.dir] = append(out[f.dir], f.check)
	}
	return out
}

type layoutFinding struct {
	dir   string
	check types.Check
}

// layoutCheck is one shape a directory can break. breaks is read over the subject and over each
// sibling alike, so the norm and the finding cannot disagree about what the shape is.
type layoutCheck struct {
	name, norm string
	breaks     func(dirShape) bool
}

var layoutChecks = []layoutCheck{
	{name: types.CheckPackageFiles, norm: "hold more than one", breaks: func(d dirShape) bool { return d.files == 1 }},
	{name: types.CheckPackageTests, norm: "hold a test file", breaks: func(d dirShape) bool { return d.tests == 0 }},
	{name: types.CheckPackageName, norm: "carry no digit in their names", breaks: dirShape.digit},
}

// importingDirs counts the directories, other than dir, holding a file that references a symbol
// one of files defines.
func (g *Graph) importingDirs(dir string, files []string) int {
	g.ensureAdj()
	seen := map[string]bool{}
	for _, f := range files {
		for _, def := range g.out[fileID(f)] {
			if def.Relation != types.RelationDefines {
				continue
			}
			for _, ref := range g.in[def.Target] {
				from, ok := strings.CutPrefix(ref.Source, types.KindFile+":")
				if ok && ref.Relation == types.RelationReferences && path.Dir(from) != dir {
					seen[path.Dir(from)] = true
				}
			}
		}
	}
	return len(seen)
}

// nearest names the two directories whose names sort closest to name, one on each side where
// both exist. ds is sorted.
func nearest(ds []dirShape, name string) []string {
	i, _ := slices.BinarySearchFunc(ds, name, func(d dirShape, n string) int { return cmp.Compare(d.base(), n) })
	lo, hi := i-1, i
	var out []string
	for len(out) < 2 && (lo >= 0 || hi < len(ds)) {
		if lo >= 0 {
			out = append(out, "`"+ds[lo].dir+"`")
			lo--
		}
		if len(out) < 2 && hi < len(ds) {
			out = append(out, "`"+ds[hi].dir+"`")
			hi++
		}
	}
	return out
}

func count(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	if strings.HasSuffix(noun, "y") {
		return fmt.Sprintf("%d %sies", n, strings.TrimSuffix(noun, "y"))
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
