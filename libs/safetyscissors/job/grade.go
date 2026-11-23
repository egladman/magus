package job

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/egladman/magus/libs/apiversion"
	"github.com/egladman/magus/libs/codec"
	"github.com/egladman/magus/libs/safetyscissors/job/internal/gen"
)

// RunRef names one run the store recorded. Only the store mints one, and a ref grades only
// the job and postcondition it was recorded for.
type RunRef string

// PostconditionGrade is one check postcondition's grade. Passed says whether the run's check exited zero;
// Verified whether the postcondition holds, which also needs a basis the postcondition accepts, a run of the
// postcondition's own check and of the tree as it is now. Violations say why a postcondition is not verified.
type PostconditionGrade struct {
	Postcondition string   `json:"postcondition"`
	Ref           RunRef   `json:"ref,omitempty"`
	Basis         Basis    `json:"basis,omitempty"`
	Passed        bool     `json:"passed"`
	Verified      bool     `json:"verified"`
	Violations    []string `json:"violations,omitempty"`
}

// Grade is a job's grade from the runs it cites. Verified only when the job has a check
// postcondition and every one is: a job with none is not graded from runs, so it never verifies
// here.
type Grade struct {
	Job            string               `json:"job"`
	Verified       bool                 `json:"verified"`
	Postconditions []PostconditionGrade `json:"postconditions"`
}

// GradingUnconfiguredError is a grading call on a store opened without an option it needs.
type GradingUnconfiguredError struct {
	// Op is the call, such as "Witness", and Option the store option it needs, such as
	// "WithTree".
	Op, Option string
}

func (e *GradingUnconfiguredError) Error() string {
	return fmt.Sprintf("job: %s needs a store opened with %s, and this one was opened without it", e.Op, e.Option)
}

// PostconditionError is a run or a grade naming a postcondition Job does not declare as a check.
type PostconditionError struct {
	Job, Postcondition string
	// Kind is the postcondition's kind when Job declares it as another kind, and empty when Job
	// declares no such postcondition.
	Kind PostconditionKind
}

func (e *PostconditionError) Error() string {
	if e.Kind != "" {
		return fmt.Sprintf("job: %s's postcondition %q is a %s postcondition, and only a check postcondition is graded from a run", e.Job, e.Postcondition, e.Kind)
	}
	return fmt.Sprintf("job: %s declares no postcondition %q", e.Job, e.Postcondition)
}

// TreeError is the caller's [WithTree] digest failing. When is the moment the store asked
// for it, such as "before the run".
type TreeError struct {
	When string
	Err  error
}

func (e *TreeError) Error() string {
	return fmt.Sprintf("job: the tree could not be digested %s: %v", e.When, e.Err)
}

func (e *TreeError) Unwrap() error { return e.Err }

// Grade grades every check postcondition of job id by the run refs cites for it, keyed by postcondition id,
// and appends the grade to grades.jsonl. A postcondition with no cited ref is not verified; a ref
// cited for a postcondition id the job does not declare as a check is a [*PostconditionError]. Paths and
// symbol postconditions are not graded here: their evidence is the diff and the graph, not a run.
//
// It needs [WithTree], to tell a run of the tree as it is from a run of an older one, and
// [WithCheckSpec], to tell a run of the postcondition's check from a run of another.
func (s *Store) Grade(ctx context.Context, id string, refs map[string]RunRef) (Grade, error) {
	if err := s.needTree("Grade"); err != nil {
		return Grade{}, err
	}
	if err := s.needChecks("Grade"); err != nil {
		return Grade{}, err
	}
	row, err := s.row(ctx, id)
	if err != nil {
		return Grade{}, err
	}
	var checks []Postcondition
	for _, g := range row.EffectivePostconditions() {
		if g.Kind == PostconditionKindCheck {
			checks = append(checks, g)
		}
	}
	for _, postcondition := range slices.Sorted(maps.Keys(refs)) {
		if !slices.ContainsFunc(checks, func(g Postcondition) bool { return g.ID == postcondition }) {
			return Grade{}, s.postconditionError(row, postcondition)
		}
	}
	runs, err := s.runs()
	if err != nil {
		return Grade{}, err
	}
	tree, err := s.digestTree(ctx, "to grade")
	if err != nil {
		return Grade{}, err
	}
	out := Grade{Job: id}
	for _, g := range checks {
		out.Postconditions = append(out.Postconditions, s.checks.gradePostcondition(row, g, refs[g.ID], runs, tree))
	}
	out.Verified = len(out.Postconditions) > 0 && !slices.ContainsFunc(out.Postconditions, func(gg PostconditionGrade) bool { return !gg.Verified })
	if err := s.appendGrades(ctx, gradesEntry{grade: recordGrade(out, s.Now())}); err != nil {
		return Grade{}, err
	}
	return out, nil
}

func (spec *checkSpec) gradePostcondition(row Job, g Postcondition, ref RunRef, runs map[RunRef]run, tree string) PostconditionGrade {
	gg := PostconditionGrade{Postcondition: g.ID, Ref: ref}
	if ref == "" {
		gg.Violations = []string{"cites no run"}
		return gg
	}
	r, ok := runs[ref]
	if !ok {
		gg.Violations = []string{fmt.Sprintf("no run is recorded for ref %q", ref)}
		return gg
	}
	gg.Basis, gg.Passed = r.basis, r.exit == 0
	var v []string
	if r.job != row.ID || r.postcondition != g.ID {
		v = append(v, fmt.Sprintf("ref %q is job %q postcondition %q's run, not this postcondition's", ref, r.job, r.postcondition))
	}
	if !slices.Contains(g.Accept, r.basis) {
		v = append(v, fmt.Sprintf("the run is %s and this postcondition accepts only %v", r.basis, g.Accept))
	}
	switch same, err := spec.Equal(r.check, g.Check); {
	case err != nil:
		v = append(v, fmt.Sprintf("the run's check could not be compared with this postcondition's: %v", err))
	case !same:
		v = append(v, fmt.Sprintf("the run is of `%s` and this postcondition's check is `%s`", spec.renderCheck(r.check), spec.renderCheck(g.Check)))
	}
	if row.Created != 0 && r.started.Unix() < row.Created {
		v = append(v, "the run started before the job was declared")
	}
	if r.moved {
		v = append(v, "the tree changed while the check ran, so no one tree is what it read")
	}
	if r.tree != tree {
		v = append(v, fmt.Sprintf("the run read tree %s and the tree is now %s", shortDigest(r.tree), shortDigest(tree)))
	}
	if r.exit != 0 {
		v = append(v, fmt.Sprintf("the check exited %d", r.exit))
	}
	gg.Violations, gg.Verified = v, len(v) == 0
	return gg
}

func shortDigest(d string) string {
	if len(d) > 12 {
		return d[:12]
	}
	return d
}

// needTree refuses a grading call op on a store that cannot be graded at all.
func (s *Store) needTree(op string) error {
	if s.err != nil {
		return s.err
	}
	if s.tree == nil {
		return s.wrap(&GradingUnconfiguredError{Op: op, Option: "WithTree"})
	}
	return nil
}

func (s *Store) digestTree(ctx context.Context, when string) (string, error) {
	tree, err := s.tree(ctx)
	if err != nil {
		return "", s.wrap(&TreeError{When: when, Err: err})
	}
	return tree, nil
}

// row is the stored row id, as written: no sweep runs.
func (s *Store) row(ctx context.Context, id string) (Job, error) {
	f, err := s.Read(ctx)
	if err != nil {
		return Job{}, err
	}
	i := slices.IndexFunc(f.Jobs, func(r Job) bool { return r.ID == id })
	if i < 0 {
		return Job{}, s.wrap(fmt.Errorf("%w: %q", ErrUnknownJob, id))
	}
	return f.Jobs[i], nil
}

// checkPostcondition is row id and its check postcondition named postcondition, the primary check included.
func (s *Store) checkPostcondition(ctx context.Context, id, postcondition string) (Job, Postcondition, error) {
	row, err := s.row(ctx, id)
	if err != nil {
		return Job{}, Postcondition{}, err
	}
	for _, g := range row.EffectivePostconditions() {
		if g.ID == postcondition && g.Kind == PostconditionKindCheck {
			return row, g, nil
		}
	}
	return Job{}, Postcondition{}, s.postconditionError(row, postcondition)
}

func (s *Store) postconditionError(row Job, postcondition string) error {
	refused := &PostconditionError{Job: row.ID, Postcondition: postcondition}
	if i := slices.IndexFunc(row.EffectivePostconditions(), func(g Postcondition) bool { return g.ID == postcondition }); i >= 0 {
		refused.Kind = row.EffectivePostconditions()[i].Kind
	}
	return s.wrap(refused)
}

// run is one recorded run of a check postcondition's check.
type run struct {
	job, postcondition string
	basis              Basis
	check              Check
	argv               []string
	dir                string
	tree               string
	exit               int
	output             string
	started, ended     time.Time
	moved              bool
	signer, by         string
}

// record appends r to grades.jsonl and returns the ref it minted: the digest of the line,
// so the ref names that run and no other.
func (s *Store) record(ctx context.Context, r run) (RunRef, error) {
	rec := recordCheckRun(r)
	body, err := codec.Marshal(rec)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(body)
	rec.Ref = RunRef("run-" + hex.EncodeToString(sum[:])[:16])
	if err := s.appendGrades(ctx, gradesEntry{run: &rec}); err != nil {
		return "", err
	}
	return rec.Ref, nil
}

// runs is every run grades.jsonl holds, by ref.
func (s *Store) runs() (map[RunRef]run, error) {
	log, err := s.gradesLog()
	if err != nil {
		return nil, err
	}
	entries, err := log.Load()
	if err != nil {
		return nil, s.wrap(err)
	}
	out := map[RunRef]run{}
	for _, e := range entries {
		if e.run == nil {
			continue
		}
		r, err := e.run.run()
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", gradesFileName, err)
		}
		out[e.run.Ref] = r
	}
	return out, nil
}

func (s *Store) gradesLog() (codec.JSONL[gradesEntry], error) {
	path, err := s.Path()
	if err != nil {
		return codec.JSONL[gradesEntry]{}, err
	}
	return codec.NewJSONL(codec.JSONLConfig[gradesEntry]{
		Path:   filepath.Join(filepath.Dir(path), gradesFileName),
		Perm:   0o600,
		Key:    gradesEntry.key,
		OnDrop: s.dropped,
	})
}

// appendGrades appends entries holding the store's mutex and grades.jsonl's lock.
func (s *Store) appendGrades(ctx context.Context, entries ...gradesEntry) error {
	log, err := s.gradesLog()
	if err != nil {
		return err
	}
	// Made here, before the lock creates it world-readable, as a row write makes it.
	if err := os.MkdirAll(filepath.Dir(log.Path()), 0o700); err != nil {
		return fmt.Errorf("job: create store %s: %w", filepath.Dir(log.Path()), err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.wrap(log.Update(ctx, func([]gradesEntry) []gradesEntry { return entries }))
}

func runType() apiversion.TypeMeta {
	return apiversion.TypeMeta{APIVersion: APIVersion, Kind: "Run"}
}

func gradeType() apiversion.TypeMeta {
	return apiversion.TypeMeta{APIVersion: APIVersion, Kind: "Grade"}
}

// gradesEntry is one line of grades.jsonl: a run or a grade. A run's last line is keyed by
// its ref, and a grade's by its job, so the file keeps every run and each job's last grade.
type gradesEntry struct {
	run   *checkRunRecord
	grade *gradeRecord
}

func (e gradesEntry) key() string {
	switch {
	case e.run != nil:
		return "run\x00" + string(e.run.Ref)
	case e.grade != nil:
		return "grade\x00" + e.grade.Job
	}
	return ""
}

// MarshalJSON writes the run or the grade the entry holds, stamped with its type.
func (e gradesEntry) MarshalJSON() ([]byte, error) {
	switch {
	case e.run != nil:
		r := *e.run
		r.TypeMeta = runType()
		return codec.Marshal(r)
	case e.grade != nil:
		g := *e.grade
		g.TypeMeta = gradeType()
		return codec.Marshal(g)
	}
	return nil, errors.New("job: a grades.jsonl entry holds neither a run nor a grade")
}

// UnmarshalJSON reads a run or a grade by the type the line is stamped with, and refuses
// a line of another version or kind with an [*apiversion.RefusedError]. A line spelling
// goal or goals, the names postcondition and postconditions had before, is refused: no
// migration reads grades.jsonl, and read as it stands it would grade nothing.
func (e *gradesEntry) UnmarshalJSON(data []byte) error {
	var probe struct {
		apiversion.TypeMeta
		Goal  codec.RawMessage `json:"goal"`
		Goals codec.RawMessage `json:"goals"`
	}
	if err := codec.Unmarshal(data, &probe); err != nil {
		return err
	}
	if probe.Goal != nil || probe.Goals != nil {
		return fmt.Errorf("job: a grades.jsonl %s line spells goal, which this build reads only as postcondition", probe.Kind)
	}
	switch probe.TypeMeta {
	case runType():
		var r checkRunRecord
		if err := r.UnmarshalJSON(data); err != nil {
			return err
		}
		*e = gradesEntry{run: &r}
	case gradeType():
		var g gradeRecord
		if err := g.UnmarshalJSON(data); err != nil {
			return err
		}
		*e = gradesEntry{grade: &g}
	default:
		return &apiversion.RefusedError{Want: runType(), Got: probe.TypeMeta}
	}
	return nil
}

// checkRunRecord is a [run] as grades.jsonl stores it. Started and Ended are unix milliseconds.
type checkRunRecord struct {
	apiversion.TypeMeta
	Ref           RunRef   `json:"ref,omitempty"`
	Job           string   `json:"job"`
	Postcondition string   `json:"postcondition"`
	Basis         string   `json:"basis"`
	Check         Check    `json:"check"`
	Argv          []string `json:"argv,omitempty"`
	Dir           string   `json:"dir,omitempty"`
	Tree          string   `json:"tree"`
	Exit          int      `json:"exit"`
	Output        string   `json:"output,omitempty"`
	Started       int64    `json:"started"`
	Ended         int64    `json:"ended"`
	Moved         bool     `json:"moved,omitzero"`
	Signer        string   `json:"signer,omitempty"`
	By            string   `json:"by,omitempty"`
}

// A run or grade line refuses a member this build does not declare, as an attempt line
// does: the compaction after an older build's read would drop it.

func (r *checkRunRecord) UnmarshalJSON(data []byte) error {
	type plain checkRunRecord
	return unmarshalWithSchema(data, (*plain)(r), &jobgen.CheckRun)
}

func (r *gradeRecord) UnmarshalJSON(data []byte) error {
	type plain gradeRecord
	return unmarshalWithSchema(data, (*plain)(r), &jobgen.Grade)
}

func recordCheckRun(r run) checkRunRecord {
	return checkRunRecord{
		TypeMeta: runType(), Job: r.job, Postcondition: r.postcondition, Basis: string(r.basis),
		Check: r.check, Argv: slices.Clone(r.argv), Dir: r.dir, Tree: r.tree, Exit: r.exit,
		Output: r.output, Started: r.started.UnixMilli(), Ended: r.ended.UnixMilli(), Moved: r.moved,
		Signer: r.signer, By: r.by,
	}
}

// run is r as the domain type, refusing a stored basis no build spells.
func (r checkRunRecord) run() (run, error) {
	var bad wireReads
	basis := wireBasis().read(r.Basis, &bad)
	if err := bad.refusal(); err != nil {
		return run{}, fmt.Errorf("run %s: %w", r.Ref, err)
	}
	return run{
		job: r.Job, postcondition: r.Postcondition, basis: basis, check: r.Check, argv: r.Argv, dir: r.Dir,
		tree: r.Tree, exit: r.Exit, output: r.Output, started: time.UnixMilli(r.Started), ended: time.UnixMilli(r.Ended),
		moved: r.Moved, signer: r.Signer, by: r.By,
	}, nil
}

// gradeRecord is a [Grade] as grades.jsonl stores it. Graded is unix milliseconds.
type gradeRecord struct {
	apiversion.TypeMeta
	Job            string                     `json:"job"`
	Graded         int64                      `json:"graded"`
	Verified       bool                       `json:"verified"`
	Postconditions []postconditionGradeRecord `json:"postconditions"`
}

type postconditionGradeRecord struct {
	Postcondition string   `json:"postcondition"`
	Ref           RunRef   `json:"ref,omitempty"`
	Basis         string   `json:"basis,omitempty"`
	Passed        bool     `json:"passed"`
	Verified      bool     `json:"verified"`
	Violations    []string `json:"violations,omitempty"`
}

func recordGrade(g Grade, now time.Time) *gradeRecord {
	out := &gradeRecord{TypeMeta: gradeType(), Job: g.Job, Graded: now.UnixMilli(), Verified: g.Verified}
	for _, gg := range g.Postconditions {
		out.Postconditions = append(out.Postconditions, postconditionGradeRecord{
			Postcondition: gg.Postcondition, Ref: gg.Ref, Basis: string(gg.Basis), Passed: gg.Passed, Verified: gg.Verified,
			Violations: slices.Clone(gg.Violations),
		})
	}
	return out
}
