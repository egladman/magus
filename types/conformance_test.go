package types

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	fileLinesSource = ConformanceKey{Metric: ConformanceFileLines, Population: ConformancePopSource}
	depFanoutSource = ConformanceKey{Metric: ConformanceDepFanout, Population: ConformancePopSource}
)

func conformanceOptions() ConformanceOptions {
	return ConformanceOptions{
		Limits: map[ConformanceKey]ConformanceLimit{
			fileLinesSource: {Percentile: 95, Anchor: 2200},
			depFanoutSource: {Percentile: 95, Anchor: 62},
		},
		Ratchet: ConformanceRatchet{
			Schema:     1,
			Thresholds: []ConformanceThreshold{{ConformanceKey: fileLinesSource, Value: 938, N: 1130, Percentile: 95, Line: 4}},
			Known:      []ConformanceKnown{{Metric: ConformanceCloneTokens, Items: []string{"a.go#F", "b.go#F"}, Reason: "twins", Line: 12}},
		},
		RatchetPath: "hack/policy/conformance.jsonl",
		MinSample:   30,
		Step:        0.05,
		Gates:       ConformanceGates{MinCohort: 5, MinShare: 0.8, Bar: 0.5, MaxFindings: 5},
	}
}

func TestConformanceEffective(t *testing.T) {
	t.Parallel()

	o := conformanceOptions()
	scoped := ConformanceKey{Metric: ConformanceDepFanout, Population: ConformancePopSource, Scope: "cmd/magus"}
	layer := ConformanceKey{Metric: ConformanceDepFanout, Population: ConformancePopSource, Scope: "internal"}
	o.Ratchet.Thresholds = append(o.Ratchet.Thresholds, ConformanceThreshold{ConformanceKey: scoped, Value: 40})

	type got struct {
		Value  int
		Source ConformanceSource
	}
	effective := func(k ConformanceKey) got {
		v, src := o.Effective(k)
		return got{v, src}
	}
	assert.Equal(t, got{938, ConformanceFromRatchet}, effective(fileLinesSource), "a committed value beats the anchor")
	assert.Equal(t, got{62, ConformanceFromAnchor}, effective(depFanoutSource), "nothing committed: the anchor")
	assert.Equal(t, got{40, ConformanceFromRatchet}, effective(scoped), "the scope's own committed value")
	assert.Equal(t, got{62, ConformanceFromAnchor}, effective(layer), "a scope with nothing of its own falls back to the unscoped key")
	assert.Equal(t, got{}, effective(ConformanceKey{Metric: ConformanceTypeMethods, Population: ConformancePopTest}), "neither is configured")

	o.Limits[layer] = ConformanceLimit{Anchor: 30}
	o.Ratchet.Thresholds = append(o.Ratchet.Thresholds, ConformanceThreshold{ConformanceKey: depFanoutSource, Value: 17})
	assert.Equal(t, got{30, ConformanceFromAnchor}, effective(layer), "the scope's own anchor beats the unscoped committed value")
	assert.Equal(t, got{17, ConformanceFromRatchet}, effective(ConformanceKey{Metric: ConformanceDepFanout, Population: ConformancePopSource, Scope: "std"}))
}

func TestConformanceValidateAcceptsAConfiguredPolicy(t *testing.T) {
	t.Parallel()

	require.NoError(t, conformanceOptions().Validate())

	o := conformanceOptions()
	o.RatchetPath, o.Ratchet = "", ConformanceRatchet{}
	require.NoError(t, o.Validate(), "no ratchet configured: anchors only, and no schema to check")

	o = conformanceOptions()
	o.Ratchet.Thresholds[0].Value = 2200
	require.NoError(t, o.Validate(), "a committed value equal to its anchor is not looser")

	o = conformanceOptions()
	o.Ratchet.Known = append(o.Ratchet.Known, ConformanceKnown{Metric: ConformanceFuncLines, Items: []string{"a.go#F"}, Reason: "long", Line: 13})
	assert.NoError(t, o.Validate(), "one item under two metrics")
}

func TestConformanceValidateRefuses(t *testing.T) {
	t.Parallel()

	metrics := "[file-lines func-lines type-methods file-symbols dep-fanout clone-tokens export-scope " +
		"call-sequence shared-body shared-reach caller-spread mode-knobs alias-share name-clash main-logic store-path-export]"
	for name, tc := range map[string]struct {
		edit func(*ConformanceOptions)
		want string
	}{
		"looser than its anchor": {
			func(o *ConformanceOptions) { o.Ratchet.Thresholds[0].Value = 2201 },
			"hack/policy/conformance.jsonl:4: file-lines source: committed 2201 is looser than its anchor 2200",
		},
		"a scoped value looser than the unscoped anchor": {
			func(o *ConformanceOptions) {
				o.Ratchet.Thresholds = append(o.Ratchet.Thresholds, ConformanceThreshold{
					ConformanceKey: ConformanceKey{Metric: ConformanceDepFanout, Population: ConformancePopSource, Scope: "cmd/magus"}, Value: 89, Line: 5,
				})
			},
			"hack/policy/conformance.jsonl:5: dep-fanout source cmd/magus: committed 89 is looser than its anchor 62",
		},
		"no anchor to bound it": {
			func(o *ConformanceOptions) {
				o.Ratchet.Thresholds[0].ConformanceKey = ConformanceKey{Metric: ConformanceTypeMethods, Population: ConformancePopSource}
			},
			"hack/policy/conformance.jsonl:4: type-methods source: no anchor is configured to bound the committed 938",
		},
		"unknown metric": {
			func(o *ConformanceOptions) { o.Ratchet.Thresholds[0].Metric = "lines" },
			"hack/policy/conformance.jsonl:4: lines source: metric lines: want one of " + metrics,
		},
		"no metric": {
			func(o *ConformanceOptions) { o.Ratchet.Thresholds[0].Metric = "" },
			"hack/policy/conformance.jsonl:4:  source: metric unset: want one of " + metrics,
		},
		"unknown population": {
			func(o *ConformanceOptions) { o.Ratchet.Thresholds[0].Population = "vendor" },
			"hack/policy/conformance.jsonl:4: file-lines vendor: population vendor: want one of [source main test]",
		},
		"committed twice": {
			func(o *ConformanceOptions) {
				o.Ratchet.Thresholds = append(o.Ratchet.Thresholds, ConformanceThreshold{ConformanceKey: fileLinesSource, Value: 900, Line: 9})
			},
			"hack/policy/conformance.jsonl:9: file-lines source: committed twice, first at line 4",
		},
		"negative value": {
			func(o *ConformanceOptions) { o.Ratchet.Thresholds[0].Value = -1 },
			"hack/policy/conformance.jsonl:4: file-lines source: value -1: want at least 0",
		},
		"schema other than 1": {
			func(o *ConformanceOptions) { o.Ratchet.Schema = 2 },
			"hack/policy/conformance.jsonl: schema 2: want 1",
		},
		"a configured ratchet with no schema": {
			func(o *ConformanceOptions) { o.Ratchet = ConformanceRatchet{} },
			"hack/policy/conformance.jsonl: schema 0: want 1",
		},
		"known with an unknown metric": {
			func(o *ConformanceOptions) { o.Ratchet.Known[0].Metric = "clones" },
			"hack/policy/conformance.jsonl:12: known: metric clones: want one of " + metrics,
		},
		"known with no items": {
			func(o *ConformanceOptions) { o.Ratchet.Known[0].Items = nil },
			"hack/policy/conformance.jsonl:12: known clone-tokens: no items",
		},
		"known with no reason": {
			func(o *ConformanceOptions) { o.Ratchet.Known[0].Reason = "" },
			"hack/policy/conformance.jsonl:12: known clone-tokens: no reason",
		},
		"known item listed twice under one metric": {
			func(o *ConformanceOptions) {
				o.Ratchet.Known = append(o.Ratchet.Known, ConformanceKnown{Metric: ConformanceCloneTokens, Items: []string{"c.go#F", "b.go#F"}, Reason: "again", Line: 13})
			},
			"hack/policy/conformance.jsonl:13: known clone-tokens: b.go#F listed twice, first at line 12",
		},
		"known with a shape for an item it does not list": {
			func(o *ConformanceOptions) { o.Ratchet.Known[0].Shapes = map[string]string{"c.go#F": "s1"} },
			"hack/policy/conformance.jsonl:12: known clone-tokens: a shape for c.go#F, which the entry does not list",
		},
		"known with an empty shape": {
			func(o *ConformanceOptions) { o.Ratchet.Known[0].Shapes = map[string]string{"a.go#F": ""} },
			"hack/policy/conformance.jsonl:12: known clone-tokens: an empty shape for a.go#F",
		},
		"limit with an unknown metric": {
			func(o *ConformanceOptions) {
				o.Limits[ConformanceKey{Metric: "bogus", Population: ConformancePopSource}] = ConformanceLimit{Anchor: 1}
			},
			"limit bogus source: metric bogus: want one of " + metrics,
		},
		"limit with an unknown population": {
			func(o *ConformanceOptions) {
				o.Limits[ConformanceKey{Metric: ConformanceFileLines, Population: "bogus"}] = ConformanceLimit{Anchor: 1}
			},
			"limit file-lines bogus: population bogus: want one of [source main test]",
		},
		"percentile past 100": {
			func(o *ConformanceOptions) {
				o.Limits[fileLinesSource] = ConformanceLimit{Percentile: 101, Anchor: 2200}
			},
			"limit file-lines source: percentile 101: want 0 through 100",
		},
		"negative anchor": {
			func(o *ConformanceOptions) {
				o.Limits[depFanoutSource] = ConformanceLimit{Percentile: 95, Anchor: -1}
			},
			"limit dep-fanout source: anchor -1: want at least 0",
		},
		"min_sample below 1": {
			func(o *ConformanceOptions) { o.MinSample = 0 },
			"min_sample 0: want at least 1",
		},
		"step of 1": {
			func(o *ConformanceOptions) { o.Step = 1 },
			"step 1: want at least 0 and below 1",
		},
		"negative step": {
			func(o *ConformanceOptions) { o.Step = -0.05 },
			"step -0.05: want at least 0 and below 1",
		},
		"min_cohort below 1": {
			func(o *ConformanceOptions) { o.Gates.MinCohort = 0 },
			"gates.min_cohort 0: want at least 1",
		},
		"min_share past 1": {
			func(o *ConformanceOptions) { o.Gates.MinShare = 80 },
			"gates.min_share 80: want 0 through 1",
		},
		"negative bar": {
			func(o *ConformanceOptions) { o.Gates.Bar = -0.5 },
			"gates.bar -0.5: want 0 through 1",
		},
		"negative max_findings": {
			func(o *ConformanceOptions) { o.Gates.MaxFindings = -1 },
			"gates.max_findings -1: want at least 0",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			o := conformanceOptions()
			tc.edit(&o)
			assert.EqualError(t, o.Validate(), tc.want)
		})
	}
}

func TestConformanceValidateReportsEveryProblem(t *testing.T) {
	t.Parallel()

	o := conformanceOptions()
	o.MinSample = 0
	o.Ratchet.Thresholds[0].Value = 3000
	assert.EqualError(t, o.Validate(), "min_sample 0: want at least 1\n"+
		"hack/policy/conformance.jsonl:4: file-lines source: committed 3000 is looser than its anchor 2200")
}

// conformanceRecord is a ratchet file from lines given as JSON objects, one per line.
func conformanceRecord(lines ...string) []byte {
	return []byte(strings.Join(lines, "\n") + "\n")
}

func TestReadConformanceRatchet(t *testing.T) {
	t.Parallel()

	r, err := ReadConformanceRatchet("r.jsonl", conformanceRecord(
		`{"kind":"schema","schema":1}`,
		`{"kind":"measured","rev":"abc123","date":"2026-10-14"}`,
		"",
		`{"kind":"threshold","metric":"file-lines","population":"source","value":938,"n":1130,"percentile":95,"why":"p95 at abc123"}`,
		`{"kind":"threshold","metric":"dep-fanout","population":"source","scope":"cmd/magus","value":89,"n":1,"percentile":0}`,
		`{"kind":"known","metric":"clone-tokens","reason":"driver twins","items":["vcs/hg.go#hgVCS.Bisect","vcs/sapling.go#saplingVCS.Bisect"]}`,
		`{"kind":"known","metric":"func-lines","reason":"long on purpose","items":["a.go#F"],"shapes":{"a.go#F":"5f3a"}}`,
	))
	require.NoError(t, err)
	assert.Equal(t, ConformanceRatchet{
		Schema:   1,
		Measured: ConformanceProvenance{Rev: "abc123", Date: "2026-10-14"},
		Thresholds: []ConformanceThreshold{
			{ConformanceKey: fileLinesSource, Value: 938, N: 1130, Percentile: 95, Why: "p95 at abc123", Line: 4},
			{
				ConformanceKey: ConformanceKey{Metric: ConformanceDepFanout, Population: ConformancePopSource, Scope: "cmd/magus"},
				Value:          89, N: 1, Line: 5,
			},
		},
		Known: []ConformanceKnown{{
			Metric: ConformanceCloneTokens, Items: []string{"vcs/hg.go#hgVCS.Bisect", "vcs/sapling.go#saplingVCS.Bisect"},
			Reason: "driver twins", Line: 6,
		}, {
			Metric: ConformanceFuncLines, Items: []string{"a.go#F"}, Reason: "long on purpose",
			Shapes: map[string]string{"a.go#F": "5f3a"}, Line: 7,
		}},
	}, r)
}

func TestReadConformanceRatchetIsStrict(t *testing.T) {
	t.Parallel()

	const (
		schema    = `{"kind":"schema","schema":1}`
		threshold = `{"kind":"threshold","metric":"file-lines","population":"source","value":938,"n":1130,"percentile":95}`
	)
	for name, tc := range map[string]struct{ in, want string }{
		"unknown kind": {
			in:   schema + "\n" + `{"kind":"extra"}`,
			want: `r.jsonl:2: kind "extra": want schema, measured, threshold or known`,
		},
		"no kind": {
			in:   schema + "\n" + `{"metric":"file-lines"}`,
			want: "r.jsonl:2: no kind: want schema, measured, threshold or known",
		},
		"a tab-separated row, the format before this one": {
			in:   "schema\t1",
			want: "r.jsonl:1:",
		},
		"a line that is not an object": {
			in:   schema + "\n[1]",
			want: "r.jsonl:2:",
		},
		"two objects on one line": {
			in:   schema + schema,
			want: "r.jsonl:1:",
		},
		"unknown member": {
			in:   schema + "\n" + `{"kind":"threshold","metric":"file-lines","population":"source","value":1,"n":1,"percentile":1,"bogus":1}`,
			want: `r.jsonl:2: threshold: unknown field "bogus"`,
		},
		"member of another kind": {
			in:   schema + "\n" + `{"kind":"threshold","metric":"file-lines","population":"source","value":1,"n":1,"percentile":1,"items":[]}`,
			want: `r.jsonl:2: threshold: unknown field "items"`,
		},
		"repeated member": {
			in:   schema + "\n" + `{"kind":"threshold","metric":"file-lines","population":"source","value":1,"value":2,"n":1,"percentile":1}`,
			want: "r.jsonl:2:",
		},
		"unknown metric": {
			in:   schema + "\n" + `{"kind":"threshold","metric":"lines","population":"source","value":1,"n":1,"percentile":1}`,
			want: "r.jsonl:2: threshold: metric lines: want one of [file-lines ",
		},
		"unknown population": {
			in:   schema + "\n" + `{"kind":"threshold","metric":"file-lines","population":"vendor","value":1,"n":1,"percentile":1}`,
			want: "r.jsonl:2: threshold: population vendor: want one of [source main test]",
		},
		"value that is not an integer": {
			in:   schema + "\n" + `{"kind":"threshold","metric":"file-lines","population":"source","value":9.5,"n":1,"percentile":1}`,
			want: "r.jsonl:2: threshold: ",
		},
		"n that is a string": {
			in:   schema + "\n" + `{"kind":"threshold","metric":"file-lines","population":"source","value":1,"n":"many","percentile":1}`,
			want: "r.jsonl:2: threshold: ",
		},
		"threshold missing members": {
			in:   schema + "\n" + `{"kind":"threshold","metric":"file-lines","population":"source","value":1}`,
			want: "r.jsonl:2: threshold: no n, percentile",
		},
		"key committed twice": {
			in:   schema + "\n" + threshold + "\n" + `{"kind":"threshold","metric":"file-lines","population":"source","value":900,"n":1130,"percentile":95}`,
			want: "r.jsonl:3: threshold file-lines source: committed twice, first at line 2",
		},
		"line repeated": {
			in:   schema + "\n" + threshold + "\n" + threshold,
			want: "r.jsonl:3: same line as line 2",
		},
		"known missing members": {
			in:   schema + "\n" + `{"kind":"known","metric":"clone-tokens"}`,
			want: "r.jsonl:2: known: no items, reason",
		},
		"known with an unknown metric": {
			in:   schema + "\n" + `{"kind":"known","metric":"clones","reason":"twins","items":["a.go#F"]}`,
			want: "r.jsonl:2: known: metric clones: want one of [file-lines ",
		},
		"measured missing members": {
			in:   schema + "\n" + `{"kind":"measured","rev":"abc123"}`,
			want: "r.jsonl:2: measured: no date",
		},
		"measured twice": {
			in:   schema + "\n" + `{"kind":"measured","rev":"a","date":"b"}` + "\n" + `{"kind":"measured","rev":"c","date":"d"}`,
			want: "r.jsonl:3: measured: given twice, first at line 2",
		},
		"schema other than 1": {
			in:   `{"kind":"schema","schema":2}`,
			want: "r.jsonl:1: schema: schema 2: want 1",
		},
		"schema missing its number": {
			in:   `{"kind":"schema"}`,
			want: "r.jsonl:1: schema: ",
		},
		"no schema": {
			in:   threshold,
			want: `ratchet has no schema line: want {"kind":"schema","schema":1}`,
		},
		"empty file": {
			in:   "",
			want: `ratchet has no schema line: want {"kind":"schema","schema":1}`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := ReadConformanceRatchet("r.jsonl", []byte(tc.in))
			require.Error(t, err)
			assert.ErrorContains(t, err, tc.want)
		})
	}
}

func TestReadConformanceRatchetRefusesASecondSchema(t *testing.T) {
	t.Parallel()

	_, err := ReadConformanceRatchet("r.jsonl", conformanceRecord(`{"kind":"schema","schema":1}`, `{"kind":"schema", "schema":1}`))
	assert.EqualError(t, err, "r.jsonl:2: schema row given twice, first at line 1")
}
