package types

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	fileLinesSource = ConformKey{Metric: ConformFileLines, Population: ConformPopSource}
	depFanoutSource = ConformKey{Metric: ConformDepFanout, Population: ConformPopSource}
)

func conformOptions() ConformOptions {
	return ConformOptions{
		Limits: map[ConformKey]ConformLimit{
			fileLinesSource: {Percentile: 95, Anchor: 2200},
			depFanoutSource: {Percentile: 95, Anchor: 62},
		},
		Ratchet: ConformRatchet{
			Schema:     1,
			Thresholds: []ConformThreshold{{ConformKey: fileLinesSource, Value: 938, N: 1130, Percentile: 95, Line: 4}},
			Known:      []ConformKnown{{Metric: ConformCloneTokens, Items: []string{"a.go#F", "b.go#F"}, Reason: "twins", Line: 12}},
		},
		RatchetPath: "hack/policy/conform.tsv",
		MinSample:   30,
		Step:        0.05,
		Gates:       ConformGates{MinCohort: 5, MinShare: 0.8, Bar: 0.5, MaxFindings: 5},
	}
}

func TestConformEffective(t *testing.T) {
	t.Parallel()

	o := conformOptions()
	scoped := ConformKey{Metric: ConformDepFanout, Population: ConformPopSource, Scope: "cmd/magus"}
	layer := ConformKey{Metric: ConformDepFanout, Population: ConformPopSource, Scope: "internal"}
	o.Ratchet.Thresholds = append(o.Ratchet.Thresholds, ConformThreshold{ConformKey: scoped, Value: 40})

	type got struct {
		Value  int
		Source ConformSource
	}
	effective := func(k ConformKey) got {
		v, src := o.Effective(k)
		return got{v, src}
	}
	assert.Equal(t, got{938, ConformFromRatchet}, effective(fileLinesSource), "a committed value beats the anchor")
	assert.Equal(t, got{62, ConformFromAnchor}, effective(depFanoutSource), "nothing committed: the anchor")
	assert.Equal(t, got{40, ConformFromRatchet}, effective(scoped), "the scope's own committed value")
	assert.Equal(t, got{62, ConformFromAnchor}, effective(layer), "a scope with nothing of its own falls back to the unscoped key")
	assert.Equal(t, got{}, effective(ConformKey{Metric: ConformTypeMethods, Population: ConformPopTest}), "neither is configured")

	o.Limits[layer] = ConformLimit{Anchor: 30}
	o.Ratchet.Thresholds = append(o.Ratchet.Thresholds, ConformThreshold{ConformKey: depFanoutSource, Value: 17})
	assert.Equal(t, got{30, ConformFromAnchor}, effective(layer), "the scope's own anchor beats the unscoped committed value")
	assert.Equal(t, got{17, ConformFromRatchet}, effective(ConformKey{Metric: ConformDepFanout, Population: ConformPopSource, Scope: "std"}))
}

func TestConformValidateAcceptsAConfiguredPolicy(t *testing.T) {
	t.Parallel()

	require.NoError(t, conformOptions().Validate())

	o := conformOptions()
	o.RatchetPath, o.Ratchet = "", ConformRatchet{}
	require.NoError(t, o.Validate(), "no ratchet configured: anchors only, and no schema to check")

	o = conformOptions()
	o.Ratchet.Thresholds[0].Value = 2200
	assert.NoError(t, o.Validate(), "a committed value equal to its anchor is not looser")
}

func TestConformValidateRefuses(t *testing.T) {
	t.Parallel()

	metrics := "[file-lines func-lines type-methods file-symbols dep-fanout clone-tokens export-scope " +
		"call-sequence shared-body shared-reach caller-spread mode-knobs alias-share name-clash]"
	for name, tc := range map[string]struct {
		edit func(*ConformOptions)
		want string
	}{
		"looser than its anchor": {
			func(o *ConformOptions) { o.Ratchet.Thresholds[0].Value = 2201 },
			"hack/policy/conform.tsv:4: file-lines source: committed 2201 is looser than its anchor 2200",
		},
		"a scoped value looser than the unscoped anchor": {
			func(o *ConformOptions) {
				o.Ratchet.Thresholds = append(o.Ratchet.Thresholds, ConformThreshold{
					ConformKey: ConformKey{Metric: ConformDepFanout, Population: ConformPopSource, Scope: "cmd/magus"}, Value: 89, Line: 5,
				})
			},
			"hack/policy/conform.tsv:5: dep-fanout source cmd/magus: committed 89 is looser than its anchor 62",
		},
		"no anchor to bound it": {
			func(o *ConformOptions) {
				o.Ratchet.Thresholds[0].ConformKey = ConformKey{Metric: ConformTypeMethods, Population: ConformPopSource}
			},
			"hack/policy/conform.tsv:4: type-methods source: no anchor is configured to bound the committed 938",
		},
		"unknown metric": {
			func(o *ConformOptions) { o.Ratchet.Thresholds[0].Metric = "lines" },
			"hack/policy/conform.tsv:4: lines source: metric lines: want one of " + metrics,
		},
		"no metric": {
			func(o *ConformOptions) { o.Ratchet.Thresholds[0].Metric = "" },
			"hack/policy/conform.tsv:4:  source: metric unset: want one of " + metrics,
		},
		"unknown population": {
			func(o *ConformOptions) { o.Ratchet.Thresholds[0].Population = "vendor" },
			"hack/policy/conform.tsv:4: file-lines vendor: population vendor: want one of [source main test]",
		},
		"committed twice": {
			func(o *ConformOptions) {
				o.Ratchet.Thresholds = append(o.Ratchet.Thresholds, ConformThreshold{ConformKey: fileLinesSource, Value: 900, Line: 9})
			},
			"hack/policy/conform.tsv:9: file-lines source: committed twice, first at line 4",
		},
		"negative value": {
			func(o *ConformOptions) { o.Ratchet.Thresholds[0].Value = -1 },
			"hack/policy/conform.tsv:4: file-lines source: value -1: want at least 0",
		},
		"schema other than 1": {
			func(o *ConformOptions) { o.Ratchet.Schema = 2 },
			"hack/policy/conform.tsv: schema 2: want 1",
		},
		"a configured ratchet with no schema": {
			func(o *ConformOptions) { o.Ratchet = ConformRatchet{} },
			"hack/policy/conform.tsv: schema 0: want 1",
		},
		"known with an unknown metric": {
			func(o *ConformOptions) { o.Ratchet.Known[0].Metric = "clones" },
			"hack/policy/conform.tsv:12: known: metric clones: want one of " + metrics,
		},
		"known with no items": {
			func(o *ConformOptions) { o.Ratchet.Known[0].Items = nil },
			"hack/policy/conform.tsv:12: known clone-tokens: no items",
		},
		"known with no reason": {
			func(o *ConformOptions) { o.Ratchet.Known[0].Reason = "" },
			"hack/policy/conform.tsv:12: known clone-tokens: no reason",
		},
		"limit with an unknown metric": {
			func(o *ConformOptions) {
				o.Limits[ConformKey{Metric: "bogus", Population: ConformPopSource}] = ConformLimit{Anchor: 1}
			},
			"limit bogus source: metric bogus: want one of " + metrics,
		},
		"limit with an unknown population": {
			func(o *ConformOptions) {
				o.Limits[ConformKey{Metric: ConformFileLines, Population: "bogus"}] = ConformLimit{Anchor: 1}
			},
			"limit file-lines bogus: population bogus: want one of [source main test]",
		},
		"percentile past 100": {
			func(o *ConformOptions) { o.Limits[fileLinesSource] = ConformLimit{Percentile: 101, Anchor: 2200} },
			"limit file-lines source: percentile 101: want 0 through 100",
		},
		"negative anchor": {
			func(o *ConformOptions) {
				o.Limits[depFanoutSource] = ConformLimit{Percentile: 95, Anchor: -1}
			},
			"limit dep-fanout source: anchor -1: want at least 0",
		},
		"min_sample below 1": {
			func(o *ConformOptions) { o.MinSample = 0 },
			"min_sample 0: want at least 1",
		},
		"step of 1": {
			func(o *ConformOptions) { o.Step = 1 },
			"step 1: want at least 0 and below 1",
		},
		"negative step": {
			func(o *ConformOptions) { o.Step = -0.05 },
			"step -0.05: want at least 0 and below 1",
		},
		"min_cohort below 1": {
			func(o *ConformOptions) { o.Gates.MinCohort = 0 },
			"gates.min_cohort 0: want at least 1",
		},
		"min_share past 1": {
			func(o *ConformOptions) { o.Gates.MinShare = 80 },
			"gates.min_share 80: want 0 through 1",
		},
		"negative bar": {
			func(o *ConformOptions) { o.Gates.Bar = -0.5 },
			"gates.bar -0.5: want 0 through 1",
		},
		"negative max_findings": {
			func(o *ConformOptions) { o.Gates.MaxFindings = -1 },
			"gates.max_findings -1: want at least 0",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			o := conformOptions()
			tc.edit(&o)
			assert.EqualError(t, o.Validate(), tc.want)
		})
	}
}

func TestConformValidateReportsEveryProblem(t *testing.T) {
	t.Parallel()

	o := conformOptions()
	o.MinSample = 0
	o.Ratchet.Thresholds[0].Value = 3000
	assert.EqualError(t, o.Validate(), "min_sample 0: want at least 1\n"+
		"hack/policy/conform.tsv:4: file-lines source: committed 3000 is looser than its anchor 2200")
}

// conformRecord is a ratchet file from rows given tab-separated by \t, one per line.
func conformRecord(rows ...string) []byte {
	return []byte(strings.Join(rows, "\n") + "\n")
}

func TestReadConformRatchet(t *testing.T) {
	t.Parallel()

	r, err := ReadConformRatchet(conformRecord(
		"# accepted from the proposal at abc123",
		"schema\t1",
		"measured\tabc123\t2026-10-14",
		"",
		"threshold\tfile-lines\tsource\t-\t938\t1130\t95\tp95 at abc123",
		"threshold\tdep-fanout\tsource\tcmd/magus\t89\t1\t0",
		"known\tclone-tokens\tdriver twins\tvcs/hg.go#hgVCS.Bisect\tvcs/sapling.go#saplingVCS.Bisect",
	))
	require.NoError(t, err)
	assert.Equal(t, ConformRatchet{
		Schema:   1,
		Measured: ConformProvenance{Rev: "abc123", Date: "2026-10-14"},
		Thresholds: []ConformThreshold{
			{ConformKey: fileLinesSource, Value: 938, N: 1130, Percentile: 95, Why: "p95 at abc123", Line: 5},
			{
				ConformKey: ConformKey{Metric: ConformDepFanout, Population: ConformPopSource, Scope: "cmd/magus"},
				Value:      89, N: 1, Line: 6,
			},
		},
		Known: []ConformKnown{{
			Metric: ConformCloneTokens, Items: []string{"vcs/hg.go#hgVCS.Bisect", "vcs/sapling.go#saplingVCS.Bisect"},
			Reason: "driver twins", Line: 7,
		}},
	}, r)
}

func TestReadConformRatchetIsStrict(t *testing.T) {
	t.Parallel()

	const threshold = "threshold\tfile-lines\tsource\t-\t938\t1130\t95\tp95"
	for name, tc := range map[string]struct{ in, want string }{
		"unknown row kind": {
			in:   "schema\t1\nextra\tx",
			want: `line 2: unknown row kind "extra": want schema, measured, threshold or known`,
		},
		"the JSON document the format replaced": {
			in:   "{\"schema\": 1,\n \"thresholds\": []}",
			want: `line 1: unknown row kind "{\"schema\": 1,": want schema, measured, threshold or known; a JSON document is not the ratchet format`,
		},
		"unknown metric": {
			in:   "schema\t1\nthreshold\tlines\tsource\t-\t938\t1130\t95\tp95",
			want: "line 2: threshold lines source: metric lines: want one of [file-lines ",
		},
		"unknown population": {
			in:   "schema\t1\nthreshold\tfile-lines\tvendor\t-\t938\t1130\t95\tp95",
			want: "line 2: threshold file-lines vendor: population vendor: want one of [source main test]",
		},
		"value that is not an integer": {
			in:   "schema\t1\nthreshold\tfile-lines\tsource\t-\t9.5\t1130\t95\tp95",
			want: `line 2: threshold file-lines source: value "9.5": want an integer`,
		},
		"n that is not an integer": {
			in:   "schema\t1\nthreshold\tfile-lines\tsource\t-\t938\tmany\t95\tp95",
			want: `line 2: threshold file-lines source: n "many": want an integer`,
		},
		"percentile that is not a number": {
			in:   "schema\t1\nthreshold\tfile-lines\tsource\t-\t938\t1130\tNaN\tp95",
			want: `line 2: threshold file-lines source: percentile "NaN": want a number`,
		},
		"threshold with too few columns": {
			in:   "schema\t1\nthreshold\tfile-lines\tsource\t-\t938\t1130",
			want: "line 2: threshold: 6 columns, want 7 to 8",
		},
		"threshold with too many columns": {
			in:   "schema\t1\n" + threshold + "\tgone\tstale",
			want: "line 2: threshold: 10 columns, want 7 to 8",
		},
		"key committed twice": {
			in:   "schema\t1\n" + threshold + "\nthreshold\tfile-lines\tsource\t-\t900\t1130\t95\tp95",
			want: "line 3: threshold file-lines source: committed twice, first at line 2",
		},
		"row repeated": {
			in:   "schema\t1\n" + threshold + "\n" + threshold,
			want: "line 3: same row as line 2",
		},
		"known with no item": {
			in:   "schema\t1\nknown\tclone-tokens\ttwins",
			want: "line 2: known: 3 columns, want 4 or more",
		},
		"known with an unknown metric": {
			in:   "schema\t1\nknown\tclones\ttwins\ta.go#F",
			want: "line 2: known: metric clones: want one of [file-lines ",
		},
		"measured with too few columns": {
			in:   "schema\t1\nmeasured\tabc123",
			want: "line 2: measured: 2 columns, want 3",
		},
		"measured twice": {
			in:   "schema\t1\nmeasured\ta\tb\nmeasured\tc\td",
			want: "line 3: measured: given twice, first at line 2",
		},
		"schema other than 1": {
			in:   "# note\nschema\t2",
			want: "line 2: schema 2: want 1",
		},
		"schema that is not an integer": {
			in:   "schema\tone",
			want: `line 1: schema "one": want an integer`,
		},
		"schema twice": {
			in:   "schema\t1\nschema\t2",
			want: "line 2: schema: given twice, first at line 1",
		},
		"no schema": {
			in:   threshold,
			want: "ratchet has no schema row: want schema<TAB>1",
		},
		"empty file": {
			in:   "",
			want: "ratchet has no schema row: want schema<TAB>1",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := ReadConformRatchet([]byte(tc.in))
			require.Error(t, err)
			assert.ErrorContains(t, err, tc.want)
		})
	}
}
