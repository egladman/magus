package job

import (
	_ "embed"
	"errors"
	"fmt"
	"io"
	"reflect"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/types"
)

// DeclarationSchema is the JSON Schema for [types.Declaration], embedded so a person or a
// harness can see the shape `magus job fork --stdin` accepts without reading Go. Generated
// from the struct itself, so a field cannot reach the wire undescribed.
//
//go:embed gen/job.schema.json
var DeclarationSchema string

// maxInputBytes bounds one decoded record. A row and a report are both a few hundred
// bytes of paths and sentences; the cap is what keeps a caller that piped the wrong file
// into stdin from being read into memory before it is rejected.
const maxInputBytes = 1 << 20

// DecodeResult reads a holder's result, and DecodeDeclaration one declared job, from r.
//
// STRICT AND VERSIONED, in that order of reporting: a sender a release ahead is told which
// versions this magus knows instead of learning that one of its fields is unknown, and
// then an unknown member is an error, because a field the verifier never reads looks to its
// author exactly like one that was taken into account. Any version up to this magus's own
// is accepted: an older sender's record is a subset of the current shape.
//
// Every failure here is one class to a caller: the input could not be understood, as
// opposed to understood and rejected. `magus job wait` exits 2 for this and 1 for a
// rejection.
func DecodeResult(r io.Reader) (types.JobResult, error) {
	raw, err := readInput(r, "result")
	if err != nil {
		return types.JobResult{}, err
	}
	if err := checkVersion(raw, "result", types.JobResultSchema); err != nil {
		return types.JobResult{}, err
	}
	var rep types.JobResult
	if err := json.UnmarshalStrict(raw, &rep); err != nil {
		return types.JobResult{}, fmt.Errorf("job: the input is not a version %d result: %w", types.JobResultSchemaVersion, err)
	}
	return rep, nil
}

// DecodeDeclaration reads one declared job. See [DecodeResult] for the rules; this is the
// same two passes over the job's schema.
func DecodeDeclaration(r io.Reader) (types.Declaration, error) {
	raw, err := readInput(r, "job")
	if err != nil {
		return types.Declaration{}, err
	}
	if err := checkVersion(raw, "job", types.DeclarationSchema); err != nil {
		return types.Declaration{}, err
	}
	var row types.Declaration
	if err := json.UnmarshalStrict(raw, &row); err != nil {
		return types.Declaration{}, fmt.Errorf("job: the input is not a version %d job: %w", types.JobSchemaVersion, err)
	}
	if err := row.FoldLegacyNames(); err != nil {
		return types.Declaration{}, err
	}
	return row, row.Validate()
}

// storeStamped are the members of a stored row that the store computes and a declaration
// cannot carry. `magus describe job -o json` prints them beside the declared ones, and the
// edit loop (describe, edit, apply) hands that record back, so [DecodeDeclarations] drops
// them rather than refusing the loop at its second step. [DecodeDeclaration] still refuses
// them, as `magus job fork` should: a caller declaring a creation time is wrong, while one
// re-applying a record it read back is not. Any other unknown member is an error in both.
var storeStamped = jobOnlyMembers()

// jobOnlyMembers are the JSON members [types.Job] serializes that [types.Declaration]
// does not: a field added to Job alone is the store's by construction.
func jobOnlyMembers() []string {
	declared := jsonMembers(reflect.TypeFor[types.Declaration]())
	return slices.DeleteFunc(jsonMembers(reflect.TypeFor[types.Job]()), func(name string) bool {
		return slices.Contains(declared, name)
	})
}

// jsonMembers are the names t serializes its fields under, promoted ones included.
func jsonMembers(t reflect.Type) []string {
	var out []string
	for _, f := range reflect.VisibleFields(t) {
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if f.Anonymous || !f.IsExported() || name == "" || name == "-" {
			continue
		}
		out = append(out, name)
	}
	return out
}

// dropStoreStamped is raw without the members in storeStamped; without `state`, the
// status a described row prints and apply never writes; and without `validation` beside a
// `check`: a stored row renders its check into that line, and a declaration carrying both
// is refused. A record that is not an object comes back as it was, for
// [DecodeDeclaration] to refuse in its own words.
func dropStoreStamped(raw []byte) ([]byte, error) {
	members, isObject := objectMembers(raw)
	if !isObject {
		return raw, nil
	}
	dropped := false
	for _, name := range append(slices.Clone(storeStamped), "state") {
		if _, ok := members[name]; ok {
			delete(members, name)
			dropped = true
		}
	}
	if _, ok := members["check"]; ok {
		if _, ok := members["validation"]; ok {
			delete(members, "validation")
			dropped = true
		}
	}
	if !dropped {
		return raw, nil
	}
	return json.Marshal(members)
}

// objectMembers is raw's top-level members, false for a record that is not a JSON object.
func objectMembers(raw []byte) (map[string]json.RawMessage, bool) {
	var members map[string]json.RawMessage
	if err := json.Unmarshal(raw, &members); err != nil {
		return nil, false
	}
	return members, true
}

// DecodeDeclarations reads the records `magus job apply -f` takes: one JSON object, a JSON
// array of them, or one object per line. Each is held to [DecodeDeclaration]'s rules, and a
// failure names the record by its position, so nothing is applied from a stream with one
// bad record in it.
func DecodeDeclarations(r io.Reader) ([]types.Declaration, error) {
	raw, err := readInput(r, "records")
	if err != nil {
		return nil, err
	}
	var parts []json.RawMessage
	trimmed := strings.TrimSpace(string(raw))
	switch {
	case strings.HasPrefix(trimmed, "["):
		if err := json.Unmarshal([]byte(trimmed), &parts); err != nil {
			return nil, fmt.Errorf("job: the records are not a JSON array of jobs: %w", err)
		}
	case json.Valid([]byte(trimmed)):
		parts = []json.RawMessage{json.RawMessage(trimmed)}
	default:
		for line := range strings.SplitSeq(trimmed, "\n") {
			if line = strings.TrimSpace(line); line != "" {
				parts = append(parts, json.RawMessage(line))
			}
		}
	}
	if len(parts) == 0 {
		return nil, errors.New("job: the records hold no job")
	}
	rows := make([]types.Declaration, 0, len(parts))
	seen := map[string]int{}
	for i, part := range parts {
		declared, err := dropStoreStamped(part)
		if err != nil {
			return nil, fmt.Errorf("record %d: %w", i+1, err)
		}
		row, err := DecodeDeclaration(strings.NewReader(string(declared)))
		if err != nil {
			return nil, fmt.Errorf("record %d: %w", i+1, err)
		}
		if first, dup := seen[row.ID]; dup {
			return nil, fmt.Errorf("record %d: job: %s is record %d too, and one stream applies each job once", i+1, row.ID, first)
		}
		seen[row.ID] = i + 1
		rows = append(rows, row)
	}
	return rows, nil
}

func readInput(r io.Reader, what string) ([]byte, error) {
	raw, err := io.ReadAll(io.LimitReader(r, maxInputBytes+1))
	if err != nil {
		return nil, fmt.Errorf("job: read the %s: %w", what, err)
	}
	if len(raw) > maxInputBytes {
		return nil, fmt.Errorf("job: the %s is larger than %d bytes, which no %s is", what, maxInputBytes, what)
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return nil, fmt.Errorf("job: the %s is empty", what)
	}
	return raw, nil
}

// checkVersion reads the envelope that decides whether the rest can be read at all.
// Non-strict on purpose: this pass must survive the very fields a newer sender added,
// or a version skew would be reported as an unknown member, which is the misdirect the
// version exists to prevent.
func checkVersion(raw []byte, what string, ledger types.SchemaLedger) error {
	var envelope types.Schema
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return fmt.Errorf("job: the %s is not JSON: %w", what, err)
	}
	supported := ledger.Version
	switch v := envelope.Version; {
	case v <= 0:
		return fmt.Errorf("job: the %s carries no schema_version, this magus accepts versions 1 through %d", what, supported)
	case v > supported:
		return fmt.Errorf("job: the %s is schema_version %d and this magus accepts versions 1 through %d:"+
			" a newer record is not readable by an older magus, update magus or send version %d", what, v, supported, supported)
	}
	if lacks := envelope.Unmet(ledger.Features()); lacks != nil {
		return fmt.Errorf("job: the %s requires %s, which this magus (schema %d) lacks, update magus",
			what, quoteAll(lacks), supported)
	}
	return nil
}
