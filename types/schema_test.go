package types

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/json"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// schemaGolden is one frozen field set, in
// testdata/schema/<name>.<version>.json. A golden is never edited after
// it lands, so a type change shows.
type schemaGolden struct {
	Fields   map[string]string `json:"fields"`
	Reserved []string          `json:"reserved"`
}

// TestFormatLedgersEvolveAdditively holds every versioned record to the contract
// FormatLedger states, against its frozen goldens. buf's WIRE_JSON rules, for JSON names.
func TestFormatLedgersEvolveAdditively(t *testing.T) {
	t.Parallel()

	for _, l := range FormatLedgers {
		t.Run(l.Name, func(t *testing.T) {
			t.Parallel()
			goldens := loadGoldens(t, l)
			assert.Empty(t, ledgerViolations(l, goldens))
		})
	}
}

func loadGoldens(t *testing.T, l FormatLedger) map[APIVersion]schemaGolden {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join("testdata", "schema", l.Name+".v*.json"))
	require.NoError(t, err)
	version := regexp.MustCompile(`\.(v[0-9a-z]+)\.json$`)
	out := map[APIVersion]schemaGolden{}
	for _, p := range paths {
		m := version.FindStringSubmatch(p)
		require.NotNil(t, m, "%s is not named <record>.<version>.json", p)
		v, err := ParseAPIVersion(strings.Replace(string(l.Version), l.Version.Version(), m[1], 1))
		require.NoError(t, err, p)
		raw, err := os.ReadFile(p)
		require.NoError(t, err)
		var g schemaGolden
		require.NoError(t, json.UnmarshalStrict(raw, &g), p)
		out[v] = g
	}
	return out
}

var packageQualifier = regexp.MustCompile(`\b[a-z][a-z0-9_]*\.`)

// wireType drops the pointer and every package qualifier, so a type that moves
// between packages is the same wire type and []types.Goal stays []Goal.
func wireType(t string) string {
	return packageQualifier.ReplaceAllString(strings.TrimPrefix(t, "*"), "")
}

// ledgerViolations is every way l's type breaks the contract against goldens.
func ledgerViolations(l FormatLedger, goldens map[APIVersion]schemaGolden) []string {
	var out []string
	add := func(format string, args ...any) {
		out = append(out, fmt.Sprintf("%s: %s", l.Name, fmt.Sprintf(format, args...)))
	}

	current, hasBag := schemaFields(l.Type)
	if !hasBag {
		add("%s does not embed types.Schema, so a rewrite drops the members it does not declare", l.Type)
	}
	versions := slices.SortedFunc(maps.Keys(goldens), APIVersion.Compare)
	if len(versions) == 0 || versions[len(versions)-1] != l.Version {
		body, _ := json.MarshalIndent(schemaGolden{Fields: current, Reserved: l.Reserved}, "", "  ")
		add("no frozen field set for %s; commit testdata/schema/%s.%s.json as\n%s", l.Version, l.Name, l.Version.Version(), body)
		return out
	}
	latest := goldens[l.Version]

	for _, v := range versions {
		g := goldens[v]
		for _, name := range slices.Sorted(maps.Keys(g.Fields)) {
			typ, ok := current[name]
			switch {
			case !ok && !slices.Contains(l.Reserved, name):
				add("%q (in %s) is gone; a removed name goes into Reserved so it is never reused", name, v)
			case ok && wireType(typ) != wireType(g.Fields[name]):
				add("%q was %s in %s and is %s now; a new type is a new name", name, g.Fields[name], v, typ)
			}
		}
		for _, name := range g.Reserved {
			if !slices.Contains(l.Reserved, name) {
				add("%q was reserved in %s and is not now; Reserved only grows", name, v)
			}
		}
	}
	for _, name := range l.Reserved {
		if _, ok := current[name]; ok {
			add("%q is reserved and declared again; pick a new name", name)
		}
	}
	for _, name := range slices.Sorted(maps.Keys(current)) {
		if _, frozen := latest.Fields[name]; frozen {
			continue
		}
		if l.Added[name] != l.Version {
			add("%q is not in %s; record it in Added as %q: %q, and leave the version where it is", name, l.Version, name, l.Version)
		}
	}
	for _, name := range slices.Sorted(maps.Keys(l.Added)) {
		_, declared := current[name]
		_, frozen := latest.Fields[name]
		if !declared || frozen {
			add("Added names %q, which is not a field added since %s", name, l.Version)
		}
	}
	for i := 1; i < len(versions); i++ {
		prev, next := versions[i-1], versions[i]
		brings := slices.Contains(slices.Collect(maps.Values(l.Requires)), next)
		if !brings && additive(goldens[prev], goldens[next]) {
			add("%s only adds to %s; leave the version at %s and put the new names in Added,"+
				" or name the feature an older reader lacks in Requires", next, prev, prev)
		}
	}
	return out
}

// additive reports whether next keeps every name prev declared, at the same type.
func additive(prev, next schemaGolden) bool {
	for name, typ := range prev.Fields {
		if got, ok := next.Fields[name]; !ok || strings.TrimPrefix(got, "*") != strings.TrimPrefix(typ, "*") {
			return false
		}
	}
	return true
}

// schemaFields maps each json name t declares to its Go type, embedded structs inlined as
// json inlines them, and reports whether t carries an unknown-members bag.
func schemaFields(t reflect.Type) (map[string]string, bool) {
	out := map[string]string{}
	hasBag := false
	for _, f := range reflect.VisibleFields(t) {
		tag := f.Tag.Get("json")
		name, opts, _ := strings.Cut(tag, ",")
		switch {
		case !f.IsExported(), f.Anonymous, name == "-":
		case opts == "unknown":
			hasBag = true
		case name == "":
			out[f.Name] = f.Type.String()
		default:
			out[name] = f.Type.String()
		}
	}
	return out, hasBag
}

const (
	ledgerV1 APIVersion = "magus.r/v1alpha1"
	ledgerV2 APIVersion = "magus.r/v1alpha2"
)

// TestLedgerViolations proves each rule fires on the change it exists for, and stays quiet
// on the ones the contract allows.
func TestLedgerViolations(t *testing.T) {
	t.Parallel()

	type base struct {
		Schema
		A string `json:"a"`
		B []int  `json:"b,omitempty"`
	}
	type added struct {
		Schema
		A string `json:"a"`
		B []int  `json:"b,omitempty"`
		C bool   `json:"c,omitempty"`
	}
	type removed struct {
		Schema
		A string `json:"a"`
	}
	type retyped struct {
		Schema
		A int   `json:"a"`
		B []int `json:"b,omitempty"`
	}
	type pointered struct {
		Schema
		A *string `json:"a"`
		B []int   `json:"b,omitempty"`
	}
	type bagless struct {
		Version APIVersion `json:"api_version"`
		A       string     `json:"a"`
		B       []int      `json:"b,omitempty"`
	}
	fields := func(extra ...string) map[string]string {
		m := map[string]string{"api_version": "types.APIVersion", "requires": "[]string", "a": "string", "b": "[]int"}
		for i := 0; i+1 < len(extra); i += 2 {
			m[extra[i]] = extra[i+1]
		}
		return m
	}
	v1 := map[APIVersion]schemaGolden{ledgerV1: {Fields: fields()}}

	for name, tc := range map[string]struct {
		ledger  FormatLedger
		goldens map[APIVersion]schemaGolden
		want    []string
	}{
		"unchanged": {
			ledger:  FormatLedger{Name: "r", Type: reflect.TypeFor[base](), Version: ledgerV1},
			goldens: v1,
		},
		"an added field recorded in Added": {
			ledger:  FormatLedger{Name: "r", Type: reflect.TypeFor[added](), Version: ledgerV1, Added: map[string]APIVersion{"c": ledgerV1}},
			goldens: v1,
		},
		"an added field left out of Added": {
			ledger:  FormatLedger{Name: "r", Type: reflect.TypeFor[added](), Version: ledgerV1},
			goldens: v1,
			want:    []string{`r: "c" is not in magus.r/v1alpha1; record it in Added as "c": "magus.r/v1alpha1", and leave the version where it is`},
		},
		"a removed field that is reserved": {
			ledger:  FormatLedger{Name: "r", Type: reflect.TypeFor[removed](), Version: ledgerV1, Reserved: []string{"b"}},
			goldens: v1,
		},
		"a removed field left unreserved": {
			ledger:  FormatLedger{Name: "r", Type: reflect.TypeFor[removed](), Version: ledgerV1},
			goldens: v1,
			want:    []string{`r: "b" (in magus.r/v1alpha1) is gone; a removed name goes into Reserved so it is never reused`},
		},
		"a changed type": {
			ledger:  FormatLedger{Name: "r", Type: reflect.TypeFor[retyped](), Version: ledgerV1},
			goldens: v1,
			want:    []string{`r: "a" was string in magus.r/v1alpha1 and is int now; a new type is a new name`},
		},
		"a value made a pointer": {
			ledger:  FormatLedger{Name: "r", Type: reflect.TypeFor[pointered](), Version: ledgerV1},
			goldens: v1,
		},
		"a reserved name declared again": {
			ledger:  FormatLedger{Name: "r", Type: reflect.TypeFor[base](), Version: ledgerV1, Reserved: []string{"a"}},
			goldens: v1,
			want:    []string{`r: "a" is reserved and declared again; pick a new name`},
		},
		"a reservation dropped": {
			ledger:  FormatLedger{Name: "r", Type: reflect.TypeFor[base](), Version: ledgerV1},
			goldens: map[APIVersion]schemaGolden{ledgerV1: {Fields: fields(), Reserved: []string{"old"}}},
			want:    []string{`r: "old" was reserved in magus.r/v1alpha1 and is not now; Reserved only grows`},
		},
		"a bump that only adds": {
			ledger:  FormatLedger{Name: "r", Type: reflect.TypeFor[added](), Version: ledgerV2},
			goldens: map[APIVersion]schemaGolden{ledgerV1: {Fields: fields()}, ledgerV2: {Fields: fields("c", "bool")}},
			want: []string{`r: magus.r/v1alpha2 only adds to magus.r/v1alpha1; leave the version at magus.r/v1alpha1 and put the new names in Added,` +
				` or name the feature an older reader lacks in Requires`},
		},
		"a bump that names the feature it breaks": {
			ledger:  FormatLedger{Name: "r", Type: reflect.TypeFor[added](), Version: ledgerV2, Requires: map[string]APIVersion{"c-means-d": ledgerV2}},
			goldens: map[APIVersion]schemaGolden{ledgerV1: {Fields: fields()}, ledgerV2: {Fields: fields("c", "bool")}},
		},
		"a bump that removes a reserved name": {
			ledger: FormatLedger{Name: "r", Type: reflect.TypeFor[removed](), Version: ledgerV2, Reserved: []string{"b"}},
			goldens: map[APIVersion]schemaGolden{ledgerV1: {Fields: fields()}, ledgerV2: {
				Fields: map[string]string{"api_version": "types.APIVersion", "requires": "[]string", "a": "string"}, Reserved: []string{"b"},
			}},
		},
		"a version with no frozen field set": {
			ledger:  FormatLedger{Name: "r", Type: reflect.TypeFor[base](), Version: ledgerV2},
			goldens: v1,
			want: []string{"r: no frozen field set for magus.r/v1alpha2; commit testdata/schema/r.v1alpha2.json as\n" +
				"{\n  \"fields\": {\n    \"a\": \"string\",\n    \"api_version\": \"types.APIVersion\",\n    \"b\": \"[]int\",\n    \"requires\": \"[]string\"\n  },\n  \"reserved\": null\n}"},
		},
		"a type without the bag": {
			ledger:  FormatLedger{Name: "r", Type: reflect.TypeFor[bagless](), Version: ledgerV1},
			goldens: map[APIVersion]schemaGolden{ledgerV1: {Fields: map[string]string{"api_version": "types.APIVersion", "a": "string", "b": "[]int"}}},
			want:    []string{"r: types.bagless does not embed types.Schema, so a rewrite drops the members it does not declare"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, ledgerViolations(tc.ledger, tc.goldens))
		})
	}
}

// TestSchemaUnknownRoundTrips pins the json/v2 behavior the store relies on: an embedded
// Schema's bag collects the members the struct does not declare and writes them back.
func TestSchemaUnknownRoundTrips(t *testing.T) {
	t.Parallel()

	in := `{"api_version":"magus.job/v1alpha99","requires":["x"],"id":"a","future_field":{"x":1}}`
	var row Job
	require.NoError(t, json.Unmarshal([]byte(in), &row))
	// The bag's raw values are the decoder's own; its keys are checked below.
	assert.Equal(t, Job{Schema: Schema{Version: "magus.job/v1alpha99", Requires: []string{"x"}, Unknown: row.Unknown}, ID: "a"}, row)
	assert.Equal(t, []string{"future_field"}, slices.Collect(maps.Keys(row.Unknown)))

	out, err := json.Marshal(row.Clone())
	require.NoError(t, err)
	var back map[string]any
	require.NoError(t, json.Unmarshal(out, &back))
	assert.Equal(t, map[string]any{"x": float64(1)}, back["future_field"])
	assert.Equal(t, "magus.job/v1alpha99", back["api_version"])
}

func TestSchemaUnmet(t *testing.T) {
	t.Parallel()

	assert.Nil(t, Schema{}.Unmet(nil))
	assert.Nil(t, Schema{Requires: []string{"a"}}.Unmet([]string{"a", "b"}))
	assert.Equal(t, []string{"c", "d"}, Schema{Requires: []string{"c", "a", "d"}}.Unmet([]string{"a"}))
	assert.Equal(t, []string{"claim-declarations", "dead-job-end", "job-generations", "signature-goals"}, JobSchema.Features())
}
