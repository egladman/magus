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
		RatchetPath: "hack/policy/conform.json",
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
	assert.NoError(t, o.Validate(), "no ratchet configured: anchors only, and no schema to check")

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
			"hack/policy/conform.json:4: file-lines source: committed 2201 is looser than its anchor 2200",
		},
		"a scoped value looser than the unscoped anchor": {
			func(o *ConformOptions) {
				o.Ratchet.Thresholds = append(o.Ratchet.Thresholds, ConformThreshold{
					ConformKey: ConformKey{Metric: ConformDepFanout, Population: ConformPopSource, Scope: "cmd/magus"}, Value: 89, Line: 5,
				})
			},
			"hack/policy/conform.json:5: dep-fanout source cmd/magus: committed 89 is looser than its anchor 62",
		},
		"no anchor to bound it": {
			func(o *ConformOptions) {
				o.Ratchet.Thresholds[0].ConformKey = ConformKey{Metric: ConformTypeMethods, Population: ConformPopSource}
			},
			"hack/policy/conform.json:4: type-methods source: no anchor is configured to bound the committed 938",
		},
		"unknown metric": {
			func(o *ConformOptions) { o.Ratchet.Thresholds[0].Metric = "lines" },
			"hack/policy/conform.json:4: lines source: metric lines: want one of " + metrics,
		},
		"no metric": {
			func(o *ConformOptions) { o.Ratchet.Thresholds[0].Metric = "" },
			"hack/policy/conform.json:4:  source: metric unset: want one of " + metrics,
		},
		"unknown population": {
			func(o *ConformOptions) { o.Ratchet.Thresholds[0].Population = "vendor" },
			"hack/policy/conform.json:4: file-lines vendor: population vendor: want one of [source main test]",
		},
		"committed twice": {
			func(o *ConformOptions) {
				o.Ratchet.Thresholds = append(o.Ratchet.Thresholds, ConformThreshold{ConformKey: fileLinesSource, Value: 900, Line: 9})
			},
			"hack/policy/conform.json:9: file-lines source: committed twice, first at line 4",
		},
		"negative value": {
			func(o *ConformOptions) { o.Ratchet.Thresholds[0].Value = -1 },
			"hack/policy/conform.json:4: file-lines source: value -1: want at least 0",
		},
		"schema other than 1": {
			func(o *ConformOptions) { o.Ratchet.Schema = 2 },
			"hack/policy/conform.json: schema 2: want 1",
		},
		"a configured ratchet with no schema": {
			func(o *ConformOptions) { o.Ratchet = ConformRatchet{} },
			"hack/policy/conform.json: schema 0: want 1",
		},
		"known with an unknown metric": {
			func(o *ConformOptions) { o.Ratchet.Known[0].Metric = "clones" },
			"hack/policy/conform.json:12: known: metric clones: want one of " + metrics,
		},
		"known with no items": {
			func(o *ConformOptions) { o.Ratchet.Known[0].Items = nil },
			"hack/policy/conform.json:12: known clone-tokens: no items",
		},
		"known with no reason": {
			func(o *ConformOptions) { o.Ratchet.Known[0].Reason = "" },
			"hack/policy/conform.json:12: known clone-tokens: no reason",
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
		"hack/policy/conform.json:4: file-lines source: committed 3000 is looser than its anchor 2200")
}

const conformRecord = `{"schema": 1, "measured": {"rev": "abc123", "date": "2026-10-14"},
 "thresholds": [
  {"metric": "file-lines", "population": "source", "value": 938, "n": 1130, "percentile": 95, "why": "p95 at abc123"},
  {"metric": "dep-fanout", "population": "source", "scope": "cmd/magus", "value": 89, "n": 1, "percentile": 0, "why": "was importceiling"}],
 "known": [{"metric": "clone-tokens", "items": ["vcs/hg.go#hgVCS.Bisect", "vcs/sapling.go#saplingVCS.Bisect"], "reason": "driver twins"}]}
`

func TestReadConformRatchet(t *testing.T) {
	t.Parallel()

	r, err := ReadConformRatchet([]byte(conformRecord))
	require.NoError(t, err)
	assert.Equal(t, ConformRatchet{
		Schema:   1,
		Measured: ConformProvenance{Rev: "abc123", Date: "2026-10-14"},
		Thresholds: []ConformThreshold{
			{ConformKey: fileLinesSource, Value: 938, N: 1130, Percentile: 95, Why: "p95 at abc123", Line: 3},
			{
				ConformKey: ConformKey{Metric: ConformDepFanout, Population: ConformPopSource, Scope: "cmd/magus"},
				Value:      89, N: 1, Why: "was importceiling", Line: 4,
			},
		},
		Known: []ConformKnown{{
			Metric: ConformCloneTokens, Items: []string{"vcs/hg.go#hgVCS.Bisect", "vcs/sapling.go#saplingVCS.Bisect"},
			Reason: "driver twins", Line: 5,
		}},
	}, r)
}

func TestReadConformRatchetIsStrict(t *testing.T) {
	t.Parallel()

	// cause is the decoder's own wording, which want does not pin.
	for name, tc := range map[string]struct{ in, want, cause string }{
		"unknown field": {
			in:   "{\"schema\": 1,\n \"extra\": true}",
			want: `line 2: unknown field "extra"`,
		},
		"unknown field in an entry": {
			in:    "{\"schema\": 1, \"thresholds\": [\n{\"metric\": \"file-lines\", \"population\": \"source\", \"line\": 3}]}",
			want:  "line 2: ",
			cause: `unknown field "line"`,
		},
		"unknown field in measured": {
			in:    `{"schema": 1, "measured": {"rev": "a", "by": "me"}}`,
			want:  "line 1: measured: ",
			cause: `unknown field "by"`,
		},
		"duplicate key": {
			in:   "{\"schema\": 1,\n \"schema\": 1}",
			want: `line 2: duplicate key "schema"`,
		},
		"duplicate key in an entry": {
			in:   "{\"schema\": 1, \"known\": [\n{\"metric\": \"clone-tokens\",\n \"metric\": \"func-lines\"}]}",
			want: `line 3: duplicate key "metric"`,
		},
		"schema other than 1": {
			in:   "{\"measured\": {},\n \"schema\": 2}",
			want: "line 2: schema 2: want 1",
		},
		"no schema": {
			in:   `{"thresholds": []}`,
			want: "ratchet has no schema: want 1",
		},
		"trailing data": {
			in:   "{\"schema\": 1}\n{}",
			want: "line 2: trailing data after the ratchet",
		},
		"schema of the wrong type": {
			in:    `{"schema": "1"}`,
			want:  "line 1: schema: ",
			cause: "unmarshal JSON string into Go int", // encoding/json/v2 picks "cannot" or "unable to" at random per process
		},
		"a list that is not one": {
			in:   `{"schema": 1, "known": {}}`,
			want: `line 1: known: want "[", got {`,
		},
		"not an object": {
			in:   `[]`,
			want: `line 1: ratchet: want "{", got [`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := ReadConformRatchet([]byte(tc.in))
			require.Error(t, err)
			if tc.cause == "" {
				assert.EqualError(t, err, tc.want)
				return
			}
			assert.True(t, strings.HasPrefix(err.Error(), tc.want), err.Error())
			assert.ErrorContains(t, err, tc.cause)
		})
	}
}
