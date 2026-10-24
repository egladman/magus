package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/types"
)

// recordFixture is a record shaped like a ledger row: one field per rule the generator
// applies, so what the schema says about each is asserted against a source small enough
// to read beside it. The @ stands in for a backtick.
const recordFixture = `
package fixture

// FixtureSchemaVersion is the shape this fixture is written in.
const FixtureSchemaVersion = 3

// Fixture is a record a caller declares.
//
// The second paragraph is rationale, and the schema does not carry it.
type Fixture struct {
	Envelope
	// ID is the lease's identity within the plan.
	ID string @json:"id" schema:"leaseid"@
	// State is where the lease stands.
	State types.JobState @json:"state,omitempty"@
	// Check is the one check this lease runs.
	Check *Check @json:"check,omitempty"@
	// Paths are the paths it may write.
	Paths []string @json:"paths,omitempty"@
	Undocumented bool @json:"undocumented,omitempty"@
	Hidden string @json:"-"@
}

// Envelope is inlined into every record that embeds it, and its bag publishes nothing.
type Envelope struct {
	// SchemaVersion is the shape this record is written in.
	SchemaVersion int @json:"schema_version"@
	Unknown map[string]string @json:",unknown"@
}

// Check is the check a lease runs.
type Check struct {
	Target  string   @json:"target"@
	Project string   @json:"project,omitempty"@
	Args    []string @json:"args,omitempty"@
	// Bare is optional by omitzero alone, the spelling jsonv2 drops a false by.
	Bare bool @json:"bare,omitzero"@
}
`

const wantFixtureSchema = `{
  "$schema": "http://json-schema.org/draft-07/schema#",
  "$id": "https://magus.invalid/job/fixture.schema.json",
  "$comment": "Generated from Fixture by ` + "`magus-utils jobschema`" + `. DO NOT EDIT; run ` + "`magus run job-generate .`" + `.",
  "title": "magus fixture",
  "description": "Fixture is a record a caller declares.",
  "type": "object",
  "additionalProperties": false,
  "required": ["schema_version", "id"],
  "properties": {
    "schema_version": {
      "type": "integer",
      "minimum": 1,
      "maximum": 3,
      "description": "SchemaVersion is the shape this record is written in."
    },
    "id": {
      "type": "string",
      "pattern": "^[./0-9a-z-]+$",
      "maxLength": 128,
      "description": "ID is the lease's identity within the plan."
    },
    "state": {
      "type": "string",
      "enum": ["", "declared", "running", "exited", "pass", "fail", "no_return"],
      "description": "State is where the lease stands."
    },
    "check": {
      "type": "object",
      "additionalProperties": false,
      "required": ["target"],
      "properties": {
        "target": {
          "type": "string"
        },
        "project": {
          "type": "string"
        },
        "args": {
          "type": "array",
          "items": {
            "type": "string"
          }
        },
        "bare": {
          "type": "boolean",
          "description": "Bare is optional by omitzero alone, the spelling jsonv2 drops a false by."
        }
      },
      "description": "Check is the one check this lease runs."
    },
    "paths": {
      "type": "array",
      "items": {
        "type": "string"
      },
      "description": "Paths are the paths it may write."
    },
    "undocumented": {
      "type": "boolean"
    }
  }
}
`

func writeRecordFixture(t *testing.T, src string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fixture.go")
	require.NoError(t, os.WriteFile(path, []byte(strings.ReplaceAll(src, "@", "`")), 0o644))
	return path
}

var fixtureRecord = jobRecord{Struct: "Fixture", Title: "magus fixture", File: "fixture.schema.json", Version: "FixtureSchemaVersion"}

// One assertion over the whole rendered document rather than a probe per rule: the schema
// is published byte for byte, so its layout and its ordering are part of what a reader
// compiles against.
func TestRenderLedgerSchemaDerivesEveryRuleFromTheStruct(t *testing.T) {
	t.Parallel()

	decls, err := loadGoDecls(writeRecordFixture(t, recordFixture))
	require.NoError(t, err)

	body, err := renderJobSchema(fixtureRecord, decls)
	require.NoError(t, err)
	assert.Equal(t, wantFixtureSchema, string(body))
}

// The lease id pattern is derived from the validator rather than written down, so the
// published charset cannot say something the store would refuse.
func TestLeaseIDPatternMatchesTheValidator(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "^[./0-9a-z-]+$", leaseIDPattern())
}

func TestWaiverCodeEnumIsExactlyTheWaivableCodes(t *testing.T) {
	t.Parallel()

	enum := closedSets["DiagnosticCode"]
	require.NotEmpty(t, enum)
	for _, c := range types.AllDiagnosticCodes() {
		assert.Equal(t, types.Waivable(c), slices.Contains(enum, string(c)), "code %s", c)
	}
	for _, name := range enum {
		assert.True(t, types.Waivable(types.DiagnosticCode(name)), "enum names %s", name)
	}
}

func TestRenderLedgerSchemaNamesWhatItCannotPublish(t *testing.T) {
	t.Parallel()

	cases := map[string]struct{ src, want string }{
		"a field with no json tag": {
			src:  "package fixture\nconst FixtureSchemaVersion = 1\ntype Fixture struct {\n\tID string\n}\n",
			want: "Fixture.ID carries no json tag",
		},
		"a type these sources do not declare": {
			src:  "package fixture\nconst FixtureSchemaVersion = 1\ntype Fixture struct {\n\tAt time.Time @json:\"at\"@\n}\n",
			want: "type Time is neither a struct nor a closed set",
		},
		"no version constant": {
			src:  "package fixture\ntype Fixture struct {\n\tID string @json:\"id\"@\n}\n",
			want: "no constant FixtureSchemaVersion",
		},
		"no such struct": {
			src:  "package fixture\nconst FixtureSchemaVersion = 1\n",
			want: "no struct Fixture",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			decls, err := loadGoDecls(writeRecordFixture(t, tc.src))
			require.NoError(t, err)

			_, err = renderJobSchema(fixtureRecord, decls)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

// The generator reads the job store's own sources, so a rename there is a rename this
// command has to be told about rather than one it silently renders around.
func TestJobRecordsRenderFromTheirDeclaredSources(t *testing.T) {
	t.Parallel()

	roots := make([]string, 0, len(jobSources))
	for _, src := range jobSources {
		roots = append(roots, filepath.Join("..", "..", src))
	}
	decls, err := loadGoDecls(roots...)
	require.NoError(t, err)

	for _, rec := range jobRecords {
		body, err := renderJobSchema(rec, decls)
		require.NoError(t, err, rec.File)
		assert.Contains(t, string(body), fmt.Sprintf("\"$id\": \"https://magus.invalid/job/%s\"", rec.File))
	}
}

func TestWritePathPatternMatchesTheValidator(t *testing.T) {
	t.Parallel()

	pattern := regexp.MustCompile(writePathPattern())
	cases := []struct {
		path   string
		accept bool
	}{
		{"cmd/magus/job.go", true},
		{"internal/job/*.go", true},
		{"internal/**/*.go", true},
		{"**/gen/*.go", true},
		{"./a/b.go", true},
		{"a/./b.go", true},
		{".github/workflows/ci.yml", true},
		{"...", true},
		{"..x/y", true},
		{"cmd/magus/job.go#jobFork", true},
		{`notes/a\#b.md#Intro`, true},
		{"", false},
		{".", false},
		{"~/x", false},
		{"~x", false},
		{"/abs/x", false},
		{"..", false},
		{"../x", false},
		{"a/../b", false},
		{"a/..", false},
		{"a/", false},
		{"internal/**", false},
		{"**", false},
		{"a#", false},
		{"#decl", false},
		{"a#b#c", false},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.accept, pattern.MatchString(tc.path), "%q", tc.path)

		// The schema is the narrower of the two: what it accepts, the record check accepts.
		if !tc.accept {
			continue
		}
		for _, p := range (types.Declaration{ID: "j", WritePaths: []string{tc.path}}).Problems() {
			assert.NotContains(t, p.Field, "write_paths", "%q: %s", tc.path, p.Message)
		}
	}
	for _, refused := range []string{"~/x", "/abs/x", "../x"} {
		problems := types.Declaration{ID: "j", WritePaths: []string{refused}}.Problems()
		assert.NotEmpty(t, problems, "the validator accepts %q, and the pattern refuses it", refused)
	}
}

func TestDurationPatternMatchesParseJobTimeout(t *testing.T) {
	t.Parallel()

	pattern := regexp.MustCompile(durationPattern())
	for _, s := range []string{
		"", "30m", "2h", "1h30m", "1.5h", ".5s", "5.s", "100ms", "250us", "250µs", "7ns", "0m5s", "10s0m",
		"0s", "0", "00s", "0.0h", "5", "-5m", "1d", "1x", "m", "s5", "1h 30m", "1..5s",
	} {
		_, err := types.ParseJobTimeout(s)
		assert.Equal(t, err == nil, pattern.MatchString(s), "%q", s)
	}
}

func TestTargetPatternMatchesTheValidator(t *testing.T) {
	t.Parallel()

	pattern := regexp.MustCompile(targetPattern())
	for _, name := range []string{"test", "go-test", "go_test", "T1"} {
		require.NoError(t, types.ValidateTargetName(name), "%q", name)
		assert.True(t, pattern.MatchString(name), "%q", name)
	}
	assert.True(t, pattern.MatchString("go::go-test"), "a spell filter prefixes the target")
	for _, name := range []string{"go test", "go:test", "::test", "a::b::c", "te$t", "go::"} {
		assert.False(t, pattern.MatchString(name), "%q", name)
	}
}

// markerFixture marks one field of each shape a marker can sit on, and names a LeaseCheck
// so the either-or rule applies to it.
const markerFixture = `
package fixture

const FixtureSchemaVersion = 1

type Fixture struct {
	Parent    string      @json:"parent,omitempty" schema:"leaseid"@
	DependsOn []string    @json:"depends_on,omitempty" schema:"leaseid"@
	Paths     []string    @json:"paths,omitempty" schema:"writepath"@
	Timeout   string      @json:"timeout,omitempty" schema:"duration"@
	Dir       string      @json:"dir,omitempty" schema:"abspath"@
	Reason    string      @json:"reason,omitempty" schema:"nonblank"@
	Check     *LeaseCheck @json:"check,omitempty"@
}

type LeaseCheck struct {
	Target string @json:"target" schema:"target"@
	Script string @json:"script,omitempty"@
}
`

func renderMarkerFixture(t *testing.T, src string) (map[string]any, error) {
	t.Helper()

	decls, err := loadGoDecls(writeRecordFixture(t, src))
	require.NoError(t, err)
	body, err := renderJobSchema(fixtureRecord, decls)
	if err != nil {
		return nil, err
	}
	var doc map[string]any
	require.NoError(t, json.Unmarshal(body, &doc))
	return doc, nil
}

func TestMarkersConstrainTheStringOrItsItems(t *testing.T) {
	t.Parallel()

	doc, err := renderMarkerFixture(t, markerFixture)
	require.NoError(t, err)
	props := doc["properties"].(map[string]any)
	prop := func(name string) map[string]any { return props[name].(map[string]any) }

	assert.Equal(t, leaseIDPattern(), prop("parent")["pattern"])
	assert.EqualValues(t, types.MaxJobIDLen, prop("parent")["maxLength"])

	deps := prop("depends_on")
	assert.NotContains(t, deps, "pattern", "the array has no charset, its items do")
	items := deps["items"].(map[string]any)
	assert.Equal(t, leaseIDPattern(), items["pattern"])
	assert.EqualValues(t, types.MaxJobIDLen, items["maxLength"])

	assert.Equal(t, writePathPattern(), prop("paths")["items"].(map[string]any)["pattern"])
	assert.Equal(t, durationPattern(), prop("timeout")["pattern"])
	assert.Equal(t, absPathPattern, prop("dir")["pattern"])
	assert.EqualValues(t, 1, prop("reason")["minLength"])
}

func TestLeaseCheckIsATargetOrAScript(t *testing.T) {
	t.Parallel()

	doc, err := renderMarkerFixture(t, markerFixture)
	require.NoError(t, err)
	check := doc["properties"].(map[string]any)["check"].(map[string]any)

	assert.NotContains(t, check, "required", "the oneOf chooses the target, the struct does not require it")
	assert.Equal(t, targetPattern(), check["properties"].(map[string]any)["target"].(map[string]any)["pattern"])

	choices := check["oneOf"].([]any)
	require.Len(t, choices, 2)
	assert.Equal(t, []any{"target"}, choices[0].(map[string]any)["required"])
	assert.Equal(t, []any{"script"}, choices[1].(map[string]any)["required"])
}

func TestAMarkerThatMarksNothingIsRefused(t *testing.T) {
	t.Parallel()

	cases := map[string]struct{ field, want string }{
		"an unknown marker":      {`Name string @json:"name" schema:"leaseld"@`, `schema marker "leaseld"`},
		"a marker on an integer": {`Count int @json:"count" schema:"leaseid"@`, "marks a string"},
		"a marker on a record":   {`Inner Inner @json:"inner" schema:"leaseid"@`, "is a record"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			src := fmt.Sprintf(`package fixture
const FixtureSchemaVersion = 1
type Inner struct{}
type Fixture struct {
	%s
}
`, tc.field)
			_, err := renderMarkerFixture(t, src)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}
