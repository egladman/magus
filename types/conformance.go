package types

import (
	"cmp"
	"errors"
	"fmt"
	"maps"
	"slices"
)

// ConformanceMetric names one measured code distribution. Every metric but file-split is a
// ceiling, where a lower value is stricter; file-split is a floor (see [ConformanceMetric.Past]).
type ConformanceMetric string

const (
	// ConformanceFileLines is a file's line count.
	ConformanceFileLines ConformanceMetric = "file-lines"
	// ConformanceFuncLines is a function's line count, signature through closing line.
	ConformanceFuncLines ConformanceMetric = "func-lines"
	// ConformanceTypeMethods is how many methods a type declares.
	ConformanceTypeMethods ConformanceMetric = "type-methods"
	// ConformanceFileSymbols is how many top-level declarations a file holds.
	ConformanceFileSymbols ConformanceMetric = "file-symbols"
	// ConformanceImportedPackages counts a package's workspace imports.
	ConformanceImportedPackages ConformanceMetric = "imported-packages"
	// ConformanceCloneTokens is the token count of a body another body shares its shape with.
	ConformanceCloneTokens ConformanceMetric = "clone-tokens"
	// ConformanceExportScope counts exported symbols nothing outside their package uses.
	ConformanceExportScope ConformanceMetric = "export-scope"
	// ConformanceCallSequence is the longest ordered run of workspace callees a function shares
	// with any other function.
	ConformanceCallSequence ConformanceMetric = "call-sequence"
	// ConformanceSharedBody is the line count of a function two or more other packages call.
	ConformanceSharedBody ConformanceMetric = "shared-body"
	// ConformanceSharedReach is how many packages besides its own call a function.
	ConformanceSharedReach ConformanceMetric = "shared-reach"
	// ConformanceCallerSpread is the most import hops between two packages calling one
	// function, with the function's own package removed.
	ConformanceCallerSpread ConformanceMetric = "caller-spread"
	// ConformanceModeKnobs counts the knobs of a function shared past the coupling rule: a
	// parameter its body branches on, or an option one caller package alone sets.
	ConformanceModeKnobs ConformanceMetric = "mode-knobs"
	// ConformanceAliasShare is the percent of a package's importers that must alias it: not
	// to its own package name, and not from inside a package of that name.
	ConformanceAliasShare ConformanceMetric = "alias-share"
	// ConformanceNameClash is the most percent of a package's importers that also import
	// another package of the same name, measured on the package with fewer importers.
	ConformanceNameClash ConformanceMetric = "name-clash"
	// ConformanceMainLogic is the body tokens of a main-package callable that computes rather
	// than wires: not main or init, no `args []string`, io.Writer or workspace-interface
	// parameter, and no call into another workspace package. Every other callable measures 0.
	ConformanceMainLogic ConformanceMetric = "main-logic"
	// ConformanceStorePathExport is how many non-test files outside its package reference an
	// exported path-shaped symbol (a name ending Path, Dir, File, FileName or Ext) of a
	// package that writes state through internal/file.
	ConformanceStorePathExport ConformanceMetric = "store-path-export"
	// ConformanceFileSplit is the lines a concept sibling and its root would make as one file:
	// R_<suffix>.go beside R.go, split by topic rather than by platform, test or generator. It is
	// a floor: a split is owed only past the value, so a sibling at or under it belongs in R.go.
	// Its suffix names one concept: R_X_Y.go splits R_X.go, never R.go (see [ConformanceItem]).
	ConformanceFileSplit ConformanceMetric = "file-split"
)

// Past reports whether an item measuring measure is past value: above it for a ceiling, at or
// under it for the file-split floor.
func (v ConformanceMetric) Past(measure, value int) bool {
	if v == ConformanceFileSplit {
		return measure <= value
	}
	return measure > value
}

// ConformancePopulation is the set of files a metric is measured over. Each is measured apart,
// so moving code into tests cannot shift the source distribution. Generated output belongs
// to none.
type ConformancePopulation string

const (
	// ConformancePopSource is non-test code outside a main package.
	ConformancePopSource ConformancePopulation = "source"
	// ConformancePopMain is code under Go's package clause `main`; a language without one has
	// no main population.
	ConformancePopMain ConformancePopulation = "main"
	// ConformancePopTest is test code.
	ConformancePopTest ConformancePopulation = "test"
)

// ConformanceKey names one threshold. Scope is a layer or package directory for imported-packages,
// and empty otherwise.
type ConformanceKey struct {
	Metric     ConformanceMetric     `json:"metric"          yaml:"metric"`
	Population ConformancePopulation `json:"population"      yaml:"population"`
	Scope      string                `json:"scope,omitempty" yaml:"scope,omitempty"`
}

// ConformanceLimit is the configured policy for one key: the percentile a proposal measures
// at, the anchor every item is judged against, and, for a metric judging
// packages by their importers, the fewest importers a package needs to be judged (zero
// judges every package with one).
type ConformanceLimit struct {
	Percentile float64
	Anchor     int
	MinSubject int
}

// ConformanceGates are the absolute noise gates of the conformance checks.
type ConformanceGates struct {
	MinCohort   int
	MinShare    float64
	Bar         float64
	MaxFindings int
}

// Override is g with one review's MinCohort and MinShare in place of its own. Zero keeps g's.
func (g ConformanceGates) Override(minCohort int, minShare float64) ConformanceGates {
	g.MinCohort = cmp.Or(minCohort, g.MinCohort)
	g.MinShare = cmp.Or(minShare, g.MinShare)
	return g
}

// ConformanceOptions is the conformance policy as the graph reads it, from configuration
// alone: no record of counts is kept, since a change is judged against its base revision.
// Generated holds the workspace paths of declared outputs, which no population counts.
type ConformanceOptions struct {
	Limits    map[ConformanceKey]ConformanceLimit
	MinSample int
	Step      float64
	Exclude   []string
	Generated map[string]bool
	Gates     ConformanceGates
}

// Effective is the value a check enforces for k: the configured anchor, the key's own scope
// consulted before the unscoped key. A live percentile is never returned, since enforcing one
// lets the threshold drift as the code grows. ok is false when no anchor is configured.
func (o ConformanceOptions) Effective(k ConformanceKey) (value int, ok bool) {
	for _, key := range conformanceKeyChain(k) {
		if l, found := o.Limits[key]; found {
			return l.Anchor, true
		}
	}
	return 0, false
}

// Validate refuses a misconfigured policy rather than clamping it: an unknown metric or
// population, and out-of-range numbers. Every problem is reported, joined.
func (o ConformanceOptions) Validate() error {
	var errs []error
	fail := func(format string, a ...any) { errs = append(errs, fmt.Errorf(format, a...)) }

	if o.MinSample < 1 {
		fail("min_sample %d: want at least 1", o.MinSample)
	}
	if o.Step < 0 || o.Step >= 1 {
		fail("step %g: want at least 0 and below 1", o.Step)
	}
	if o.Gates.MinCohort < 1 {
		fail("gates.min_cohort %d: want at least 1", o.Gates.MinCohort)
	}
	if o.Gates.MinShare < 0 || o.Gates.MinShare > 1 {
		fail("gates.min_share %g: want 0 through 1", o.Gates.MinShare)
	}
	if o.Gates.Bar < 0 || o.Gates.Bar > 1 {
		fail("gates.bar %g: want 0 through 1", o.Gates.Bar)
	}
	if o.Gates.MaxFindings < 0 {
		fail("gates.max_findings %d: want at least 0", o.Gates.MaxFindings)
	}

	keys := slices.SortedFunc(maps.Keys(o.Limits), compareConformanceKeys)
	for _, k := range keys {
		l := o.Limits[k]
		if err := errors.Join(checkConformanceMetric(k.Metric), checkConformancePopulation(k.Population)); err != nil {
			fail("limit %s: %w", conformanceKeyName(k), err)
			continue
		}
		if l.Percentile < 0 || l.Percentile > 100 {
			fail("limit %s: percentile %g: want 0 through 100", conformanceKeyName(k), l.Percentile)
		}
		if l.Anchor < 0 {
			fail("limit %s: anchor %d: want at least 0", conformanceKeyName(k), l.Anchor)
		}
		if l.MinSubject < 0 {
			fail("limit %s: min_subject %d: want at least 0", conformanceKeyName(k), l.MinSubject)
		}
	}
	return errors.Join(errs...)
}

func conformanceKeyChain(k ConformanceKey) []ConformanceKey {
	if k.Scope == "" {
		return []ConformanceKey{k}
	}
	return []ConformanceKey{k, {Metric: k.Metric, Population: k.Population}}
}

func checkConformanceMetric(m ConformanceMetric) error {
	if m == "" || !m.Valid() {
		return fmt.Errorf("metric %s: want one of %v", m, m.Values())
	}
	return nil
}

func checkConformancePopulation(p ConformancePopulation) error {
	if p == "" || !p.Valid() {
		return fmt.Errorf("population %s: want one of %v", p, p.Values())
	}
	return nil
}

func conformanceKeyName(k ConformanceKey) string {
	name := fmt.Sprintf("%s %s", string(k.Metric), string(k.Population))
	if k.Scope != "" {
		name += " " + k.Scope
	}
	return name
}

func compareConformanceKeys(a, b ConformanceKey) int {
	return cmp.Or(cmp.Compare(a.Metric, b.Metric), cmp.Compare(a.Population, b.Population), cmp.Compare(a.Scope, b.Scope))
}

// Conformance is the conformance lens: one row per key, and how many files Exclude hid,
// so a widened exclusion stays visible.
//
// Packages and Unmeasured are set only on a base measure. Packages, in
// directory order, tells an import of moved declarations from a new one.
// Unmeasured are the projects the base lacks, each with why.
type Conformance struct {
	Definition    string                    `json:"definition"           yaml:"definition"`
	Distributions []ConformanceDistribution `json:"distributions"        yaml:"distributions"`
	Excluded      int                       `json:"excluded"             yaml:"excluded"`
	Answer        KnowledgeAnswer           `json:"answer"               yaml:"answer"`
	Unmeasured    []ConformanceUnmeasured   `json:"unmeasured,omitempty" yaml:"unmeasured,omitempty"`
	Packages      []ConformancePackage      `json:"packages,omitempty"   yaml:"packages,omitempty"`
}

// ConformanceBase is the measure a change is judged against: Commit's kept lens, under the
// policy the head lens is measured with, and the files changed since it. Touched are the
// changed files the base was measured for; Unmeasured are the changed files it was not, each
// with why. It carries no verdict: comparing it with the head lens is the caller's.
type ConformanceBase struct {
	Commit        string                    `json:"commit"        yaml:"commit"`
	Distributions []ConformanceDistribution `json:"distributions" yaml:"distributions"`
	Packages      []ConformancePackage      `json:"packages"      yaml:"packages"`
	Touched       []string                  `json:"touched"       yaml:"touched"`
	Unmeasured    []ConformanceUnmeasured   `json:"unmeasured"    yaml:"unmeasured"`
}

// ConformanceUnmeasured is a file or project the base was not measured for: its language and why.
type ConformanceUnmeasured struct {
	// File is the project's directory when the project is new at head.
	File     string `json:"file"     yaml:"file"`
	Language string `json:"language" yaml:"language"`
	Reason   string `json:"reason"   yaml:"reason"`
}

// ConformanceDistribution is one key's distribution and verdict. Live is the configured percentile
// measured today; Value is the anchor Effective enforces. Propose is zero, or a tighter anchor
// to configure: it is never looser than Value. Violations are the items past Value.
type ConformanceDistribution struct {
	Key        ConformanceKey    `json:"key"        yaml:"key"`
	N          int               `json:"n"          yaml:"n"`
	P50        int               `json:"p50"        yaml:"p50"`
	P90        int               `json:"p90"        yaml:"p90"`
	P95        int               `json:"p95"        yaml:"p95"`
	P99        int               `json:"p99"        yaml:"p99"`
	Live       int               `json:"live"       yaml:"live"`
	Value      int               `json:"value"      yaml:"value"`
	Propose    int               `json:"propose"    yaml:"propose"`
	Violations []ConformanceItem `json:"violations" yaml:"violations"`
}

// ImportAliases is how a workspace's packages are imported under other names. Indexed
// is false when no symbol index was read; Packages is then empty, never nil, because nobody
// looked. ADR 0022: domain noun, not ImportAliasReport.
type ImportAliases struct {
	Indexed  bool         `json:"indexed"  yaml:"indexed"`
	Packages []AliasFacts `json:"packages" yaml:"packages"`
}

// AliasFacts is one workspace package with an importer: its directory and package name,
// how many files import it, how many write an alias and under which spellings, and how many
// of those had to (Needed: not the package's own name, and not from inside a package of
// that name). Clashes are the packages of the same name its importers also import.
type AliasFacts struct {
	Dir       string         `json:"dir"       yaml:"dir"`
	Name      string         `json:"name"      yaml:"name"`
	Importers int            `json:"importers" yaml:"importers"`
	Aliased   int            `json:"aliased"   yaml:"aliased"`
	Needed    int            `json:"needed"    yaml:"needed"`
	Aliases   map[string]int `json:"aliases"   yaml:"aliases"`
	Clashes   []AliasClash   `json:"clashes"   yaml:"clashes"`
}

// AliasClash is another package of the same name: Both files import the two, and
// Importers import it. Ours is false for a package outside the workspace, whose name is the
// last element of its import path.
type AliasClash struct {
	Dir       string `json:"dir"       yaml:"dir"`
	Name      string `json:"name"      yaml:"name"`
	Both      int    `json:"both"      yaml:"both"`
	Importers int    `json:"importers" yaml:"importers"`
	Ours      bool   `json:"ours"      yaml:"ours"`
}

// ConformanceItem is one file, symbol, package or clone group past its row's value. Source is
// its path:line. Home is where it belongs, for a metric that names one: a
// package directory, or for file-split the root file. Unshared marks a file-split sibling
// that references nothing its root defines and defines nothing its root references. Rename
// is the file a file-split sibling should be when its suffix after the root names more than
// one concept: R_X_Y.go with no R_X.go is past its row whatever it measures, and names R_X.go.
//
// Digest is a symbol's KnowledgeSymbol.BodyDigest, so the item keeps its
// identity when its file moves. A clone group's is its members' digests,
// sorted and joined by commas. A file or package item has none. Imports
// are an imported-packages item's imports, in directory order.
type ConformanceItem struct {
	ID       string              `json:"id"                 yaml:"id"`
	Source   string              `json:"source"             yaml:"source"`
	Measure  int                 `json:"measure"            yaml:"measure"`
	Home     string              `json:"home,omitempty"     yaml:"home,omitempty"`
	Unshared bool                `json:"unshared,omitempty" yaml:"unshared,omitempty"`
	Rename   string              `json:"rename,omitempty"   yaml:"rename,omitempty"`
	Digest   string              `json:"digest,omitempty"   yaml:"digest,omitempty"`
	Imports  []ConformanceImport `json:"imports,omitempty"  yaml:"imports,omitempty"`
}

// ConformanceImport is one package directory an imported-packages item
// imports. Digests are the sorted body digests of the top-level
// declarations its files reference there. Digests is empty when one of
// those has no digest, since the import is then unknown.
type ConformanceImport struct {
	Dir     string   `json:"dir"               yaml:"dir"`
	Digests []string `json:"digests,omitempty" yaml:"digests,omitempty"`
}

// ConformancePackage is one package directory as a base measure keeps it.
// Imports are the other workspace package directories it imports. Digests
// are the sorted body digests of its top-level non-test declarations.
type ConformancePackage struct {
	Dir     string   `json:"dir"               yaml:"dir"`
	Imports []string `json:"imports,omitempty" yaml:"imports,omitempty"`
	Digests []string `json:"digests,omitempty" yaml:"digests,omitempty"`
}
