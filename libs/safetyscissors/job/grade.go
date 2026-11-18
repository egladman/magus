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
)

// RunRef names one run the store recorded. Only the store mints one, and a ref grades only
// the job and goal it was recorded for.
type RunRef string

// GoalGrade is one check goal's grade. Passed says whether the run's check exited zero;
// Verified whether the goal holds, which also needs a basis the goal accepts, a run of the
// goal's own check and of the tree as it is now. Violations say why a goal is not verified.
type GoalGrade struct {
	Goal       string   `json:"goal"`
	Ref        RunRef   `json:"ref,omitempty"`
	Basis      Basis    `json:"basis,omitempty"`
	Passed     bool     `json:"passed"`
	Verified   bool     `json:"verified"`
	Violations []string `json:"violations,omitempty"`
}

// Grade is a job's grade from the runs it cites. Verified only when the job has a check
// goal and every one is: a job with none is not graded from runs, so it never verifies
// here.
type Grade struct {
	Job      string      `json:"job"`
	Verified bool        `json:"verified"`
	Goals    []GoalGrade `json:"goals"`
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

// GoalError is a run or a grade naming a goal Job does not declare as a check.
type GoalError struct {
	Job, Goal string
	// Kind is the goal's kind when Job declares it as another kind, and empty when Job
	// declares no such goal.
	Kind GoalKind
}

func (e *GoalError) Error() string {
	if e.Kind != "" {
		return fmt.Sprintf("job: %s's goal %q is a %s goal, and only a check goal is graded from a run", e.Job, e.Goal, e.Kind)
	}
	return fmt.Sprintf("job: %s declares no goal %q", e.Job, e.Goal)
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

// Grade grades every check goal of job id by the run refs cites for it, keyed by goal id,
// and appends the grade to grades.jsonl. A goal with no cited ref is not verified; a ref
// cited for a goal id the job does not declare as a check is a [*GoalError]. Paths and
// symbol goals are not graded here: their evidence is the diff and the graph, not a run.
//
// It needs [WithTree], to tell a run of the tree as it is from a run of an older one, and
// [WithCheckSpec], to tell a run of the goal's check from a run of another.
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
	var checks []Goal
	for _, g := range row.EffectiveGoals() {
		if g.Kind == GoalKindCheck {
			checks = append(checks, g)
		}
	}
	for _, goal := range slices.Sorted(maps.Keys(refs)) {
		if !slices.ContainsFunc(checks, func(g Goal) bool { return g.ID == goal }) {
			return Grade{}, s.goalError(row, goal)
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
		out.Goals = append(out.Goals, s.checks.gradeGoal(row, g, refs[g.ID], runs, tree))
	}
	out.Verified = len(out.Goals) > 0 && !slices.ContainsFunc(out.Goals, func(gg GoalGrade) bool { return !gg.Verified })
	if err := s.appendGrades(ctx, gradesEntry{grade: recordGrade(out, s.Now())}); err != nil {
		return Grade{}, err
	}
	return out, nil
}

func (spec *checkSpec) gradeGoal(row Job, g Goal, ref RunRef, runs map[RunRef]run, tree string) GoalGrade {
	gg := GoalGrade{Goal: g.ID, Ref: ref}
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
	if r.job != row.ID || r.goal != g.ID {
		v = append(v, fmt.Sprintf("ref %q is job %q goal %q's run, not this goal's", ref, r.job, r.goal))
	}
	if !slices.Contains(g.Accept, r.basis) {
		v = append(v, fmt.Sprintf("the run is %s and this goal accepts only %v", r.basis, g.Accept))
	}
	switch same, err := spec.equal(r.check, g.Check); {
	case err != nil:
		v = append(v, fmt.Sprintf("the run's check could not be compared with this goal's: %v", err))
	case !same:
		v = append(v, fmt.Sprintf("the run is of `%s` and this goal's check is `%s`", spec.renderCheck(r.check), spec.renderCheck(g.Check)))
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

// checkGoal is row id and its check goal named goal, the primary check included.
func (s *Store) checkGoal(ctx context.Context, id, goal string) (Job, Goal, error) {
	row, err := s.row(ctx, id)
	if err != nil {
		return Job{}, Goal{}, err
	}
	for _, g := range row.EffectiveGoals() {
		if g.ID == goal && g.Kind == GoalKindCheck {
			return row, g, nil
		}
	}
	return Job{}, Goal{}, s.goalError(row, goal)
}

func (s *Store) goalError(row Job, goal string) error {
	refused := &GoalError{Job: row.ID, Goal: goal}
	if i := slices.IndexFunc(row.EffectiveGoals(), func(g Goal) bool { return g.ID == goal }); i >= 0 {
		refused.Kind = row.EffectiveGoals()[i].Kind
	}
	return s.wrap(refused)
}

// run is one recorded run of a check goal's check.
type run struct {
	job, goal      string
	basis          Basis
	check          codec.RawMessage
	argv           []string
	dir            string
	tree           string
	exit           int
	output         string
	started, ended time.Time
	moved          bool
	signer, by     string
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
	return codec.JSONL[gradesEntry]{
		Path:   filepath.Join(filepath.Dir(path), gradesFileName),
		Perm:   0o600,
		Key:    gradesEntry.key,
		OnDrop: s.dropped,
	}, nil
}

// appendGrades appends entries holding the store's mutex and grades.jsonl's lock.
func (s *Store) appendGrades(ctx context.Context, entries ...gradesEntry) error {
	log, err := s.gradesLog()
	if err != nil {
		return err
	}
	// Made here, before the lock creates it world-readable, as a row write makes it.
	if err := os.MkdirAll(filepath.Dir(log.Path), 0o700); err != nil {
		return fmt.Errorf("job: create store %s: %w", filepath.Dir(log.Path), err)
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
// a line of another version or kind with an [*apiversion.RefusedError].
func (e *gradesEntry) UnmarshalJSON(data []byte) error {
	var probe struct{ apiversion.TypeMeta }
	if err := codec.Unmarshal(data, &probe); err != nil {
		return err
	}
	switch probe.TypeMeta {
	case runType():
		var r checkRunRecord
		if err := codec.Unmarshal(data, &r); err != nil {
			return err
		}
		*e = gradesEntry{run: &r}
	case gradeType():
		var g gradeRecord
		if err := codec.Unmarshal(data, &g); err != nil {
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
	Ref     RunRef           `json:"ref,omitempty"`
	Job     string           `json:"job"`
	Goal    string           `json:"goal"`
	Basis   string           `json:"basis"`
	Check   codec.RawMessage `json:"check"`
	Argv    []string         `json:"argv,omitempty"`
	Dir     string           `json:"dir,omitempty"`
	Tree    string           `json:"tree"`
	Exit    int              `json:"exit"`
	Output  string           `json:"output,omitempty"`
	Started int64            `json:"started"`
	Ended   int64            `json:"ended"`
	Moved   bool             `json:"moved,omitzero"`
	Signer  string           `json:"signer,omitempty"`
	By      string           `json:"by,omitempty"`
}

func recordCheckRun(r run) checkRunRecord {
	return checkRunRecord{
		TypeMeta: runType(), Job: r.job, Goal: r.goal, Basis: string(r.basis),
		Check: slices.Clone(r.check), Argv: slices.Clone(r.argv), Dir: r.dir, Tree: r.tree, Exit: r.exit,
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
		job: r.Job, goal: r.Goal, basis: basis, check: r.Check, argv: r.Argv, dir: r.Dir,
		tree: r.Tree, exit: r.Exit, output: r.Output, started: time.UnixMilli(r.Started), ended: time.UnixMilli(r.Ended),
		moved: r.Moved, signer: r.Signer, by: r.By,
	}, nil
}

// gradeRecord is a [Grade] as grades.jsonl stores it. Graded is unix milliseconds.
type gradeRecord struct {
	apiversion.TypeMeta
	Job      string            `json:"job"`
	Graded   int64             `json:"graded"`
	Verified bool              `json:"verified"`
	Goals    []goalGradeRecord `json:"goals"`
}

type goalGradeRecord struct {
	Goal       string   `json:"goal"`
	Ref        RunRef   `json:"ref,omitempty"`
	Basis      string   `json:"basis,omitempty"`
	Passed     bool     `json:"passed"`
	Verified   bool     `json:"verified"`
	Violations []string `json:"violations,omitempty"`
}

func recordGrade(g Grade, now time.Time) *gradeRecord {
	out := &gradeRecord{TypeMeta: gradeType(), Job: g.Job, Graded: now.UnixMilli(), Verified: g.Verified}
	for _, gg := range g.Goals {
		out.Goals = append(out.Goals, goalGradeRecord{
			Goal: gg.Goal, Ref: gg.Ref, Basis: string(gg.Basis), Passed: gg.Passed, Verified: gg.Verified,
			Violations: slices.Clone(gg.Violations),
		})
	}
	return out
}
