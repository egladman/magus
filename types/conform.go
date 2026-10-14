package types

import (
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
)

// ConformMetric names one measured code distribution. Every metric is a ceiling: a lower
// value is stricter.
type ConformMetric string

const (
	// ConformFileLines is a file's line count.
	ConformFileLines ConformMetric = "file-lines"
	// ConformFuncLines is a function's line count, signature through closing line.
	ConformFuncLines ConformMetric = "func-lines"
	// ConformTypeMethods is how many methods a type declares.
	ConformTypeMethods ConformMetric = "type-methods"
	// ConformFileSymbols is how many top-level declarations a file holds.
	ConformFileSymbols ConformMetric = "file-symbols"
	// ConformDepFanout is how many workspace packages a package imports.
	ConformDepFanout ConformMetric = "dep-fanout"
	// ConformCloneTokens is the token count of a body another body shares its shape with.
	ConformCloneTokens ConformMetric = "clone-tokens"
	// ConformExportScope counts exported symbols nothing outside their package uses.
	ConformExportScope ConformMetric = "export-scope"
)

// ConformPopulation is the set of files a metric is measured over. Each is measured apart,
// so moving code into tests cannot shift the source distribution. Generated output belongs
// to none.
type ConformPopulation string

const (
	// ConformPopSource is non-test code outside a main package.
	ConformPopSource ConformPopulation = "source"
	// ConformPopMain is code under Go's package clause `main`; a language without one has
	// no main population.
	ConformPopMain ConformPopulation = "main"
	// ConformPopTest is test code.
	ConformPopTest ConformPopulation = "test"
)

// ConformSource is where an enforced value came from.
type ConformSource string

const (
	// ConformFromRatchet is a value a person committed to the ratchet record.
	ConformFromRatchet ConformSource = "ratchet"
	// ConformFromAnchor is the configured anchor, enforced while nothing is committed.
	ConformFromAnchor ConformSource = "anchor"
)

// ConformKey names one threshold. Scope is a layer or package directory for dep-fanout,
// and empty otherwise.
type ConformKey struct {
	Metric     ConformMetric     `json:"metric"          yaml:"metric"`
	Population ConformPopulation `json:"population"      yaml:"population"`
	Scope      string            `json:"scope,omitempty" yaml:"scope,omitempty"`
}

// ConformLimit is the configured policy for one key: the percentile a proposal measures
// at, and the shipped anchor no committed value may exceed.
type ConformLimit struct {
	Percentile float64
	Anchor     int
}

// ConformGates are the absolute noise gates of the conformance checks.
type ConformGates struct {
	MinCohort   int
	MinShare    float64
	Bar         float64
	MaxFindings int
}

// ConformOptions is the conform policy as the graph reads it: configuration plus the
// committed ratchet record named by RatchetPath. Generated holds the workspace paths of
// declared outputs, which no population counts.
type ConformOptions struct {
	Limits      map[ConformKey]ConformLimit
	Ratchet     ConformRatchet
	RatchetPath string
	MinSample   int
	Step        float64
	Exclude     []string
	Generated   map[string]bool
	Gates       ConformGates
}

// Effective is the value a check enforces for k, and where it came from: the committed
// ratchet value, else the configured anchor. The key's own scope is consulted before the
// unscoped key. A live percentile is never returned, since enforcing one lets the
// threshold drift as the code grows. Zero and an empty source mean neither is configured.
func (o ConformOptions) Effective(k ConformKey) (value int, src ConformSource) {
	for _, key := range conformKeyChain(k) {
		if t, ok := o.committed(key); ok {
			return t.Value, ConformFromRatchet
		}
		if l, ok := o.Limits[key]; ok {
			return l.Anchor, ConformFromAnchor
		}
	}
	return 0, ""
}

// Validate refuses a misconfigured policy rather than clamping it: an unknown metric or
// population, a committed value looser than its anchor or with no anchor to bound it, a
// key committed twice, a ratchet schema other than 1, and out-of-range numbers. Ratchet
// errors name RatchetPath:line. Every problem is reported, joined.
func (o ConformOptions) Validate() error {
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

	keys := slices.SortedFunc(maps.Keys(o.Limits), compareConformKeys)
	for _, k := range keys {
		l := o.Limits[k]
		if err := checkConformKey(k); err != nil {
			fail("limit %s: %w", conformKeyName(k), err)
			continue
		}
		if l.Percentile < 0 || l.Percentile > 100 {
			fail("limit %s: percentile %g: want 0 through 100", conformKeyName(k), l.Percentile)
		}
		if l.Anchor < 0 {
			fail("limit %s: anchor %d: want at least 0", conformKeyName(k), l.Anchor)
		}
	}

	r := o.Ratchet
	if (o.RatchetPath != "" || r.Schema != 0 || len(r.Thresholds) > 0 || len(r.Known) > 0) && r.Schema != 1 {
		fail("%s: schema %d: want 1", o.ratchetName(), r.Schema)
	}
	firstLine := map[ConformKey]int{}
	for _, t := range r.Thresholds {
		at, name := o.ratchetAt(t.Line), conformKeyName(t.ConformKey)
		if err := checkConformKey(t.ConformKey); err != nil {
			fail("%s: %s: %w", at, name, err)
			continue
		}
		if line, dup := firstLine[t.ConformKey]; dup {
			fail("%s: %s: committed twice, first at line %d", at, name, line)
			continue
		}
		firstLine[t.ConformKey] = t.Line
		if t.Value < 0 {
			fail("%s: %s: value %d: want at least 0", at, name, t.Value)
		}
		limit, ok := o.anchor(t.ConformKey)
		switch {
		case !ok:
			fail("%s: %s: no anchor is configured to bound the committed %d", at, name, t.Value)
		case t.Value > limit.Anchor:
			fail("%s: %s: committed %d is looser than its anchor %d", at, name, t.Value, limit.Anchor)
		}
	}
	for _, k := range r.Known {
		at := o.ratchetAt(k.Line)
		if err := checkConformMetric(k.Metric); err != nil {
			fail("%s: known: %w", at, err)
		}
		if len(k.Items) == 0 {
			fail("%s: known %s: no items", at, k.Metric)
		}
		if k.Reason == "" {
			fail("%s: known %s: no reason", at, k.Metric)
		}
	}
	return errors.Join(errs...)
}

func (o ConformOptions) committed(k ConformKey) (ConformThreshold, bool) {
	for _, t := range o.Ratchet.Thresholds {
		if t.ConformKey == k {
			return t, true
		}
	}
	return ConformThreshold{}, false
}

// anchor is the limit bounding k: its own scope's, else the unscoped key's.
func (o ConformOptions) anchor(k ConformKey) (ConformLimit, bool) {
	for _, key := range conformKeyChain(k) {
		if l, ok := o.Limits[key]; ok {
			return l, true
		}
	}
	return ConformLimit{}, false
}

func (o ConformOptions) ratchetName() string {
	if o.RatchetPath == "" {
		return "conform ratchet"
	}
	return o.RatchetPath
}

func (o ConformOptions) ratchetAt(line int) string {
	if line == 0 {
		return o.ratchetName()
	}
	return fmt.Sprintf("%s:%d", o.ratchetName(), line)
}

func conformKeyChain(k ConformKey) []ConformKey {
	if k.Scope == "" {
		return []ConformKey{k}
	}
	return []ConformKey{k, {Metric: k.Metric, Population: k.Population}}
}

func checkConformKey(k ConformKey) error {
	return errors.Join(checkConformMetric(k.Metric), checkConformPopulation(k.Population))
}

func checkConformMetric(m ConformMetric) error {
	if m == "" || !m.Valid() {
		return fmt.Errorf("metric %s: want one of %v", m, m.Values())
	}
	return nil
}

func checkConformPopulation(p ConformPopulation) error {
	if p == "" || !p.Valid() {
		return fmt.Errorf("population %s: want one of %v", p, p.Values())
	}
	return nil
}

func conformKeyName(k ConformKey) string {
	name := string(k.Metric) + " " + string(k.Population)
	if k.Scope != "" {
		name += " " + k.Scope
	}
	return name
}

func compareConformKeys(a, b ConformKey) int {
	return cmp.Or(cmp.Compare(a.Metric, b.Metric), cmp.Compare(a.Population, b.Population), cmp.Compare(a.Scope, b.Scope))
}

// ConformRatchet is the committed record of measured thresholds: what a person accepted
// from a proposal, never something magus writes on its own.
type ConformRatchet struct {
	Schema     int                `json:"schema"`
	Measured   ConformProvenance  `json:"measured"`
	Thresholds []ConformThreshold `json:"thresholds"`
	Known      []ConformKnown     `json:"known"`
}

// ConformProvenance is the revision and date the committed values were measured at.
type ConformProvenance struct {
	Rev  string `json:"rev"`
	Date string `json:"date"`
}

// ConformThreshold is one committed value, with the sample size and percentile it was
// measured at and why. Line is its 1-based line in the record, for naming file:line.
type ConformThreshold struct {
	ConformKey
	Value      int     `json:"value"`
	N          int     `json:"n"`
	Percentile float64 `json:"percentile"`
	Why        string  `json:"why"`
	Line       int     `json:"-"`
}

// ConformKnown is a worklist of items already past a metric's value and accepted for a
// reason. A known item that no longer violates is stale and should be deleted. Line is its
// 1-based line in the record.
type ConformKnown struct {
	Metric ConformMetric `json:"metric" yaml:"metric"`
	Items  []string      `json:"items"  yaml:"items"`
	Reason string        `json:"reason" yaml:"reason"`
	Line   int           `json:"-"      yaml:"-"`
}

// ReadConformRatchet decodes a committed ratchet record strictly: an unknown field, a
// duplicate key, trailing data or a schema other than 1 is an error naming its line.
// Each threshold and known entry carries the line it starts on. The caller prefixes
// errors with the record's path.
func ReadConformRatchet(data []byte) (ConformRatchet, error) {
	if err := conformNoDuplicateKeys(data); err != nil {
		return ConformRatchet{}, err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	if err := conformExpect(dec, '{'); err != nil {
		return ConformRatchet{}, fmt.Errorf("line %d: ratchet: %w", conformLineAt(data, 0), err)
	}
	var r ConformRatchet
	schemaLine := 0
	for dec.More() {
		at := conformLineAt(data, dec.InputOffset())
		tok, err := dec.Token()
		if err != nil {
			return ConformRatchet{}, fmt.Errorf("line %d: %w", at, err)
		}
		key, _ := tok.(string)
		switch key {
		case "schema":
			schemaLine = at
			err = dec.Decode(&r.Schema)
		case "measured":
			err = conformDecodeStrict(dec, &r.Measured)
		case "thresholds":
			r.Thresholds, err = conformDecodeList(dec, data, func(t *ConformThreshold, line int) { t.Line = line })
		case "known":
			r.Known, err = conformDecodeList(dec, data, func(k *ConformKnown, line int) { k.Line = line })
		default:
			err = conformLineError{line: at, err: fmt.Errorf("unknown field %q", key)}
		}
		if err != nil {
			var lined conformLineError
			if errors.As(err, &lined) {
				return ConformRatchet{}, err
			}
			return ConformRatchet{}, fmt.Errorf("line %d: %s: %w", at, key, err)
		}
	}
	if _, err := dec.Token(); err != nil {
		return ConformRatchet{}, err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return ConformRatchet{}, fmt.Errorf("line %d: trailing data after the ratchet", conformLineAt(data, dec.InputOffset()))
	}
	if r.Schema != 1 {
		if schemaLine == 0 {
			return ConformRatchet{}, errors.New("ratchet has no schema: want 1")
		}
		return ConformRatchet{}, fmt.Errorf("line %d: schema %d: want 1", schemaLine, r.Schema)
	}
	return r, nil
}

// conformLineError is an error that already names its line.
type conformLineError struct {
	line int
	err  error
}

func (e conformLineError) Error() string { return fmt.Sprintf("line %d: %v", e.line, e.err) }
func (e conformLineError) Unwrap() error { return e.err }

func conformDecodeList[T any](dec *json.Decoder, data []byte, setLine func(*T, int)) ([]T, error) {
	if err := conformExpect(dec, '['); err != nil {
		return nil, err
	}
	var out []T
	for dec.More() {
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return nil, err
		}
		line := conformLineAt(data, dec.InputOffset()-int64(len(raw)))
		var v T
		if err := conformUnmarshalStrict(raw, &v); err != nil {
			return nil, conformLineError{line: line, err: err}
		}
		setLine(&v, line)
		out = append(out, v)
	}
	_, err := dec.Token()
	return out, err
}

func conformDecodeStrict(dec *json.Decoder, v any) error {
	var raw json.RawMessage
	if err := dec.Decode(&raw); err != nil {
		return err
	}
	return conformUnmarshalStrict(raw, v)
}

func conformUnmarshalStrict(raw []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

func conformExpect(dec *json.Decoder, want json.Delim) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	if d, ok := tok.(json.Delim); !ok || d != want {
		return fmt.Errorf("want %q, got %v", want, tok)
	}
	return nil
}

// conformNoDuplicateKeys walks every object in data, which encoding/json would otherwise
// resolve silently in favor of the last key.
func conformNoDuplicateKeys(data []byte) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	var walk func() error
	walk = func() error {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		switch tok {
		case json.Delim('{'):
			seen := map[string]bool{}
			for dec.More() {
				at := conformLineAt(data, dec.InputOffset())
				tok, err := dec.Token()
				if err != nil {
					return err
				}
				key, _ := tok.(string)
				if seen[key] {
					return fmt.Errorf("line %d: duplicate key %q", at, key)
				}
				seen[key] = true
				if err := walk(); err != nil {
					return err
				}
			}
		case json.Delim('['):
			for dec.More() {
				if err := walk(); err != nil {
					return err
				}
			}
		default:
			return nil
		}
		_, err = dec.Token()
		return err
	}
	return walk()
}

// conformLineAt is the 1-based line of the first token at or after offset. A decoder's
// offset sits before the separator and space that precede its next token.
func conformLineAt(data []byte, offset int64) int {
	off := int(min(max(offset, 0), int64(len(data))))
	for off < len(data) && strings.IndexByte(" \t\r\n,:", data[off]) >= 0 {
		off++
	}
	return 1 + bytes.Count(data[:off], []byte("\n"))
}

// ConformOutput is the conform lens: one row per key, the known items no longer past
// their value, and how many files Exclude hid, so a widened exclusion stays visible.
type ConformOutput struct {
	Definition string          `json:"definition" yaml:"definition"`
	Rows       []ConformRow    `json:"rows"       yaml:"rows"`
	Stale      []ConformKnown  `json:"stale"      yaml:"stale"`
	Excluded   int             `json:"excluded"   yaml:"excluded"`
	Answer     KnowledgeAnswer `json:"answer"     yaml:"answer"`
}

// ConformRow is one key's distribution and verdict. Live is the configured percentile
// measured today; Value and Source are what Effective enforces. Propose is zero, or a
// tighter value to commit: it is never looser than Value. Known counts the accepted items
// past Value; Violations are the rest.
type ConformRow struct {
	Key        ConformKey    `json:"key"        yaml:"key"`
	N          int           `json:"n"          yaml:"n"`
	P50        int           `json:"p50"        yaml:"p50"`
	P90        int           `json:"p90"        yaml:"p90"`
	P95        int           `json:"p95"        yaml:"p95"`
	P99        int           `json:"p99"        yaml:"p99"`
	Live       int           `json:"live"       yaml:"live"`
	Value      int           `json:"value"      yaml:"value"`
	Anchor     int           `json:"anchor"     yaml:"anchor"`
	Propose    int           `json:"propose"    yaml:"propose"`
	Known      int           `json:"known"      yaml:"known"`
	Source     ConformSource `json:"source"     yaml:"source"`
	Violations []ConformItem `json:"violations" yaml:"violations"`
}

// ConformItem is one file, symbol, package or clone group past its row's value and not
// known. Source is its path:line.
type ConformItem struct {
	ID      string `json:"id"      yaml:"id"`
	Source  string `json:"source"  yaml:"source"`
	Measure int    `json:"measure" yaml:"measure"`
}
