package types

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	json "github.com/egladman/magus/internal/json"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// schemaGolden is one frozen field set, testdata/schema/<name>.v<N>.json: every json name
// the type declared when version N was frozen, with its Go type, and the names reserved
// then. A golden is never edited after it lands; that is what lets a type change be seen.
type schemaGolden struct {
	Fields   map[string]string `json:"fields"`
	Reserved []string          `json:"reserved"`
}

// TestSchemaLedgersEvolveAdditively holds every versioned record to the contract
// SchemaLedger states, against its frozen goldens. buf's WIRE_JSON rules, for JSON names.
func TestSchemaLedgersEvolveAdditively(t *testing.T) {
	t.Parallel()

	for _, l := range SchemaLedgers {
		t.Run(l.Name, func(t *testing.T) {
			t.Parallel()
			goldens := loadGoldens(t, l.Name)
			assert.Empty(t, ledgerViolations(l, goldens))
		})
	}
}

func loadGoldens(t *testing.T, name string) map[int]schemaGolden {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join("testdata", "schema", name+".v*.json"))
	require.NoError(t, err)
	version := regexp.MustCompile(`\.v(\d+)\.json$`)
	out := map[int]schemaGolden{}
	for _, p := range paths {
		m := version.FindStringSubmatch(p)
		require.NotNil(t, m, "%s is not named <record>.v<N>.json", p)
		v, err := strconv.Atoi(m[1])
		require.NoError(t, err)
		raw, err := os.ReadFile(p)
		require.NoError(t, err)
		var g schemaGolden
		require.NoError(t, json.UnmarshalStrict(raw, &g), p)
		out[v] = g
	}
	return out
}

// ledgerViolations is every way l's type breaks the contract against goldens.
func ledgerViolations(l SchemaLedger, goldens map[int]schemaGolden) []string {
	var out []string
	add := func(format string, args ...any) { out = append(out, fmt.Sprintf(l.Name+": "+format, args...)) }

	current, hasBag := schemaFields(l.Type)
	if !hasBag {
		add("%s does not embed types.Schema, so a rewrite drops the members it does not declare", l.Type)
	}
	versions := slices.Sorted(maps.Keys(goldens))
	if len(versions) == 0 || versions[len(versions)-1] != l.Version {
		body, _ := json.MarshalIndent(schemaGolden{Fields: current, Reserved: l.Reserved}, "", "  ")
		add("no frozen field set for version %d; commit testdata/schema/%s.v%d.json as\n%s", l.Version, l.Name, l.Version, body)
		return out
	}
	latest := goldens[l.Version]

	for _, v := range versions {
		g := goldens[v]
		for _, name := range slices.Sorted(maps.Keys(g.Fields)) {
			typ, ok := current[name]
			switch {
			case !ok && !slices.Contains(l.Reserved, name):
				add("%q (in v%d) is gone; a removed name goes into Reserved so it is never reused", name, v)
			case ok && strings.TrimPrefix(typ, "*") != strings.TrimPrefix(g.Fields[name], "*"):
				add("%q was %s in v%d and is %s now; a new type is a new name", name, g.Fields[name], v, typ)
			}
		}
		for _, name := range g.Reserved {
			if !slices.Contains(l.Reserved, name) {
				add("%q was reserved in v%d and is not now; Reserved only grows", name, v)
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
			add("%q is not in v%d; record it in Added as %q: %d, and leave the version where it is", name, l.Version, name, l.Version)
		}
	}
	for _, name := range slices.Sorted(maps.Keys(l.Added)) {
		_, declared := current[name]
		_, frozen := latest.Fields[name]
		if !declared || frozen {
			add("Added names %q, which is not a field added since v%d", name, l.Version)
		}
	}
	for i := 1; i < len(versions); i++ {
		prev, next := versions[i-1], versions[i]
		if l.Requires[next] == "" && additive(goldens[prev], goldens[next]) {
			add("v%d only adds to v%d; leave the version at %d and put the new names in Added,"+
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
		Version int    `json:"schema_version"`
		A       string `json:"a"`
		B       []int  `json:"b,omitempty"`
	}
	fields := func(extra ...string) map[string]string {
		m := map[string]string{"schema_version": "int", "requires": "[]string", "a": "string", "b": "[]int"}
		for i := 0; i+1 < len(extra); i += 2 {
			m[extra[i]] = extra[i+1]
		}
		return m
	}
	v1 := map[int]schemaGolden{1: {Fields: fields()}}

	for name, tc := range map[string]struct {
		ledger  SchemaLedger
		goldens map[int]schemaGolden
		want    []string
	}{
		"unchanged": {
			ledger:  SchemaLedger{Name: "r", Type: reflect.TypeFor[base](), Version: 1},
			goldens: v1,
		},
		"an added field recorded in Added": {
			ledger:  SchemaLedger{Name: "r", Type: reflect.TypeFor[added](), Version: 1, Added: map[string]int{"c": 1}},
			goldens: v1,
		},
		"an added field left out of Added": {
			ledger:  SchemaLedger{Name: "r", Type: reflect.TypeFor[added](), Version: 1},
			goldens: v1,
			want:    []string{`r: "c" is not in v1; record it in Added as "c": 1, and leave the version where it is`},
		},
		"a removed field that is reserved": {
			ledger:  SchemaLedger{Name: "r", Type: reflect.TypeFor[removed](), Version: 1, Reserved: []string{"b"}},
			goldens: v1,
		},
		"a removed field left unreserved": {
			ledger:  SchemaLedger{Name: "r", Type: reflect.TypeFor[removed](), Version: 1},
			goldens: v1,
			want:    []string{`r: "b" (in v1) is gone; a removed name goes into Reserved so it is never reused`},
		},
		"a changed type": {
			ledger:  SchemaLedger{Name: "r", Type: reflect.TypeFor[retyped](), Version: 1},
			goldens: v1,
			want:    []string{`r: "a" was string in v1 and is int now; a new type is a new name`},
		},
		"a value made a pointer": {
			ledger:  SchemaLedger{Name: "r", Type: reflect.TypeFor[pointered](), Version: 1},
			goldens: v1,
		},
		"a reserved name declared again": {
			ledger:  SchemaLedger{Name: "r", Type: reflect.TypeFor[base](), Version: 1, Reserved: []string{"a"}},
			goldens: v1,
			want:    []string{`r: "a" is reserved and declared again; pick a new name`},
		},
		"a reservation dropped": {
			ledger:  SchemaLedger{Name: "r", Type: reflect.TypeFor[base](), Version: 1},
			goldens: map[int]schemaGolden{1: {Fields: fields(), Reserved: []string{"old"}}},
			want:    []string{`r: "old" was reserved in v1 and is not now; Reserved only grows`},
		},
		"a bump that only adds": {
			ledger:  SchemaLedger{Name: "r", Type: reflect.TypeFor[added](), Version: 2},
			goldens: map[int]schemaGolden{1: {Fields: fields()}, 2: {Fields: fields("c", "bool")}},
			want: []string{`r: v2 only adds to v1; leave the version at 1 and put the new names in Added,` +
				` or name the feature an older reader lacks in Requires`},
		},
		"a bump that names the feature it breaks": {
			ledger:  SchemaLedger{Name: "r", Type: reflect.TypeFor[added](), Version: 2, Requires: map[int]string{2: "c-means-d"}},
			goldens: map[int]schemaGolden{1: {Fields: fields()}, 2: {Fields: fields("c", "bool")}},
		},
		"a bump that removes a reserved name": {
			ledger:  SchemaLedger{Name: "r", Type: reflect.TypeFor[removed](), Version: 2, Reserved: []string{"b"}},
			goldens: map[int]schemaGolden{1: {Fields: fields()}, 2: {Fields: map[string]string{"schema_version": "int", "requires": "[]string", "a": "string"}, Reserved: []string{"b"}}},
		},
		"a version with no frozen field set": {
			ledger:  SchemaLedger{Name: "r", Type: reflect.TypeFor[base](), Version: 2},
			goldens: v1,
			want: []string{"r: no frozen field set for version 2; commit testdata/schema/r.v2.json as\n" +
				"{\n  \"fields\": {\n    \"a\": \"string\",\n    \"b\": \"[]int\",\n    \"requires\": \"[]string\",\n    \"schema_version\": \"int\"\n  },\n  \"reserved\": null\n}"},
		},
		"a type without the bag": {
			ledger:  SchemaLedger{Name: "r", Type: reflect.TypeFor[bagless](), Version: 1},
			goldens: map[int]schemaGolden{1: {Fields: map[string]string{"schema_version": "int", "a": "string", "b": "[]int"}}},
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

	in := `{"schema_version":99,"requires":["x"],"id":"a","future_field":{"x":1}}`
	var row Job
	require.NoError(t, json.Unmarshal([]byte(in), &row))
	// The bag's raw values are the decoder's own; its keys are checked below.
	assert.Equal(t, Job{Schema: Schema{Version: 99, Requires: []string{"x"}, Unknown: row.Unknown}, ID: "a"}, row)
	assert.Equal(t, []string{"future_field"}, slices.Collect(maps.Keys(row.Unknown)))

	out, err := json.Marshal(row.Clone())
	require.NoError(t, err)
	var back map[string]any
	require.NoError(t, json.Unmarshal(out, &back))
	assert.Equal(t, map[string]any{"x": float64(1)}, back["future_field"])
	assert.Equal(t, float64(99), back["schema_version"])
}

func TestSchemaUnmet(t *testing.T) {
	t.Parallel()

	assert.Nil(t, Schema{}.Unmet(nil))
	assert.Nil(t, Schema{Requires: []string{"a"}}.Unmet([]string{"a", "b"}))
	assert.Equal(t, []string{"c", "d"}, Schema{Requires: []string{"c", "a", "d"}}.Unmet([]string{"a"}))
	assert.Equal(t, []string{"claim-declarations", "dead-job-end"}, JobSchema.Features())
}
