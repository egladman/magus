package ledger

import (
	_ "embed"
	"fmt"
	"io"
	"strings"

	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/types"
)

// RowSchema is the JSON Schema for [Row], embedded so a person or a harness can see the
// shape `magus ledger register --stdin` accepts without reading Go.
//
// A file beside the struct rather than reflection over it, for the reason report.schema.json
// gives: the schema is the CONTRACT, and one generated from field tags changes shape
// whenever the struct's internals do. TestRowSchemaMatchesTheStruct keeps the two honest.
//
//go:embed row.schema.json
var RowSchema string

// Row is the typed INPUT for one lease row: the fields a caller DECLARES, and nothing the
// store computes.
//
// Separate from types.Lease because the two answer different questions. types.Lease is the
// record served to a reader, timestamps, releases and registration verdict included; this
// is what a client is allowed to say. A caller cannot set when its row was created or what
// it released, and the way to make that true is for the input type not to carry the fields
// rather than for the store to strip them afterwards.
//
// It is a DECLARATION and not a merge: every field it carries is written, so an omitted one
// is cleared rather than kept. That is what `register` means and what the magus_ledger
// tool's put deliberately does not do, since an agent advancing one field of a live row
// must not erase the rest (see [Merge]).
type Row struct {
	// SchemaVersion is required. See types.LeaseSchemaVersion.
	SchemaVersion int `json:"schema_version" yaml:"schema_version"`
	// ID is the lease's identity within the plan, and the only required field besides
	// the version.
	ID string `json:"id" yaml:"id"`
	// The rest mirror types.Lease one for one; that type documents what each one means.
	Parent         string           `json:"parent,omitempty"          yaml:"parent,omitempty"`
	Goal           string           `json:"goal,omitempty"            yaml:"goal,omitempty"`
	Checkpoint     string           `json:"checkpoint,omitempty"      yaml:"checkpoint,omitempty"`
	OwnedPaths     []string         `json:"owned_paths,omitempty"     yaml:"owned_paths,omitempty"`
	ForbiddenPaths []string         `json:"forbidden_paths,omitempty" yaml:"forbidden_paths,omitempty"`
	Focus          []string         `json:"focus,omitempty"           yaml:"focus,omitempty"`
	DependsOn      []string         `json:"depends_on,omitempty"      yaml:"depends_on,omitempty"`
	Tier           string           `json:"tier,omitempty"            yaml:"tier,omitempty"`
	Validation     string           `json:"validation,omitempty"      yaml:"validation,omitempty"`
	State          types.LeaseState `json:"state,omitempty"           yaml:"state,omitempty"`
	ReadOnly       bool             `json:"read_only,omitempty"       yaml:"read_only,omitempty"`
}

// Validate reports what is wrong with a declared row, or nil.
func (r Row) Validate() error {
	if !types.ValidLeaseID(strings.TrimSpace(r.ID)) {
		return fmt.Errorf("ledger: %q is not a lease id (letters, digits and -_./: only, at most %d characters)", r.ID, types.MaxLeaseIDLen)
	}
	if r.State != "" && !types.ValidLeaseState(r.State) {
		return fmt.Errorf("ledger: state must be one of %s", stateVocabulary())
	}
	return nil
}

// stateVocabulary is the closed set as an error quotes it.
func stateVocabulary() string {
	states := types.LeaseStates()
	names := make([]string, len(states))
	for i, s := range states {
		names[i] = string(s)
	}
	return strings.Join(names, ", ")
}

// Apply writes this declaration onto a row, for [Store.Update]. Store-computed fields are
// untouched: a row that already carries releases or a registration keeps them.
func (r Row) Apply(u *types.Lease) {
	u.Parent = strings.TrimSpace(r.Parent)
	u.Goal = r.Goal
	u.Checkpoint = strings.TrimSpace(r.Checkpoint)
	u.OwnedPaths = trimmed(r.OwnedPaths)
	u.ForbiddenPaths = trimmed(r.ForbiddenPaths)
	u.Focus = trimmed(r.Focus)
	u.DependsOn = trimmed(r.DependsOn)
	u.Tier = strings.TrimSpace(r.Tier)
	u.Validation = r.Validation
	u.State = r.State
	u.ReadOnly = r.ReadOnly
}

func trimmed(in []string) []string {
	var out []string
	for _, s := range in {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// maxInputBytes bounds one decoded record. A row and a report are both a few hundred
// bytes of paths and sentences; the cap is what keeps a caller that piped the wrong file
// into stdin from being read into memory before it is rejected.
const maxInputBytes = 1 << 20

// DecodeReport reads a worker's report, and DecodeRow one declared row, from r.
//
// STRICT AND VERSIONED, in that order of reporting. The version is read first, so a
// sender a release ahead is told which versions this magus knows instead of learning that
// one of its fields is unknown; then an unknown member is an error, because a field the
// grader never reads looks to its author exactly like one that was taken into account.
//
// Every failure here is the same class to a caller: the input could not be understood, as
// opposed to understood and rejected. `magus ledger accept` exits 2 for this and 1 for a
// rejection, which is what lets a script tell "fix your report" from "the work was not
// accepted".
func DecodeReport(r io.Reader) (Report, error) {
	raw, err := readInput(r, "report")
	if err != nil {
		return Report{}, err
	}
	if err := checkVersion(raw, "report", ReportSchemaVersion); err != nil {
		return Report{}, err
	}
	var rep Report
	if err := json.UnmarshalStrict(raw, &rep); err != nil {
		return Report{}, fmt.Errorf("ledger: the report is not a version %d report: %w", ReportSchemaVersion, err)
	}
	return rep, nil
}

// DecodeRow reads one declared lease row. See [DecodeReport] for the rules; this is the
// same two passes over the row's schema.
func DecodeRow(r io.Reader) (Row, error) {
	raw, err := readInput(r, "row")
	if err != nil {
		return Row{}, err
	}
	if err := checkVersion(raw, "row", types.LeaseSchemaVersion); err != nil {
		return Row{}, err
	}
	var row Row
	if err := json.UnmarshalStrict(raw, &row); err != nil {
		return Row{}, fmt.Errorf("ledger: the input is not a version %d lease row: %w", types.LeaseSchemaVersion, err)
	}
	return row, row.Validate()
}

func readInput(r io.Reader, what string) ([]byte, error) {
	raw, err := io.ReadAll(io.LimitReader(r, maxInputBytes+1))
	if err != nil {
		return nil, fmt.Errorf("ledger: read the %s: %w", what, err)
	}
	if len(raw) > maxInputBytes {
		return nil, fmt.Errorf("ledger: the %s is larger than %d bytes, which no %s is", what, maxInputBytes, what)
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return nil, fmt.Errorf("ledger: the %s is empty", what)
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
		return fmt.Errorf("ledger: the %s is not JSON: %w", what, err)
	}
	switch v := envelope.SchemaVersion; {
	case v == 0:
		return fmt.Errorf("ledger: the %s carries no schema_version; this magus accepts version %d", what, supported)
	case v != supported:
		return fmt.Errorf("ledger: the %s is schema_version %d and this magus accepts version %d only."+
			" A newer record is not readable by an older magus; update magus, or send version %d", what, v, supported, supported)
	}
	return nil
}
