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

// DeclarationSchema is the JSON Schema for [Declaration], embedded so a person or a harness can see the
// shape `magus ledger register --stdin` accepts without reading Go. Generated from the
// struct itself, so a field cannot reach the wire undescribed.
//
//go:embed gen/job.schema.json
var DeclarationSchema string

// Declaration is the typed INPUT for one lease row: the fields a caller DECLARES, and nothing the
// store computes. A caller cannot say when its row was created or what it released, and
// the way to make that true is for the input type not to carry those fields rather than
// for the store to strip them afterwards.
//
// It is a DECLARATION and not a merge: every field it carries is written, so an omitted
// one is cleared rather than kept. The magus_job tool's fork deliberately does the
// opposite, since an agent advancing one field of a live row must not erase the rest (see
// [ParseMerge]).
//
// JSON only: this is decoded from stdin and never emitted, so it carries no yaml tags.
type Declaration struct {
	// SchemaVersion is the row shape this record is written in, and it is required: a
	// version this magus does not know is rejected by name. See types.JobSchemaVersion.
	SchemaVersion int `json:"schema_version"`
	// ID is the lease's identity within the plan, the key a second register replaces on,
	// and the only required field besides the version.
	ID string `json:"id" schema:"leaseid"`
	// Parent is the lease this one was spawned under, empty for a lease the root declared.
	Parent string `json:"parent,omitempty"`
	// Goal is the goal and its observable acceptance criteria, as one block of text.
	Goal string `json:"goal,omitempty"`
	// Checkpoint is the working state this lease starts from, as `magus vcs checkpoint -o
	// name` prints it.
	Checkpoint string `json:"checkpoint,omitempty"`
	// WritePaths is the declared write lane, empty on a read-only row by design.
	WritePaths []string `json:"write_paths,omitempty"`
	// DenyPaths are the paths inside that lane this lease may not write.
	DenyPaths []string `json:"deny_paths,omitempty"`
	// ReadPaths is the declared READ lane, widened to those projects' dependencies by the
	// guard. Empty means write_paths stands in.
	ReadPaths []string `json:"read_paths,omitempty"`
	// DependsOn names the lease ids that must land before this one.
	DependsOn []string `json:"depends_on,omitempty"`
	// Model is the model or effort tier the work was matched to, a free string because
	// hosts name their models differently.
	Model string `json:"model,omitempty"`
	// LegacyWritePaths is the pre-rename spelling of write_paths, accepted on input and
	// never emitted.
	//
	// compat(until: no client or stored ledger still sends owned_paths/focus/
	// forbidden_paths/tier; observe: grep the leases-*.json archives and the trail for the
	// old keys): the decoder is strict, so an old client's row would be refused as an
	// unknown member rather than understood. A row naming both spellings of one lane is
	// refused, since nothing here can say which one its author meant.
	LegacyWritePaths []string `json:"owned_paths,omitempty"`
	// LegacyDenyPaths is the pre-rename spelling of deny_paths. compat: see LegacyWritePaths.
	LegacyDenyPaths []string `json:"forbidden_paths,omitempty"`
	// LegacyReadPaths is the pre-rename spelling of read_paths. compat: see LegacyWritePaths.
	LegacyReadPaths []string `json:"focus,omitempty"`
	// LegacyModel is the pre-rename spelling of model. compat: see LegacyWritePaths.
	LegacyModel string `json:"tier,omitempty"`
	// Check is the one check this lease runs, and acceptance binds a worker's evidence
	// to it.
	Check *types.LeaseCheck `json:"check,omitempty"`
	// State is the row's lifecycle position, empty for a row that has not said where it
	// stands. no_return is a lease that never reported, which is not a failure.
	State types.JobState `json:"state,omitempty"`
	// ReadOnly marks a lease that gathers evidence and writes nothing, so empty write
	// paths are correct rather than missing.
	ReadOnly bool `json:"read_only,omitempty"`
	// Validation is the check as a rendered `magus run` line.
	//
	// compat(until: no client still sends a rendered line; observe: a grep of the ledger
	// archives for a row carrying `validation` and no `check`): it is what rows declared
	// before the check record existed, so it is accepted and parsed into Check. Sending
	// both is refused rather than merged, since nothing here can say which one meant it.
	Validation string `json:"validation,omitempty"`
}

// foldLegacyLanes moves a lane declared under its old name onto the field that carries it,
// refusing a row that names one lane twice.
//
// compat: see the legacy fields on [Declaration].
func (r *Declaration) foldLegacyLanes() error {
	var err error
	fold := func(name string, into *[]string, from []string) {
		switch {
		case len(from) == 0:
		case len(*into) > 0:
			err = errors.Join(err, fmt.Errorf("job: a row declares %s or its renamed spelling, not both", name))
		default:
			*into = from
		}
	}
	fold("owned_paths", &r.WritePaths, r.LegacyWritePaths)
	fold("forbidden_paths", &r.DenyPaths, r.LegacyDenyPaths)
	fold("focus", &r.ReadPaths, r.LegacyReadPaths)
	switch {
	case r.LegacyModel == "":
	case r.Model != "":
		err = errors.Join(err, errors.New("job: a row declares tier or its renamed spelling, not both"))
	default:
		r.Model = r.LegacyModel
	}
	r.LegacyWritePaths, r.LegacyDenyPaths, r.LegacyReadPaths, r.LegacyModel = nil, nil, nil, ""
	return err
}

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

// Validate reports what is wrong with a declared row, or nil.
func (r Declaration) Validate() error {
	if !types.ValidJobID(strings.TrimSpace(r.ID)) {
		return fmt.Errorf("job: %q is not a lease id (letters, digits and -_./: only, at most %d characters)", r.ID, types.MaxJobIDLen)
	}
	if r.State != "" && !types.ValidJobState(r.State) {
		return fmt.Errorf("job: state must be one of %s", stateVocabulary())
	}
	_, _, err := r.check()
	return err
}

// check is the row's declared check, from either spelling, and whether it declares one at
// all. A line that does not parse is refused HERE, at the door, rather than at grading
// time, where the row is already stored and the worker has already run something.
func (r Declaration) check() (types.LeaseCheck, bool, error) {
	line := strings.TrimSpace(r.Validation)
	switch {
	case r.Check != nil && line != "":
		return types.LeaseCheck{}, false, errors.New("job: a row carries `check` or a rendered `validation` line, not both")
	case r.Check != nil:
		parsed, err := types.ParseLeaseCheck(r.Check.Target + " " + r.Check.Project)
		if err != nil {
			return types.LeaseCheck{}, false, fmt.Errorf("job: %w", err)
		}
		parsed.Args = r.Check.Args
		return parsed, true, nil
	case line != "":
		parsed, err := types.ParseLeaseRunLine(line)
		if err != nil {
			return types.LeaseCheck{}, false, fmt.Errorf("job: %w", err)
		}
		return parsed, true, nil
	}
	return types.LeaseCheck{}, false, nil
}

// stateVocabulary is the closed set as an error quotes it.
func stateVocabulary() string {
	states := types.JobStates()
	names := make([]string, len(states))
	for i, s := range states {
		names[i] = string(s)
	}
	return strings.Join(names, ", ")
}

// Apply writes this declaration onto a row, for [Store.Update]. Store-computed fields are
// untouched: a row that already carries releases or a registration keeps them.
func (r Declaration) Apply(u *types.Job) {
	u.Parent = strings.TrimSpace(r.Parent)
	u.Goal = r.Goal
	u.Checkpoint = strings.TrimSpace(r.Checkpoint)
	u.WritePaths = trimmed(r.WritePaths)
	u.DenyPaths = trimmed(r.DenyPaths)
	u.ReadPaths = trimmed(r.ReadPaths)
	u.DependsOn = trimmed(r.DependsOn)
	u.Model = strings.TrimSpace(r.Model)
	// Validate refused an unparsable check before the row reached a store, so the error
	// here cannot fire; the rendered line is written from the record so the two agree.
	check, declared, _ := r.check()
	u.Check, u.Validation = nil, ""
	if declared {
		u.Check, u.Validation = &check, check.String()
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
func DecodeDeclaration(r io.Reader) (Declaration, error) {
	raw, err := readInput(r, "job")
	if err != nil {
		return Declaration{}, err
	}
	if err := checkVersion(raw, "job", types.JobSchemaVersion); err != nil {
		return Declaration{}, err
	}
	var row Declaration
	if err := json.UnmarshalStrict(raw, &row); err != nil {
		return Declaration{}, fmt.Errorf("job: the input is not a version %d job: %w", types.JobSchemaVersion, err)
	}
	if err := row.foldLegacyLanes(); err != nil {
		return Declaration{}, err
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
