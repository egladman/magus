package ledger

import (
	_ "embed"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/types"
)

// RowSchema is the JSON Schema for [Row], embedded so a person or a harness can see the
// shape `magus ledger register --stdin` accepts without reading Go. A file beside the
// struct rather than reflection over it: the schema is the CONTRACT, and one generated
// from field tags changes shape whenever the struct's internals do.
//
//go:embed row.schema.json
var RowSchema string

// Row is the typed INPUT for one lease row: the fields a caller DECLARES, and nothing the
// store computes. A caller cannot say when its row was created or what it released, and
// the way to make that true is for the input type not to carry those fields rather than
// for the store to strip them afterwards.
//
// It is a DECLARATION and not a merge: every field it carries is written, so an omitted
// one is cleared rather than kept. The magus_ledger tool's put deliberately does the
// opposite, since an agent advancing one field of a live row must not erase the rest (see
// [ParseMerge]).
//
// JSON only: this is decoded from stdin and never emitted, so it carries no yaml tags.
type Row struct {
	// SchemaVersion is required. See types.LeaseSchemaVersion.
	SchemaVersion int `json:"schema_version"`
	// ID is the lease's identity within the plan, and the only required field besides
	// the version.
	ID string `json:"id"`
	// The rest mirror types.Lease one for one; that type documents what each one means.
	Parent         string           `json:"parent,omitempty"`
	Goal           string           `json:"goal,omitempty"`
	Checkpoint     string           `json:"checkpoint,omitempty"`
	OwnedPaths     []string         `json:"owned_paths,omitempty"`
	ForbiddenPaths []string         `json:"forbidden_paths,omitempty"`
	Focus          []string         `json:"focus,omitempty"`
	DependsOn      []string         `json:"depends_on,omitempty"`
	Tier           string           `json:"tier,omitempty"`
	Check          *types.LeaseCheck `json:"check,omitempty"`
	State          types.LeaseState  `json:"state,omitempty"`
	ReadOnly       bool              `json:"read_only,omitempty"`
	// Validation is the check as a rendered `magus run` line.
	//
	// compat(until: no client still sends a rendered line; observe: a grep of the ledger
	// archives for a row carrying `validation` and no `check`): it is what rows declared
	// before the check record existed, so it is accepted and parsed into Check. Sending
	// both is refused rather than merged, since nothing here can say which one meant it.
	Validation string `json:"validation,omitempty"`
}

// Validate reports what is wrong with a declared row, or nil.
func (r Row) Validate() error {
	if !types.ValidLeaseID(strings.TrimSpace(r.ID)) {
		return fmt.Errorf("ledger: %q is not a lease id (letters, digits and -_./: only, at most %d characters)", r.ID, types.MaxLeaseIDLen)
	}
	if r.State != "" && !types.ValidLeaseState(r.State) {
		return fmt.Errorf("ledger: state must be one of %s", stateVocabulary())
	}
	_, err := r.check()
	return err
}

// check is the row's declared check, from either spelling. A line that does not parse is
// refused HERE, at the door, rather than at grading time, where the row is already stored
// and the worker has already run something.
func (r Row) check() (*types.LeaseCheck, error) {
	line := strings.TrimSpace(r.Validation)
	switch {
	case r.Check != nil && line != "":
		return nil, errors.New("ledger: a row carries `check` or a rendered `validation` line, not both")
	case r.Check != nil:
		parsed, err := types.ParseLeaseCheck(r.Check.Target + " " + r.Check.Project)
		if err != nil {
			return nil, fmt.Errorf("ledger: %w", err)
		}
		parsed.Args = r.Check.Args
		return &parsed, nil
	case line != "":
		parsed, err := types.ParseLeaseRunLine(line)
		if err != nil {
			return nil, fmt.Errorf("ledger: %w", err)
		}
		return &parsed, nil
	}
	return nil, nil
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
	// Validate refused an unparsable check before the row reached a store, so the error
	// here cannot fire; the rendered line is written from the record so the two agree.
	check, _ := r.check()
	u.Check = check
	u.Validation = ""
	if check != nil {
		u.Validation = check.String()
	}
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
// STRICT AND VERSIONED, in that order of reporting: a sender a release ahead is told which
// versions this magus knows instead of learning that one of its fields is unknown, and
// then an unknown member is an error, because a field the grader never reads looks to its
// author exactly like one that was taken into account.
//
// Every failure here is one class to a caller: the input could not be understood, as
// opposed to understood and rejected. `magus ledger accept` exits 2 for this and 1 for a
// rejection.
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
