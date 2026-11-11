package types

import (
	"maps"
	"reflect"
	"slices"

	"github.com/egladman/magus/internal/json"
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
	Unknown map[string]json.RawMessage `json:",unknown" yaml:"-" buzz:"-"`
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

func (s Schema) clone() Schema {
	s.Requires = slices.Clone(s.Requires)
	s.Unknown = maps.Clone(s.Unknown)
	return s
}

// SchemaLedger is the evolution record of one versioned type, read by the conventions
// test in schema_test.go against the frozen field sets in testdata/schema.
//
// The contract it enforces: adding a field never moves Version; a removed or renamed
// json name goes into Reserved and is never declared again; a field keeps its type for
// life. Version moves only for a change an older reader would act on WRONGLY, and that
// version names the feature in Requires.
type SchemaLedger struct {
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
func (l SchemaLedger) Features() []string {
	var out []string
	for f, v := range l.Requires {
		if v.Compare(l.Version) <= 0 {
			out = append(out, f)
		}
	}
	slices.Sort(out)
	return out
}

// JobSchema is the ledger of a stored job row.
var JobSchema = SchemaLedger{
	Name:    "job",
	Type:    reflect.TypeFor[Job](),
	Version: JobAPIVersion,
	// The four boundary renames and completion_gates, names only the JSON document an
	// older magus kept the plan in carried, and waivers, the name deviations had first.
	Reserved: []string{"owned_paths", "forbidden_paths", "focus", "tier", "completion_gates", "waivers", "schema_version"},
	Requires: map[string]APIVersion{
		"dead-job-end":       JobAPIVersion,
		"claim-declarations": JobAPIVersion,
		"signature-goals":    JobAPIVersion,
		"job-generations":    JobAPIVersion,
	},
}

// DeclarationSchema is the ledger of the job a caller declares on stdin. It shares the
// row's version and features.
var DeclarationSchema = SchemaLedger{
	Name:    "declaration",
	Type:    reflect.TypeFor[Declaration](),
	Version: JobAPIVersion,
	// goals' spelling frozen in v11, and deviations' first name. A record sending either is
	// refused as an unknown member.
	Reserved: []string{"completion_gates", "waivers", "schema_version"},
	Requires: JobSchema.Requires,
}

// JobResultSchema is the ledger of the result a holder files.
var JobResultSchema = SchemaLedger{
	Name:     "job-result",
	Type:     reflect.TypeFor[JobResult](),
	Version:  JobResultAPIVersion,
	Reserved: []string{"gate_evidence", "schema_version"},
}

// StoredOutputSchema is the ledger of the record `magus query output <ref> -o jsonl`
// writes and `magus query output --stdin` reads.
var StoredOutputSchema = SchemaLedger{
	Name:     "output-record",
	Type:     reflect.TypeFor[StoredOutput](),
	Version:  StoredOutputAPIVersion,
	Reserved: []string{"schema_version"},
}

// SchemaLedgers are every ledger the conventions test holds to the contract.
var SchemaLedgers = []SchemaLedger{JobSchema, DeclarationSchema, JobResultSchema, StoredOutputSchema}
