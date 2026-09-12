package job

import (
	_ "embed"
	"errors"
	"fmt"
	"io"
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

// foldStoredLanes reads the four lanes out of a ledger file a previous magus wrote and
// puts them on the rows raw was decoded into, which carry only the current spelling.
//
// compat: see the legacy fields on [Declaration]. Without it a plan written before the rename
// comes back with every boundary empty, and the next put stores that as the truth.
func foldStoredLanes(raw []byte, rows []types.Job) error {
	var stored struct {
		Jobs []struct {
			WritePaths []string `json:"owned_paths"`
			DenyPaths  []string `json:"forbidden_paths"`
			ReadPaths  []string `json:"focus"`
			Model      string   `json:"tier"`
		} `json:"jobs"`
	}
	if err := json.Unmarshal(raw, &stored); err != nil {
		return err
	}
	if len(stored.Jobs) != len(rows) {
		return nil
	}
	for i := range rows {
		old := stored.Jobs[i]
		fold := func(name string, into *[]string, from []string) error {
			if len(from) == 0 {
				return nil
			}
			if len(*into) > 0 {
				return fmt.Errorf("row %s carries %s and its renamed spelling, and nothing here can say which one it meant", rows[i].ID, name)
			}
			*into = from
			return nil
		}
		if err := errors.Join(
			fold("owned_paths", &rows[i].WritePaths, old.WritePaths),
			fold("forbidden_paths", &rows[i].DenyPaths, old.DenyPaths),
			fold("focus", &rows[i].ReadPaths, old.ReadPaths),
		); err != nil {
			return err
		}
		if old.Model != "" {
			if rows[i].Model != "" {
				return fmt.Errorf("row %s carries tier and its renamed spelling, and nothing here can say which one it meant", rows[i].ID)
			}
			rows[i].Model = old.Model
		}
	}
	return nil
}

// maxInputBytes bounds one decoded record. A row and a report are both a few hundred
// bytes of paths and sentences; the cap is what keeps a caller that piped the wrong file
// into stdin from being read into memory before it is rejected.
const maxInputBytes = 1 << 20

// DecodeResult reads a holder's result, and DecodeDeclaration one declared job, from r.
//
// STRICT AND VERSIONED, in that order of reporting: a sender a release ahead is told which
// versions this magus knows instead of learning that one of its fields is unknown, and
// then an unknown member is an error, because a field the verifier never reads looks to its
// author exactly like one that was taken into account.
//
// Every failure here is one class to a caller: the input could not be understood, as
// opposed to understood and rejected. `magus job wait` exits 2 for this and 1 for a
// rejection.
func DecodeResult(r io.Reader) (types.JobResult, error) {
	raw, err := readInput(r, "result")
	if err != nil {
		return types.JobResult{}, err
	}
	if err := checkVersion(raw, "result", ResultSchemaVersion); err != nil {
		return types.JobResult{}, err
	}
	var rep types.JobResult
	if err := json.UnmarshalStrict(raw, &rep); err != nil {
		return types.JobResult{}, fmt.Errorf("job: the input is not a version %d result: %w", ResultSchemaVersion, err)
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
	if err := checkVersion(raw, "job", types.JobSchemaVersion); err != nil {
		return types.Declaration{}, err
	}
	var row types.Declaration
	if err := json.UnmarshalStrict(raw, &row); err != nil {
		return types.Declaration{}, fmt.Errorf("job: the input is not a version %d job: %w", types.JobSchemaVersion, err)
	}
	if err := row.FoldLegacyLanes(); err != nil {
		return types.Declaration{}, err
	}
	return row, row.Validate()
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

// checkVersion reads the one member that decides whether the rest can be read at all.
// Non-strict on purpose: this pass must survive the very fields a newer sender added,
// or a version skew would be reported as an unknown member, which is the misdirect the
// version exists to prevent.
func checkVersion(raw []byte, what string, supported int) error {
	var envelope struct {
		SchemaVersion int `json:"schema_version"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return fmt.Errorf("job: the %s is not JSON: %w", what, err)
	}
	switch v := envelope.SchemaVersion; {
	case v == 0:
		return fmt.Errorf("job: the %s carries no schema_version; this magus accepts version %d", what, supported)
	case v != supported:
		return fmt.Errorf("job: the %s is schema_version %d and this magus accepts version %d only."+
			" A newer record is not readable by an older magus; update magus, or send version %d", what, v, supported, supported)
	}
	return nil
}
