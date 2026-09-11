package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	// SchemaVersion is the shape this record is written in.
	SchemaVersion int @json:"schema_version"@
	// ID is the lease's identity within the plan.
	ID string @json:"id" schema:"leaseid"@
	// State is where the lease stands.
	State types.LeaseState @json:"state,omitempty"@
	// Check is the one check this lease runs.
	Check *Check @json:"check,omitempty"@
	// Paths are the paths it may write.
	Paths []string @json:"paths,omitempty"@
	Undocumented bool @json:"undocumented,omitempty"@
	Hidden string @json:"-"@
}

// Check is the check a lease runs.
type Check struct {
	Target  string   @json:"target"@
	Project string   @json:"project,omitempty"@
	Args    []string @json:"args,omitempty"@
}
`

const wantFixtureSchema = `{
  "$schema": "http://json-schema.org/draft-07/schema#",
  "$id": "https://magus.invalid/ledger/fixture.schema.json",
  "$comment": "Generated from ledger.Fixture by ` + "`magus-utils ledgerschema`" + `. DO NOT EDIT; run ` + "`magus run ledger-generate .`" + `.",
  "title": "magus fixture",
  "description": "Fixture is a record a caller declares.",
  "type": "object",
  "additionalProperties": false,
  "required": ["schema_version", "id"],
  "properties": {
    "schema_version": {
      "type": "integer",
      "const": 3,
      "description": "SchemaVersion is the shape this record is written in."
    },
    "id": {
      "type": "string",
      "pattern": "^[./0-9:A-Z_a-z-]+$",
      "maxLength": 128,
      "description": "ID is the lease's identity within the plan."
    },
    "state": {
      "type": "string",
      "enum": ["", "declared", "running", "pass", "fail", "no_return"],
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

var fixtureRecord = ledgerRecord{Struct: "Fixture", Title: "magus fixture", File: "fixture.schema.json", Version: "FixtureSchemaVersion"}

// One assertion over the whole rendered document rather than a probe per rule: the schema
// is published byte for byte, so its layout and its ordering are part of what a reader
// compiles against.
func TestRenderLedgerSchemaDerivesEveryRuleFromTheStruct(t *testing.T) {
	t.Parallel()

	decls, err := loadGoDecls(writeRecordFixture(t, recordFixture))
	require.NoError(t, err)

	body, err := renderLedgerSchema(fixtureRecord, decls)
	require.NoError(t, err)
	assert.Equal(t, wantFixtureSchema, string(body))
}

// The lease id pattern is derived from the validator rather than written down, so the
// published charset cannot say something the store would refuse.
func TestLeaseIDPatternMatchesTheValidator(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "^[./0-9:A-Z_a-z-]+$", leaseIDPattern())
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

			_, err = renderLedgerSchema(fixtureRecord, decls)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

// The generator reads the ledger's own sources, so a rename there is a rename this
// command has to be told about rather than one it silently renders around.
func TestLedgerRecordsRenderFromTheirDeclaredSources(t *testing.T) {
	t.Parallel()

	roots := make([]string, 0, len(ledgerSources))
	for _, src := range ledgerSources {
		roots = append(roots, filepath.Join("..", "..", src))
	}
	decls, err := loadGoDecls(roots...)
	require.NoError(t, err)

	for _, rec := range ledgerRecords {
		body, err := renderLedgerSchema(rec, decls)
		require.NoError(t, err, rec.File)
		assert.Contains(t, string(body), `"$id": "https://magus.invalid/ledger/`+rec.File+`"`)
	}
}
