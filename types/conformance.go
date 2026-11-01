package types

import (
	"cmp"
	"errors"
	"fmt"
	"maps"
	"path"
	"slices"
	"strings"
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
	// ConformanceDepFanout is how many workspace packages a package imports.
	ConformanceDepFanout ConformanceMetric = "dep-fanout"
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

// ConformanceKey names one threshold. Scope is a layer or package directory for dep-fanout,
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
		if err := checkConformanceKey(k); err != nil {
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

func checkConformanceKey(k ConformanceKey) error {
	return errors.Join(checkConformanceMetric(k.Metric), checkConformancePopulation(k.Population))
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

// ConformanceOutput is the conformance lens: one row per key, and how many files Exclude hid,
// so a widened exclusion stays visible. Base and Ceilings are set when the caller names a base
// revision: Base is the commit it resolved to, and Ceilings are the touched files whose count of
// a checked metric's violations rose past their count there. Unmeasured are the touched files
// only an external indexer reads, which hold no ceiling, each with why.
type ConformanceOutput struct {
	Definition string                  `json:"definition"           yaml:"definition"`
	Rows       []ConformanceRow        `json:"rows"                 yaml:"rows"`
	Excluded   int                     `json:"excluded"             yaml:"excluded"`
	Answer     KnowledgeAnswer         `json:"answer"               yaml:"answer"`
	Base       string                  `json:"base,omitempty"       yaml:"base,omitempty"`
	Ceilings   []ConformanceCeiling    `json:"ceilings,omitempty"   yaml:"ceilings,omitempty"`
	Unmeasured []ConformanceUnmeasured `json:"unmeasured,omitempty" yaml:"unmeasured,omitempty"`
}

// ConformanceUnmeasured is a touched file the base was not measured for: its language and why.
type ConformanceUnmeasured struct {
	// File is the project's directory when the project is new at head.
	File     string `json:"file"     yaml:"file"`
	Language string `json:"language" yaml:"language"`
	Reason   string `json:"reason"   yaml:"reason"`
}

// ConformanceRow is one key's distribution and verdict. Live is the configured percentile
// measured today; Value is the anchor Effective enforces. Propose is zero, or a tighter anchor
// to configure: it is never looser than Value. Violations are the items past Value.
type ConformanceRow struct {
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

// Conformance is what a caller asks the conformance lens beyond the configured policy:
// Checks are the metrics held to a conformance ceiling, and Base the revision a touched
// file's count of their violations may not rise above. Neither is set to measure only.
type Conformance struct {
	Checks []ConformanceMetric `json:"checks" yaml:"checks"`
	Base   string              `json:"base"   yaml:"base"`
}

// Validate refuses a Base with no Checks, which would compare nothing, and a check that
// names no metric.
func (c Conformance) Validate() error {
	var errs []error
	if c.Base != "" && len(c.Checks) == 0 {
		errs = append(errs, fmt.Errorf("base %s: name the checks held to a conformance ceiling", c.Base))
	}
	for _, m := range c.Checks {
		if err := checkConformanceMetric(m); err != nil {
			errs = append(errs, fmt.Errorf("checks: %w", err))
		}
	}
	return errors.Join(errs...)
}

// ConformanceCeiling is one touched file whose violations of Key rose past its count at the
// base: Base and Head are the two counts, and Violations the head's items in the file.
type ConformanceCeiling struct {
	Key        ConformanceKey    `json:"key"        yaml:"key"`
	File       string            `json:"file"       yaml:"file"`
	Base       int               `json:"base"       yaml:"base"`
	Head       int               `json:"head"       yaml:"head"`
	Violations []ConformanceItem `json:"violations" yaml:"violations"`
}

// ConformanceCeilings is each file among touched whose count of violations of a key whose
// metric is in checks is higher in head than in base, sorted by key, then file. A drop or a
// file that holds as many as before passes. An item counts in every file it names: a clone
// group in each member's file, and a dep-fanout package in every file directly inside it.
func ConformanceCeilings(base, head ConformanceOutput, touched []string, checks []ConformanceMetric) []ConformanceCeiling {
	before := conformanceRowsByKey(base)
	var out []ConformanceCeiling
	for _, row := range head.Rows {
		if !slices.Contains(checks, row.Key.Metric) {
			continue
		}
		for _, file := range touched {
			now := conformanceItemsIn(row.Violations, row.Key.Metric, file)
			was := len(conformanceItemsIn(before[row.Key], row.Key.Metric, file))
			if len(now) > was {
				out = append(out, ConformanceCeiling{Key: row.Key, File: file, Base: was, Head: len(now), Violations: now})
			}
		}
	}
	slices.SortFunc(out, func(a, b ConformanceCeiling) int {
		return cmp.Or(compareConformanceKeys(a.Key, b.Key), cmp.Compare(a.File, b.File))
	})
	return out
}

func conformanceRowsByKey(out ConformanceOutput) map[ConformanceKey][]ConformanceItem {
	byKey := make(map[ConformanceKey][]ConformanceItem, len(out.Rows))
	for _, row := range out.Rows {
		byKey[row.Key] = row.Violations
	}
	return byKey
}

// conformanceItemsIn is the items of metric m that name file.
func conformanceItemsIn(items []ConformanceItem, m ConformanceMetric, file string) []ConformanceItem {
	var out []ConformanceItem
	for _, it := range items {
		if slices.ContainsFunc(strings.Split(it.ID, ", "), func(member string) bool {
			if m == ConformanceDepFanout {
				return member == path.Dir(file)
			}
			name, _, _ := strings.Cut(member, "#")
			return name == file
		}) {
			out = append(out, it)
		}
	}
	return out
}

// ImportAliasReport is how a workspace's packages are imported under other names. Indexed
// is false when no symbol index was read; Packages is then empty, never nil, because nobody
// looked.
type ImportAliasReport struct {
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
type ConformanceItem struct {
	ID       string `json:"id"                 yaml:"id"`
	Source   string `json:"source"             yaml:"source"`
	Measure  int    `json:"measure"            yaml:"measure"`
	Home     string `json:"home,omitempty"     yaml:"home,omitempty"`
	Unshared bool   `json:"unshared,omitempty" yaml:"unshared,omitempty"`
	Rename   string `json:"rename,omitempty"   yaml:"rename,omitempty"`
}
