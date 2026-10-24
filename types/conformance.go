package types

import (
	"cmp"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/json"
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

// ConformanceSource is where an enforced value came from.
type ConformanceSource string

const (
	// ConformanceFromRatchet is a value a person committed to the ratchet record.
	ConformanceFromRatchet ConformanceSource = "ratchet"
	// ConformanceFromAnchor is the configured anchor, enforced while nothing is committed.
	ConformanceFromAnchor ConformanceSource = "anchor"
)

// ConformanceKey names one threshold. Scope is a layer or package directory for dep-fanout,
// and empty otherwise.
type ConformanceKey struct {
	Metric     ConformanceMetric     `json:"metric"          yaml:"metric"`
	Population ConformancePopulation `json:"population"      yaml:"population"`
	Scope      string                `json:"scope,omitempty" yaml:"scope,omitempty"`
}

// ConformanceLimit is the configured policy for one key: the percentile a proposal measures
// at, the shipped anchor no committed value may exceed, and, for a metric judging
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

// ConformanceOptions is the conformance policy as the graph reads it: configuration plus the
// committed ratchet record named by RatchetPath. Generated holds the workspace paths of
// declared outputs, which no population counts.
type ConformanceOptions struct {
	Limits      map[ConformanceKey]ConformanceLimit
	Ratchet     ConformanceRatchet
	RatchetPath string
	MinSample   int
	Step        float64
	Exclude     []string
	Generated   map[string]bool
	Gates       ConformanceGates
}

// Effective is the value a check enforces for k, and where it came from: the committed
// ratchet value, else the configured anchor. The key's own scope is consulted before the
// unscoped key. A live percentile is never returned, since enforcing one lets the
// threshold drift as the code grows. Zero and an empty source mean neither is configured.
func (o ConformanceOptions) Effective(k ConformanceKey) (value int, src ConformanceSource) {
	for _, key := range conformanceKeyChain(k) {
		if t, ok := o.committed(key); ok {
			return t.Value, ConformanceFromRatchet
		}
		if l, ok := o.Limits[key]; ok {
			return l.Anchor, ConformanceFromAnchor
		}
	}
	return 0, ""
}

// Validate refuses a misconfigured policy rather than clamping it: an unknown metric or
// population, a committed value looser than its anchor or with no anchor to bound it, a
// key committed twice, a ratchet schema other than 1, and out-of-range numbers. Ratchet
// errors name RatchetPath:line. Every problem is reported, joined.
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

	r := o.Ratchet
	if (o.RatchetPath != "" || r.Schema != 0 || len(r.Thresholds) > 0 || len(r.Known) > 0) && r.Schema != 1 {
		fail("%s: schema %d: want 1", o.ratchetName(), r.Schema)
	}
	firstLine := map[ConformanceKey]int{}
	for _, t := range r.Thresholds {
		at, name := o.ratchetAt(t.Line), conformanceKeyName(t.ConformanceKey)
		if err := checkConformanceKey(t.ConformanceKey); err != nil {
			fail("%s: %s: %w", at, name, err)
			continue
		}
		if line, dup := firstLine[t.ConformanceKey]; dup {
			fail("%s: %s: committed twice, first at line %d", at, name, line)
			continue
		}
		firstLine[t.ConformanceKey] = t.Line
		if t.Value < 0 {
			fail("%s: %s: value %d: want at least 0", at, name, t.Value)
		}
		limit, ok := o.anchor(t.ConformanceKey)
		switch {
		case !ok:
			fail("%s: %s: no anchor is configured to bound the committed %d", at, name, t.Value)
		case looser(t.Metric, t.Value, limit.Anchor):
			fail("%s: %s: committed %d is looser than its anchor %d", at, name, t.Value, limit.Anchor)
		}
	}
	listedAt := map[ConformanceMetric]map[string]int{}
	for _, k := range r.Known {
		at := o.ratchetAt(k.Line)
		if err := checkConformanceMetric(k.Metric); err != nil {
			fail("%s: known: %w", at, err)
		}
		if len(k.Items) == 0 {
			fail("%s: known %s: no items", at, k.Metric)
		}
		if listedAt[k.Metric] == nil {
			listedAt[k.Metric] = map[string]int{}
		}
		for _, item := range k.Items {
			if line, dup := listedAt[k.Metric][item]; dup {
				fail("%s: known %s: %s listed twice, first at line %d", at, k.Metric, item, line)
				continue
			}
			listedAt[k.Metric][item] = k.Line
		}
		if k.Reason == "" {
			fail("%s: known %s: no reason", at, k.Metric)
		}
		for _, item := range slices.Sorted(maps.Keys(k.Shapes)) {
			switch {
			case !slices.Contains(k.Items, item):
				fail("%s: known %s: a shape for %s, which the entry does not list", at, k.Metric, item)
			case k.Shapes[item] == "":
				fail("%s: known %s: an empty shape for %s", at, k.Metric, item)
			}
		}
	}
	return errors.Join(errs...)
}

// looser reports whether committing value for m finds fewer items than its anchor would.
func looser(m ConformanceMetric, value, anchor int) bool {
	if m == ConformanceFileSplit {
		return value < anchor
	}
	return value > anchor
}

func (o ConformanceOptions) committed(k ConformanceKey) (ConformanceThreshold, bool) {
	for _, t := range o.Ratchet.Thresholds {
		if t.ConformanceKey == k {
			return t, true
		}
	}
	return ConformanceThreshold{}, false
}

// anchor is the limit bounding k: its own scope's, else the unscoped key's.
func (o ConformanceOptions) anchor(k ConformanceKey) (ConformanceLimit, bool) {
	for _, key := range conformanceKeyChain(k) {
		if l, ok := o.Limits[key]; ok {
			return l, true
		}
	}
	return ConformanceLimit{}, false
}

func (o ConformanceOptions) ratchetName() string {
	if o.RatchetPath == "" {
		return "conformance ratchet"
	}
	return o.RatchetPath
}

func (o ConformanceOptions) ratchetAt(line int) string {
	if line == 0 {
		return o.ratchetName()
	}
	return fmt.Sprintf("%s:%d", o.ratchetName(), line)
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

// ConformanceRatchet is the committed record of measured thresholds: what a person accepted
// from a proposal, never something magus writes on its own. [ReadConformanceRatchet] gives
// the file format.
type ConformanceRatchet struct {
	Schema     int
	Measured   ConformanceProvenance
	Thresholds []ConformanceThreshold
	Known      []ConformanceKnown
}

// ConformanceProvenance is the revision and date the committed values were measured at.
type ConformanceProvenance struct {
	Rev  string `json:"rev"`
	Date string `json:"date"`
}

// ConformanceThreshold is one committed value, with the sample size and percentile it was
// measured at and why. Line is its 1-based line in the record, for naming file:line.
type ConformanceThreshold struct {
	ConformanceKey
	Value      int     `json:"value"`
	N          int     `json:"n"`
	Percentile float64 `json:"percentile"`
	Why        string  `json:"why,omitempty"`
	Line       int     `json:"-"`
}

// ConformanceKnown is a worklist of items already past a metric's value and accepted for a
// reason. A known item that no longer violates is stale and should be deleted. Shapes maps
// a listed item to its fingerprint when it was listed, a symbol's body shape or a file's
// set of body digests, which a rename or a move leaves unchanged: it is how the item is
// recognized under a new ID. Line is its 1-based line in the record.
type ConformanceKnown struct {
	Metric ConformanceMetric `json:"metric"           yaml:"metric"`
	Items  []string          `json:"items"            yaml:"items"`
	Reason string            `json:"reason"           yaml:"reason"`
	Shapes map[string]string `json:"shapes,omitempty" yaml:"shapes,omitempty"`
	Line   int               `json:"-"                yaml:"-"                buzz:"-"`
}

const conformanceRowKinds = "schema, measured, threshold or known"

// conformanceRow is one line of the ratchet record: a kind and the members that kind names.
// Only the field for kind is set.
type conformanceRow struct {
	kind      string
	schema    int
	measured  ConformanceProvenance
	threshold ConformanceThreshold
	known     ConformanceKnown
}

// UnmarshalJSON decodes one line, strictly: a member the kind does not name, a repeated
// member, a wrongly typed value and a missing required member are errors, with no
// fallback reader for older shapes.
func (c *conformanceRow) UnmarshalJSON(data []byte) error {
	var members map[string]any
	if err := json.Unmarshal(data, &members); err != nil {
		return err
	}
	rawKind, ok := members["kind"]
	if !ok {
		return fmt.Errorf("no kind: want %s", conformanceRowKinds)
	}
	kind, _ := rawKind.(string)
	var row conformanceRow
	var required []string
	var err error
	switch kind {
	case "schema":
		w := struct {
			Kind   string `json:"kind"`
			Schema int    `json:"schema"`
		}{}
		required = []string{"schema"}
		if err = json.UnmarshalStrict(data, &w); err == nil && w.Schema != 1 {
			err = fmt.Errorf("schema %d: want 1", w.Schema)
		}
		row.schema = w.Schema
	case "measured":
		w := struct {
			Kind string `json:"kind"`
			ConformanceProvenance
		}{}
		required = []string{"rev", "date"}
		err = json.UnmarshalStrict(data, &w)
		row.measured = w.ConformanceProvenance
	case "threshold":
		w := struct {
			Kind string `json:"kind"`
			ConformanceThreshold
		}{}
		required = []string{"metric", "population", "value", "n", "percentile"}
		if err = json.UnmarshalStrict(data, &w); err == nil {
			err = checkConformanceKey(w.ConformanceKey)
		}
		row.threshold = w.ConformanceThreshold
	case "known":
		w := struct {
			Kind string `json:"kind"`
			ConformanceKnown
		}{}
		required = []string{"metric", "items", "reason"}
		if err = json.UnmarshalStrict(data, &w); err == nil {
			err = checkConformanceMetric(w.Metric)
		}
		row.known = w.ConformanceKnown
	default:
		return fmt.Errorf("kind %#v: want %s", rawKind, conformanceRowKinds)
	}
	if err != nil {
		return fmt.Errorf("%s: %w", kind, err)
	}
	if missing := slices.DeleteFunc(required, func(name string) bool { _, ok := members[name]; return ok }); len(missing) > 0 {
		return fmt.Errorf("%s: no %s", kind, strings.Join(missing, ", "))
	}
	row.kind = kind
	*c = row
	return nil
}

// ReadConformanceRatchet decodes a committed ratchet record in JSON Lines: one JSON object
// per line, blank lines skipped. Each carries a kind and that kind's members:
//
//	{"kind":"schema","schema":1}
//	{"kind":"measured","rev":"<rev>","date":"<date>"}
//	{"kind":"threshold","metric":"file-lines","population":"source","scope":"<dir>","value":938,"n":1130,"percentile":95,"why":"<why>"}
//	{"kind":"known","metric":"clone-tokens","reason":"<why>","items":["<item>"],"shapes":{"<item>":"<shape>"}}
//
// scope, why and shapes are optional, and a record names its schema and its measurement at
// most once. An unknown kind, member, metric or population, a repeated member, a wrongly
// typed value, a repeated line, and a key committed twice are errors naming path:line,
// path being where data was read from.
func ReadConformanceRatchet(path string, data []byte) (ConformanceRatchet, error) {
	var r ConformanceRatchet
	seen := conformanceSeen{thresholds: map[ConformanceKey]int{}}
	lineAt := map[string]int{}
	for i, text := range strings.Split(string(data), "\n") {
		line := i + 1
		text = strings.TrimSpace(text)
		if text == "" {
			continue
		}
		if first, dup := lineAt[text]; dup {
			return ConformanceRatchet{}, fmt.Errorf("%s:%d: same line as line %d", path, line, first)
		}
		lineAt[text] = line

		var row conformanceRow
		err := row.UnmarshalJSON([]byte(text))
		if err == nil {
			err = r.add(row, line, &seen)
		}
		if err != nil {
			return ConformanceRatchet{}, fmt.Errorf("%s:%d: %w", path, line, err)
		}
	}
	if seen.schema == 0 {
		return ConformanceRatchet{}, fmt.Errorf(`%s: ratchet has no schema line: want {"kind":"schema","schema":1}`, path)
	}
	return r, nil
}

// conformanceSeen is the line each once-only row of a record was first read at.
type conformanceSeen struct {
	schema, measured int
	thresholds       map[ConformanceKey]int
}

// add files row, read from line, into r, refusing a second schema or measurement and a
// key already committed.
func (r *ConformanceRatchet) add(row conformanceRow, line int, seen *conformanceSeen) error {
	switch row.kind {
	case "schema":
		if seen.schema != 0 {
			return fmt.Errorf("schema row given twice, first at line %d", seen.schema)
		}
		seen.schema, r.Schema = line, row.schema
	case "measured":
		if seen.measured != 0 {
			return fmt.Errorf("measured: given twice, first at line %d", seen.measured)
		}
		seen.measured, r.Measured = line, row.measured
	case "threshold":
		t := row.threshold
		if first, dup := seen.thresholds[t.ConformanceKey]; dup {
			return fmt.Errorf("threshold %s: committed twice, first at line %d", conformanceKeyName(t.ConformanceKey), first)
		}
		seen.thresholds[t.ConformanceKey] = line
		t.Line = line
		r.Thresholds = append(r.Thresholds, t)
	case "known":
		k := row.known
		k.Line = line
		r.Known = append(r.Known, k)
	}
	return nil
}

// ConformanceOutput is the conformance lens: one row per key, the known items no longer past
// their value, and how many files Exclude hid, so a widened exclusion stays visible.
// Carried are the known items followed through a rename or a move, each judged under its
// new ID; Ambiguous are those not followed, because more than one candidate shares the
// recorded shape or another known item claims the one that does. Shapes is the current fingerprint of each listed item that has one,
// under its carried ID, for filling a record that lacks it.
type ConformanceOutput struct {
	Definition string             `json:"definition" yaml:"definition"`
	Rows       []ConformanceRow   `json:"rows"       yaml:"rows"`
	Stale      []ConformanceKnown `json:"stale"      yaml:"stale"`
	Carried    []ConformanceCarry `json:"carried"    yaml:"carried"`
	Ambiguous  []ConformanceCarry `json:"ambiguous"  yaml:"ambiguous"`
	Shapes     map[string]string  `json:"shapes"     yaml:"shapes"`
	Excluded   int                `json:"excluded"   yaml:"excluded"`
	Answer     KnowledgeAnswer    `json:"answer"     yaml:"answer"`
}

// ConformanceRow is one key's distribution and verdict. Live is the configured percentile
// measured today; Value and Source are what Effective enforces. Propose is zero, or a
// tighter value to commit: it is never looser than Value. Known counts the accepted items
// past Value; Violations are the rest.
type ConformanceRow struct {
	Key        ConformanceKey    `json:"key"        yaml:"key"`
	N          int               `json:"n"          yaml:"n"`
	P50        int               `json:"p50"        yaml:"p50"`
	P90        int               `json:"p90"        yaml:"p90"`
	P95        int               `json:"p95"        yaml:"p95"`
	P99        int               `json:"p99"        yaml:"p99"`
	Live       int               `json:"live"       yaml:"live"`
	Value      int               `json:"value"      yaml:"value"`
	Anchor     int               `json:"anchor"     yaml:"anchor"`
	Propose    int               `json:"propose"    yaml:"propose"`
	Known      int               `json:"known"      yaml:"known"`
	Source     ConformanceSource `json:"source"     yaml:"source"`
	Violations []ConformanceItem `json:"violations" yaml:"violations"`
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

// ConformanceCarry is a known item that matches no sample of its metric, From, and the
// unlisted items past its value whose shape equals the one recorded for it, To. A carried
// item has exactly one; an ambiguous one has more, or shares its one with another known
// item.
type ConformanceCarry struct {
	Metric ConformanceMetric `json:"metric" yaml:"metric"`
	From   string            `json:"from"   yaml:"from"`
	To     []string          `json:"to"     yaml:"to"`
}

// ConformanceItem is one file, symbol, package or clone group past its row's value and not
// known. Source is its path:line. Home is where it belongs, for a metric that names one: a
// package directory, or for file-split the root file. Unshared marks a file-split sibling
// that references nothing its root defines and defines nothing its root references.
type ConformanceItem struct {
	ID       string `json:"id"                 yaml:"id"`
	Source   string `json:"source"             yaml:"source"`
	Measure  int    `json:"measure"            yaml:"measure"`
	Home     string `json:"home,omitempty"     yaml:"home,omitempty"`
	Unshared bool   `json:"unshared,omitempty" yaml:"unshared,omitempty"`
}
