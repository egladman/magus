package types

import (
	"reflect"
	"slices"

	"github.com/egladman/magus/libs/codec"
	"github.com/egladman/magus/libs/safetyscissors/job"
)

// Schema is the envelope a record magus rewrites in place embeds. A reader ignores the
// members it does not know and refuses only what it cannot act on; a writer carries those
// members back out.
//
// Embedded, never named: json inlines it, so api_version stays a
// top-level member, and the Buzz mirrors inline it the same way.
type Schema struct {
	// Version is the format this record is written in. A reader refuses
	// any other, so no magus acts on a record it cannot read.
	Version APIVersion `json:"api_version" yaml:"api_version" buzz:"apiVersion"`
	// Requires names the features a reader must implement to act on this record at all.
	// A reader lacking one treats the record as read-only; the records around it stay
	// usable. Set only for content an older reader would act on wrongly, never for an
	// added field it can ignore.
	Requires []string `json:"requires,omitempty" yaml:"requires,omitempty"`
	// Unknown holds every member this build does not declare, re-emitted unchanged when
	// the record is written back.
	Unknown map[string]codec.RawMessage `json:",unknown" yaml:"-" buzz:"-"`
}

// Unmet returns the entries of Requires that known does not name, in record order, or
// nil when this reader can act on the record.
func (s Schema) Unmet(known []string) []string {
	var out []string
	for _, r := range s.Requires {
		if !slices.Contains(known, r) {
			out = append(out, r)
		}
	}
	return out
}

// FormatLedger is the evolution record of one versioned type, read by the conventions
// test in schema_test.go against the frozen field sets in testdata/schema.
//
// The contract it enforces: adding a field never moves Version; a removed or renamed
// json name goes into Reserved and is never declared again; a field keeps its type for
// life. Version moves only for a change an older reader would act on WRONGLY, and that
// version names the feature in Requires.
type FormatLedger struct {
	// Name keys the golden files: testdata/schema/<Name>.<version>.json,
	// as job.v1alpha1.json.
	Name string
	Type reflect.Type
	// Version is the constant this build stamps and accepts.
	Version APIVersion
	// Added maps each field declared after the newest golden was frozen to the version
	// in force when it appeared.
	Added map[string]APIVersion
	// Reserved are json names the type once declared. They may only grow.
	Reserved []string
	// Requires maps each feature that broke older readers to the version
	// that brought it.
	Requires map[string]APIVersion
}

// Features are the Requires entries a reader at l.Version implements, sorted.
func (l FormatLedger) Features() []string {
	var out []string
	for f, v := range l.Requires {
		if v.Compare(l.Version) <= 0 {
			out = append(out, f)
		}
	}
	slices.Sort(out)
	return out
}

// DeclarationSchema is the ledger of the job a caller declares on stdin.
// Its features are the ones a stored row requires; see [JobFeatures].
var DeclarationSchema = FormatLedger{
	Name:    "declaration",
	Type:    reflect.TypeFor[Declaration](),
	Version: APIVersion(job.APIVersion),
	// completion_gates and goals are postconditions' earlier spellings, and waivers is
	// deviations' first name. A record sending one is refused as an unknown member.
	Added: map[string]APIVersion{"postconditions": APIVersion(job.APIVersion)},
	Reserved: []string{"completion_gates", "goals", "waivers", "schema_version", "criteria", "model", "fresh_reason", "tier",
		"write_paths", "deny_paths", "read_paths", "soft_paths", "focus", "forbidden_paths", "owned_paths", "validation"},
	Requires: map[string]APIVersion{
		"dead-job-end":             APIVersion(job.APIVersion),
		"claim-declarations":       APIVersion(job.APIVersion),
		"signature-postconditions": APIVersion(job.APIVersion),
		"job-generations":          APIVersion(job.APIVersion),
	},
}

// JobResultSchema is the ledger of the result a holder files.
var JobResultSchema = FormatLedger{
	Name:     "job-result",
	Type:     reflect.TypeFor[job.Result](),
	Version:  job.ResultAPIVersion,
	Reserved: []string{"gate_evidence", "schema_version"},
}

// StoredOutputSchema is the ledger of the record `magus query output <ref> -o jsonl`
// writes and `magus query output --stdin` reads.
var StoredOutputSchema = FormatLedger{
	Name:     "output-record",
	Type:     reflect.TypeFor[StoredOutput](),
	Version:  StoredOutputAPIVersion,
	Reserved: []string{"schema_version"},
}

// FormatLedgers are every ledger the conventions test holds to the
// contract. A stored job row is internal/job's record, not a ledger.
var FormatLedgers = []FormatLedger{DeclarationSchema, JobResultSchema, StoredOutputSchema}
