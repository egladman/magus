package job

import (
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/egladman/magus/libs/apiversion"
	"github.com/egladman/magus/libs/codec"
	"github.com/egladman/magus/libs/safetyscissors/internal/apigroup"
	"github.com/egladman/magus/libs/safetyscissors/tier"
)

// jobRecord is one row of jobs.jsonl, the persistence type of
// [Job] (ADR 0030). [recordJob] and [jobRecord.job] are the one
// conversion. Unknown, here and on spec and status, is what a newer
// writer stored, carried back out unchanged.
type jobRecord struct {
	apiversion.TypeMeta
	Metadata apiversion.Metadata         `json:"metadata"`
	Spec     jobSpec                     `json:"spec"`
	Status   jobStatus                   `json:"status"`
	Unknown  map[string]codec.RawMessage `json:"-" codec:"unknown"`
}

// jobSpec is what a declaration writes.
type jobSpec struct {
	Parent       string                      `json:"parent,omitempty"`
	Agent        *agentRecord                `json:"agent,omitzero"`
	Checkpoint   string                      `json:"checkpoint,omitempty"`
	Paths        Paths                       `json:"paths,omitzero"`
	DependsOn    []string                    `json:"dependsOn,omitempty"`
	Integrates   []string                    `json:"integrates,omitempty"`
	Breaks       []string                    `json:"breaks,omitempty"`
	BriefFormat  string                      `json:"briefFormat,omitempty"`
	Delivery     string                      `json:"delivery,omitempty"`
	ParentHead   string                      `json:"parentHead,omitempty"`
	Follows      string                      `json:"follows,omitempty"`
	MaxAttempts  int                         `json:"maxAttempts,omitzero"`
	MaxClaims    int                         `json:"maxClaims,omitzero"`
	Deferred     []Deferred                  `json:"deferred,omitempty"`
	LeftUnmerged string                      `json:"leftUnmerged,omitempty"`
	Weight       float64                     `json:"weight,omitzero"`
	Check        *leaseCheckRecord           `json:"check,omitzero"`
	Validation   string                      `json:"validation,omitempty"`
	Goals        []goalRecord                `json:"goals,omitempty"`
	ReadOnly     bool                        `json:"readOnly,omitzero"`
	Deviations   []Deviation                 `json:"deviations,omitempty"`
	Unknown      map[string]codec.RawMessage `json:"-" codec:"unknown"`
}

// jobStatus is what the store computes. Its conditions are the row's
// lifecycle; see [lifecycleConditions].
type jobStatus struct {
	Conditions      []apiversion.Condition      `json:"conditions,omitempty"`
	ModelTier       *modelTierRecord            `json:"modelTier,omitzero"`
	ReworkRounds    int                         `json:"reworkRounds,omitzero"`
	Widenings       int                         `json:"widenings,omitzero"`
	Claims          []claimRecord               `json:"claims,omitempty"`
	Landed          bool                        `json:"landed,omitzero"`
	Paused          int64                       `json:"paused,omitzero"`
	PausedBy        originRecord                `json:"pausedBy,omitzero"`
	PauseReason     string                      `json:"pauseReason,omitempty"`
	PausedUntil     int64                       `json:"pausedUntil,omitzero"`
	Rejection       *rejectionRecord            `json:"rejection,omitzero"`
	Releases        []releaseRecord             `json:"releases,omitempty"`
	Unattributed    []UnattributedWrite         `json:"unattributed,omitempty"`
	Entries         []entryRecord               `json:"entries,omitempty"`
	ReleaseRequests []ReleaseRequest            `json:"releaseRequests,omitempty"`
	WriteProof      string                      `json:"writeProof,omitempty"`
	Validated       int64                       `json:"validated,omitzero"`
	ReportedBase    string                      `json:"reportedBase,omitempty"`
	BaseVerdict     string                      `json:"baseVerdict,omitempty"`
	RegisteredBy    originRecord                `json:"registeredBy,omitzero"`
	Authority       string                      `json:"authority,omitempty"`
	Registered      int64                       `json:"registered,omitzero"`
	HolderStopped   int64                       `json:"holderStopped,omitzero"`
	CheckoutRoot    string                      `json:"checkoutRoot,omitempty"`
	Worktree        *worktreeRecord             `json:"worktree,omitzero"`
	EndReason       string                      `json:"endReason,omitempty"`
	Created         int64                       `json:"created"`
	Updated         int64                       `json:"updated"`
	Deadline        int64                       `json:"deadline,omitzero"`
	Attempt         *runAttemptRecord           `json:"attempt,omitzero"`
	GateAttempts    []gateAttemptRecord         `json:"gateAttempts,omitempty"`
	LastRun         *runRecord                  `json:"lastRun,omitzero"`
	Integration     *integrationRecord          `json:"integration,omitzero"`
	WaiterEvidence  *integrationRecord          `json:"waiterEvidence,omitzero"`
	Unknown         map[string]codec.RawMessage `json:"-" codec:"unknown"`
}

// MarshalJSON writes the record with the members it kept in Unknown, so a member a
// newer writer stored goes back out under encoding/json and encoding/json/v2 alike.
func (r jobRecord) MarshalJSON() ([]byte, error) {
	type plain jobRecord
	return codec.MarshalWithUnknown(plain(r), r.Unknown)
}

// UnmarshalJSON reads the record, keeping the members it does not declare in Unknown.
func (r *jobRecord) UnmarshalJSON(data []byte) error {
	type plain jobRecord
	var p plain
	unknown, err := codec.UnmarshalWithUnknown(data, &p)
	if err != nil {
		return err
	}
	*r = jobRecord(p)
	r.Unknown = unknown
	return nil
}

// MarshalJSON writes the spec with the members it kept in Unknown.
func (s jobSpec) MarshalJSON() ([]byte, error) {
	type plain jobSpec
	return codec.MarshalWithUnknown(plain(s), s.Unknown)
}

// UnmarshalJSON reads the spec, keeping the members it does not declare in Unknown.
func (s *jobSpec) UnmarshalJSON(data []byte) error {
	type plain jobSpec
	var p plain
	unknown, err := codec.UnmarshalWithUnknown(data, &p)
	if err != nil {
		return err
	}
	*s = jobSpec(p)
	s.Unknown = unknown
	return nil
}

// MarshalJSON writes the status with the members it kept in Unknown.
func (st jobStatus) MarshalJSON() ([]byte, error) {
	type plain jobStatus
	return codec.MarshalWithUnknown(plain(st), st.Unknown)
}

// UnmarshalJSON reads the status, keeping the members it does not declare in Unknown.
func (st *jobStatus) UnmarshalJSON(data []byte) error {
	type plain jobStatus
	var p plain
	unknown, err := codec.UnmarshalWithUnknown(data, &p)
	if err != nil {
		return err
	}
	*st = jobStatus(p)
	st.Unknown = unknown
	return nil
}

// recordJob is row as the line jobs.jsonl stores. stored is the row's
// record as read, nil for a new row: what it carries that row cannot say
// is kept.
// creds spells the credential kinds the store's caller admits.
func recordJob(row Job, stored *jobRecord, creds wireEnum[CredentialKind]) jobRecord {
	var prev jobRecord
	if stored != nil {
		prev = *stored
	}
	features := make([]apiversion.Feature, len(row.Requires))
	for i, f := range row.Requires {
		features[i] = apiversion.Feature(f)
	}
	meta := prev.Metadata.WithRequires(recordGroup, features)
	meta.Name, meta.Generation = row.ID, int64(row.Generation)
	return jobRecord{TypeMeta: jobType(), Metadata: meta, Spec: recordSpec(row, prev.Spec.Unknown),
		Status: recordStatus(row, prev.Status, creds), Unknown: prev.Unknown}
}

// recordSpec is the declared half of row's record.
func recordSpec(row Job, unknown map[string]codec.RawMessage) jobSpec {
	spec := jobSpec{
		Parent: row.Parent, Agent: recordAgent(row.Agent), Checkpoint: row.Checkpoint,
		Paths:     row.Paths,
		DependsOn: row.DependsOn, Integrates: row.Integrates, Breaks: row.Breaks,
		BriefFormat: wireBriefFormat().spell(row.BriefFormat), Delivery: row.Delivery, ParentHead: row.ParentHead,
		Follows: row.Follows, MaxAttempts: row.MaxAttempts, MaxClaims: row.MaxClaims,
		Deferred: row.Deferred, LeftUnmerged: row.LeftUnmerged, Weight: row.Weight,
		Validation: row.Validation, ReadOnly: row.ReadOnly, Deviations: row.Deviations,
		Unknown: unknown,
	}
	if row.Check != nil {
		c := leaseCheckRecord(*row.Check)
		spec.Check = &c
	}
	for _, g := range row.Goals {
		spec.Goals = append(spec.Goals, recordGoal(g))
	}
	return spec
}

// recordStatus is the computed half of row's record. prev is the stored
// status, whose conditions and unknown members carry over.
func recordStatus(row Job, prev jobStatus, creds wireEnum[CredentialKind]) jobStatus {
	st := jobStatus{
		Conditions:   lifecycleConditions(row, prev.Conditions),
		ModelTier:    recordModelTier(row.ModelTier),
		ReworkRounds: row.ReworkRounds, Widenings: row.Widenings, Landed: row.Landed,
		Paused: row.Paused, PausedBy: recordOrigin(row.PausedBy, creds), PauseReason: row.PauseReason, PausedUntil: row.PausedUntil,
		Rejection:    recordRejection(row.Rejection),
		Unattributed: row.Unattributed, ReleaseRequests: row.ReleaseRequests,
		WriteProof: wireWriteProof().spell(row.WriteProof), Validated: row.Validated,
		ReportedBase: row.ReportedBase, BaseVerdict: wireBaseVerdict().spell(row.BaseVerdict),
		RegisteredBy: recordOrigin(row.RegisteredBy, creds), Authority: row.Authority, Registered: row.Registered,
		HolderStopped: row.HolderStopped, CheckoutRoot: row.CheckoutRoot, EndReason: row.EndReason,
		Created: row.Created, Updated: row.Updated, Deadline: row.Deadline,
		Integration: recordIntegration(row.Integration), WaiterEvidence: recordIntegration(row.WaiterEvidence),
		Unknown: prev.Unknown,
	}
	for _, c := range row.Claims {
		st.Claims = append(st.Claims, claimRecord{
			Path: c.Path, Evidence: claimEvidenceRecord{Kind: wireClaimKind().spell(c.Evidence.Kind), Subject: c.Evidence.Subject},
			Proof: c.Proof, At: c.At, By: recordOrigin(c.By, creds),
		})
	}
	for _, rel := range row.Releases {
		st.Releases = append(st.Releases, releaseRecord(rel))
	}
	for _, e := range row.Entries {
		st.Entries = append(st.Entries, entryRecord{Path: e.Path, By: recordOrigin(e.By, creds), At: e.At, Consumed: e.Consumed})
	}
	recordRuns(&st, row)
	return st
}

// recordRuns sets the worktree and the run evidence of st from row.
func recordRuns(st *jobStatus, row Job) {
	if row.Worktree != nil {
		w := worktreeRecord(*row.Worktree)
		st.Worktree = &w
	}
	if row.Attempt != nil {
		a := runAttemptRecord(*row.Attempt)
		st.Attempt = &a
	}
	for _, g := range row.GateAttempts {
		st.GateAttempts = append(st.GateAttempts, gateAttemptRecord{GateID: g.GateID, Attempt: runAttemptRecord(g.Attempt)})
	}
	if row.LastRun != nil {
		run := runRecord(*row.LastRun)
		st.LastRun = &run
	}
}

// job is r as the domain type, refusing a record of another kind or
// version. creds reads the credential kinds the store's caller admits.
func (r jobRecord) job(creds wireEnum[CredentialKind]) (Job, error) {
	if r.TypeMeta != jobType() {
		return Job{}, &apiversion.RefusedError{Want: jobType(), Got: r.TypeMeta}
	}
	state, err := lifecycleState(r.Status.Conditions)
	if err != nil {
		return Job{}, fmt.Errorf("job %s: %w", r.Metadata.Name, err)
	}
	var bad wireReads
	row := r.Spec.job(&bad)
	r.Status.populate(&row, &bad, creds)
	if err := bad.refusal(); err != nil {
		return Job{}, fmt.Errorf("job %s: %w", r.Metadata.Name, err)
	}
	row.ID, row.Generation, row.State = r.Metadata.Name, int(r.Metadata.Generation), state
	for _, f := range r.Metadata.Requires(recordGroup) {
		row.Requires = append(row.Requires, string(f))
	}
	row.Unknown = unknownMembers(r.Unknown, r.Spec.Unknown, r.Status.Unknown)
	return row, nil
}

// job is s as the declared members of a domain row.
func (s jobSpec) job(bad *wireReads) Job {
	row := Job{
		Parent: s.Parent, Agent: s.Agent.spec(bad), Checkpoint: s.Checkpoint,
		Paths:     s.Paths,
		DependsOn: s.DependsOn, Integrates: s.Integrates, Breaks: s.Breaks,
		BriefFormat: wireBriefFormat().read(s.BriefFormat, bad), Delivery: s.Delivery, ParentHead: s.ParentHead,
		Follows: s.Follows, MaxAttempts: s.MaxAttempts, MaxClaims: s.MaxClaims,
		Deferred: s.Deferred, LeftUnmerged: s.LeftUnmerged, Weight: s.Weight,
		Validation: s.Validation, ReadOnly: s.ReadOnly, Deviations: s.Deviations,
	}
	if s.Check != nil {
		c := LeaseCheck(*s.Check)
		row.Check = &c
	}
	for _, g := range s.Goals {
		row.Goals = append(row.Goals, g.goal(bad))
	}
	return row
}

// populate sets the computed members of row from st.
func (st jobStatus) populate(row *Job, bad *wireReads, creds wireEnum[CredentialKind]) {
	row.ModelTier = st.ModelTier.tier(bad)
	row.ReworkRounds, row.Widenings, row.Landed = st.ReworkRounds, st.Widenings, st.Landed
	row.Paused, row.PausedBy, row.PauseReason, row.PausedUntil = st.Paused, st.PausedBy.origin(bad, creds), st.PauseReason, st.PausedUntil
	row.Rejection = st.Rejection.rejection()
	row.Unattributed, row.ReleaseRequests = st.Unattributed, st.ReleaseRequests
	row.WriteProof, row.Validated = wireWriteProof().read(st.WriteProof, bad), st.Validated
	row.ReportedBase, row.BaseVerdict = st.ReportedBase, wireBaseVerdict().read(st.BaseVerdict, bad)
	row.RegisteredBy, row.Authority, row.Registered = st.RegisteredBy.origin(bad, creds), st.Authority, st.Registered
	row.HolderStopped, row.CheckoutRoot, row.EndReason = st.HolderStopped, st.CheckoutRoot, st.EndReason
	row.Created, row.Updated, row.Deadline = st.Created, st.Updated, st.Deadline
	row.Integration, row.WaiterEvidence = st.Integration.integration(), st.WaiterEvidence.integration()
	for _, c := range st.Claims {
		row.Claims = append(row.Claims, Claim{
			Path: c.Path, Evidence: ClaimEvidence{Kind: wireClaimKind().read(c.Evidence.Kind, bad), Subject: c.Evidence.Subject},
			Proof: c.Proof, At: c.At, By: c.By.origin(bad, creds),
		})
	}
	for _, rel := range st.Releases {
		row.Releases = append(row.Releases, Release(rel))
	}
	for _, e := range st.Entries {
		row.Entries = append(row.Entries, Entry{Path: e.Path, By: e.By.origin(bad, creds), At: e.At, Consumed: e.Consumed})
	}
	if st.Worktree != nil {
		w := Worktree(*st.Worktree)
		row.Worktree = &w
	}
	if st.Attempt != nil {
		a := CapturedRun(*st.Attempt)
		row.Attempt = &a
	}
	for _, g := range st.GateAttempts {
		row.GateAttempts = append(row.GateAttempts, GateAttempt{GateID: g.GateID, Attempt: CapturedRun(g.Attempt)})
	}
	if st.LastRun != nil {
		run := Run(*st.LastRun)
		row.LastRun = &run
	}
}

// unknownMembers names the members of top, spec and status, as
// [Job.Unknown] spells them.
func unknownMembers(top, spec, status map[string]codec.RawMessage) []string {
	if len(top)+len(spec)+len(status) == 0 {
		return nil
	}
	out := make([]string, 0, len(top)+len(spec)+len(status))
	for k := range top {
		out = append(out, k)
	}
	for k := range spec {
		out = append(out, "spec."+k)
	}
	for k := range status {
		out = append(out, "status."+k)
	}
	slices.Sort(out)
	return out
}

// lifecycleCondition is the condition a state is stored as, True.
type lifecycleCondition struct {
	state        State
	kind, reason string
}

// lifecycle is every state's condition. Failed takes two reasons, as a
// Kubernetes Job's Failed does.
func lifecycle() []lifecycleCondition {
	return []lifecycleCondition{
		{StateDeclared, "Declared", "Declared"},
		{StateRunning, "Running", "Running"},
		{StateExited, "Exited", "Exited"},
		{StatePass, "Complete", "Passed"},
		{StateFail, "Failed", "CriteriaUnmet"},
		{StateNoReturn, "Failed", "NoReturn"},
	}
}

// lifecycleConditions are row's conditions: its state's, then every
// condition of stored no state names, kept as stored. A state's condition
// keeps its stored transition time; a new one transitioned at Updated.
func lifecycleConditions(row Job, stored []apiversion.Condition) []apiversion.Condition {
	var out []apiversion.Condition
	for _, l := range lifecycle() {
		if l.state != row.State {
			continue
		}
		c := apiversion.Condition{
			Type: l.kind, Status: apiversion.ConditionTrue, ObservedGeneration: int64(row.Generation),
			LastTransitionTime: time.Unix(row.Updated, 0).UTC(), Reason: l.reason,
		}
		for _, s := range stored {
			if s.Type == c.Type && s.Reason == c.Reason && s.Status == c.Status {
				c.LastTransitionTime = s.LastTransitionTime
			}
		}
		out = append(out, c)
	}
	for _, s := range stored {
		if !slices.ContainsFunc(lifecycle(), func(l lifecycleCondition) bool { return l.kind == s.Type }) {
			out = append(out, s)
		}
	}
	return out
}

// lifecycleState is the state conditions hold True, "" for none. Two
// is a record no writer stores.
func lifecycleState(conditions []apiversion.Condition) (State, error) {
	var state State
	for _, c := range conditions {
		if c.Status != apiversion.ConditionTrue {
			continue
		}
		for _, l := range lifecycle() {
			if l.kind != c.Type || l.reason != c.Reason {
				continue
			}
			if state != "" {
				return "", fmt.Errorf("the conditions hold both %s and %s", state, l.state)
			}
			state = l.state
		}
	}
	return state, nil
}

// wireEnum spells an enum's lowercase Go values in CamelCase on a record.
// It writes a value it does not list unchanged, and refuses to read one.
type wireEnum[T ~string] struct {
	wire map[T]string
	gov  map[string]T
}

// wireReads collects the stored spellings no wireEnum lists, so a record
// carrying one is refused with an error rather than read.
type wireReads struct{ errs []error }

func (r *wireReads) refusal() error { return errors.Join(r.errs...) }

func newWireEnum[T ~string](values ...T) wireEnum[T] {
	e := wireEnum[T]{wire: map[T]string{}, gov: map[string]T{}}
	for _, v := range values {
		w := camelCase(string(v))
		e.wire[v], e.gov[w] = w, v
	}
	return e
}

func (e wireEnum[T]) spell(v T) string {
	if w, ok := e.wire[v]; ok {
		return w
	}
	return string(v)
}

// read is the value w spells, empty for an empty w. A spelling e does not
// list goes on bad: a reader switching on the value meets only values it knows.
func (e wireEnum[T]) read(w string, bad *wireReads) T {
	if v, ok := e.gov[w]; ok || w == "" {
		return v
	}
	bad.errs = append(bad.errs, fmt.Errorf("%q is not a stored %s", w, reflect.TypeFor[T]().Name()))
	return ""
}

// camelCase spells words joined by '_' or '-' as one CamelCase word.
func camelCase(v string) string {
	var b strings.Builder
	for word := range strings.FieldsFuncSeq(v, func(r rune) bool { return r == '_' || r == '-' }) {
		b.WriteString(strings.ToUpper(word[:1]) + word[1:])
	}
	return b.String()
}

func wireWriteProof() wireEnum[WriteProof] {
	return newWireEnum(WriteProofAlone, WriteProofDisjoint, WriteProofOverlapping)
}

func wireBaseVerdict() wireEnum[BaseVerdict] {
	return newWireEnum(BaseMatch, BaseRevisionMatch, BaseDiverged, BaseUnknown)
}

func wireBriefFormat() wireEnum[BriefFormat] {
	return newWireEnum(BriefFormatMarkdown, BriefFormatBuzz)
}
func wireGoalKind() wireEnum[GoalKind]     { return newWireEnum(GoalKinds()...) }
func wireGoalExpect() wireEnum[GoalExpect] { return newWireEnum(GoalExpects()...) }

func wireClaimKind() wireEnum[ClaimKind] {
	return newWireEnum(ClaimRefs, ClaimTest, ClaimOutput, ClaimReleased, ClaimDeadHolder)
}

func wireEntryPoint() wireEnum[EntryPoint] {
	return newWireEnum(EntryPointCLI, EntryPointHook, EntryPointMCP, EntryPointRPC, EntryPointServer)
}

func wireWorkerType() wireEnum[WorkerType] { return newWireEnum(WorkerTypeResumed, WorkerTypeNew) }

func wireTierSource() wireEnum[tier.Source] {
	return newWireEnum(tier.SourceJob, tier.SourceMatrix, tier.SourceUserMatrix, tier.SourceDefault)
}

func wireSkills() wireEnum[tier.SkillVariant] {
	return newWireEnum(tier.SkillVariantShort, tier.SkillVariantFull)
}

// The records below mirror domain types under camelCase keys. A domain
// type whose keys are all one word is stored as it is.

type agentRecord struct {
	Model        Model        `json:"model,omitzero"`
	Instructions string       `json:"instructions,omitempty"`
	Worker       workerRecord `json:"worker"`
}

type workerRecord struct {
	Type   string `json:"type"`
	Reason string `json:"reason,omitempty"`
}

func recordAgent(s *Agent) *agentRecord {
	if s == nil {
		return nil
	}
	return &agentRecord{Model: s.Model, Instructions: s.Instructions,
		Worker: workerRecord{Type: wireWorkerType().spell(s.Worker.Type), Reason: s.Worker.Reason}}
}

func (r *agentRecord) spec(bad *wireReads) *Agent {
	if r == nil {
		return nil
	}
	return &Agent{Model: r.Model, Instructions: r.Instructions,
		Worker: Worker{Type: wireWorkerType().read(r.Worker.Type, bad), Reason: r.Worker.Reason}}
}

type leaseCheckRecord struct {
	Target          string   `json:"target"`
	Script          string   `json:"script,omitempty"`
	Project         string   `json:"project,omitempty"`
	Args            []string `json:"args,omitempty"`
	NoDefaultCharms bool     `json:"noDefaultCharms,omitzero"`
	Test            bool     `json:"test,omitzero"`
}

type goalRecord struct {
	ID          string           `json:"id"`
	Description string           `json:"description,omitempty"`
	DependsOn   []string         `json:"dependsOn,omitempty"`
	Kind        string           `json:"kind"`
	Expect      string           `json:"expect,omitempty"`
	Check       leaseCheckRecord `json:"check,omitzero"`
	Paths       []string         `json:"paths,omitempty"`
	Symbols     []string         `json:"symbols,omitempty"`
	Signature   string           `json:"signature,omitempty"`
	// Accept is written by every build since [AcceptWitnessedMigration], which wrote it
	// onto each check goal stored before it, so a reader never defaults it.
	Accept []string `json:"accept,omitempty"`
}

func recordGoal(g Goal) goalRecord {
	r := goalRecord{ID: g.ID, Description: g.Description, DependsOn: g.DependsOn,
		Kind: wireGoalKind().spell(g.Kind), Expect: wireGoalExpect().spell(g.Expect), Check: leaseCheckRecord(g.Check),
		Paths: g.Paths, Symbols: g.Symbols, Signature: g.Signature}
	bases := wireBasis()
	for _, b := range g.Accept {
		r.Accept = append(r.Accept, bases.spell(b))
	}
	return r
}

func (r goalRecord) goal(bad *wireReads) Goal {
	g := Goal{ID: r.ID, Description: r.Description, DependsOn: r.DependsOn,
		Kind: wireGoalKind().read(r.Kind, bad), Expect: wireGoalExpect().read(r.Expect, bad), Check: LeaseCheck(r.Check),
		Paths: r.Paths, Symbols: r.Symbols, Signature: r.Signature}
	bases := wireBasis()
	for _, w := range r.Accept {
		g.Accept = append(g.Accept, bases.read(w, bad))
	}
	return g
}

type modelTierRecord struct {
	Name   string   `json:"name"`
	Rank   int      `json:"rank"`
	Skills string   `json:"skills"`
	Source string   `json:"source"`
	Reason string   `json:"reason,omitempty"`
	Site   string   `json:"site,omitempty"`
	Model  string   `json:"model,omitempty"`
	Shared []string `json:"shared,omitempty"`
}

func recordModelTier(t *tier.Assignment) *modelTierRecord {
	if t == nil {
		return nil
	}
	return &modelTierRecord{Name: t.Name, Rank: t.Rank, Skills: wireSkills().spell(t.Skills), Source: wireTierSource().spell(t.Source),
		Reason: t.Reason, Site: t.Site, Model: t.Model, Shared: t.Shared}
}

func (r *modelTierRecord) tier(bad *wireReads) *tier.Assignment {
	if r == nil {
		return nil
	}
	return &tier.Assignment{Name: r.Name, Rank: r.Rank, Skills: wireSkills().read(r.Skills, bad), Source: wireTierSource().read(r.Source, bad),
		Reason: r.Reason, Site: r.Site, Model: r.Model, Shared: r.Shared}
}

type originRecord struct {
	User       string           `json:"user,omitempty"`
	UID        string           `json:"uid,omitempty"`
	EntryPoint string           `json:"entryPoint,omitempty"`
	Host       string           `json:"host,omitempty"`
	Session    string           `json:"session,omitempty"`
	Agent      string           `json:"agent,omitzero"`
	Credential credentialRecord `json:"credential,omitzero"`
}

type credentialRecord struct {
	Kind string `json:"class,omitempty"`
	ID   string `json:"id,omitempty"`
	Name string `json:"name,omitempty"`
}

func recordOrigin(o Origin, creds wireEnum[CredentialKind]) originRecord {
	return originRecord{User: o.User, UID: o.UID, EntryPoint: wireEntryPoint().spell(o.EntryPoint), Host: o.Host,
		Session: o.Session, Agent: o.Agent, Credential: credentialRecord{
			Kind: creds.spell(o.Credential.Kind), ID: o.Credential.ID, Name: o.Credential.Name,
		}}
}

func (r originRecord) origin(bad *wireReads, creds wireEnum[CredentialKind]) Origin {
	return Origin{User: r.User, UID: r.UID, EntryPoint: wireEntryPoint().read(r.EntryPoint, bad), Host: r.Host,
		Session: r.Session, Agent: r.Agent, Credential: Credential{
			Kind: creds.read(r.Credential.Kind, bad), ID: r.Credential.ID, Name: r.Credential.Name,
		}}
}

type claimRecord struct {
	Path     string              `json:"path"`
	Evidence claimEvidenceRecord `json:"evidence"`
	Proof    string              `json:"proof"`
	At       int64               `json:"at"`
	By       originRecord        `json:"by"`
}

type claimEvidenceRecord struct {
	Kind    string `json:"kind"`
	Subject string `json:"subject,omitempty"`
}

type releaseRecord struct {
	Path        string `json:"path"`
	Digest      string `json:"digest"`
	ReleasedAt  int64  `json:"releasedAt"`
	Revoked     bool   `json:"revoked,omitzero"`
	TakenBy     string `json:"takenBy,omitempty"`
	DeadBecause string `json:"deadBecause,omitempty"`
	Narrowed    bool   `json:"narrowed,omitzero"`
}

type entryRecord struct {
	Path     string       `json:"path"`
	By       originRecord `json:"by"`
	At       int64        `json:"at"`
	Consumed int64        `json:"consumed,omitzero"`
}

type verificationRecord struct {
	ID         string   `json:"id"`
	Verified   bool     `json:"verified"`
	OutputRef  string   `json:"outputRef,omitempty"`
	Scope      string   `json:"scope,omitempty"`
	Owed       string   `json:"owed,omitempty"`
	Violations []string `json:"violations,omitempty"`
}

func recordVerifications(in []VerificationStatus) []verificationRecord {
	if len(in) == 0 {
		return nil
	}
	out := make([]verificationRecord, 0, len(in))
	for _, v := range in {
		out = append(out, verificationRecord(v))
	}
	return out
}

func verifications(in []verificationRecord) []VerificationStatus {
	if len(in) == 0 {
		return nil
	}
	out := make([]VerificationStatus, 0, len(in))
	for _, v := range in {
		out = append(out, VerificationStatus(v))
	}
	return out
}

type rejectionRecord struct {
	Unmet              []verificationRecord `json:"unmet,omitempty"`
	Violations         []string             `json:"violations,omitempty"`
	At                 int64                `json:"at"`
	ObservedGeneration int                  `json:"observedGeneration,omitzero"`
}

func recordRejection(r *Rejection) *rejectionRecord {
	if r == nil {
		return nil
	}
	return &rejectionRecord{Unmet: recordVerifications(r.Unmet), Violations: r.Violations, At: r.At, ObservedGeneration: r.ObservedGeneration}
}

func (r *rejectionRecord) rejection() *Rejection {
	if r == nil {
		return nil
	}
	return &Rejection{Unmet: verifications(r.Unmet), Violations: r.Violations, At: r.At, ObservedGeneration: r.ObservedGeneration}
}

type worktreeRecord struct {
	Root         string `json:"root"`
	Source       string `json:"source"`
	Base         string `json:"base"`
	Created      int64  `json:"created"`
	Binary       string `json:"binary,omitempty"`
	BinaryDigest string `json:"binaryDigest,omitempty"`
	Removed      int64  `json:"removed,omitzero"`
	Kept         string `json:"kept,omitempty"`
	Made         bool   `json:"made,omitzero"`
}

type runAttemptRecord struct {
	Found              bool     `json:"found"`
	Ref                string   `json:"ref,omitempty"`
	TimestampMs        int64    `json:"timestampMs,omitzero"`
	Project            string   `json:"project,omitempty"`
	Target             string   `json:"target,omitempty"`
	Spell              string   `json:"spell,omitempty"`
	Failed             bool     `json:"failed,omitzero"`
	AttemptID          string   `json:"attemptId,omitempty"`
	DurationMs         int64    `json:"durationMs,omitzero"`
	Revision           string   `json:"revision,omitempty"`
	Dirty              bool     `json:"dirty,omitzero"`
	Lease              string   `json:"lease,omitempty"`
	ExtraArgs          []string `json:"extraArgs,omitempty"`
	ObservedGeneration int      `json:"observedGeneration,omitzero"`
}

type gateAttemptRecord struct {
	GateID  string           `json:"gateId"`
	Attempt runAttemptRecord `json:"attempt"`
}

type runRecord struct {
	Invocation     string `json:"invocation,omitempty"`
	Ended          int64  `json:"ended,omitzero"`
	DurationMs     int64  `json:"durationMs,omitzero"`
	OK             bool   `json:"ok"`
	Error          string `json:"error,omitempty"`
	ItemsRemoved   int64  `json:"itemsRemoved,omitzero"`
	BytesReclaimed int64  `json:"bytesReclaimed,omitzero"`
}

type integrationRecord struct {
	Checkout           string               `json:"checkout"`
	At                 int64                `json:"at"`
	Verified           bool                 `json:"verified"`
	Gates              []verificationRecord `json:"gates,omitempty"`
	ObservedGeneration int                  `json:"observedGeneration,omitzero"`
}

func recordIntegration(i *Integration) *integrationRecord {
	if i == nil {
		return nil
	}
	return &integrationRecord{Checkout: i.Checkout, At: i.At, Verified: i.Verified, Gates: recordVerifications(i.Gates), ObservedGeneration: i.ObservedGeneration}
}

func (r *integrationRecord) integration() *Integration {
	if r == nil {
		return nil
	}
	return &Integration{Checkout: r.Checkout, At: r.At, Verified: r.Verified, Gates: verifications(r.Gates), ObservedGeneration: r.ObservedGeneration}
}

// RecordAPIVersion is the apiVersion of every line of jobs.jsonl and
// attempts.jsonl (ADR 0044). A line stamped with another is refused.
const RecordAPIVersion apiversion.APIVersion = apigroup.Job

// recordGroup prefixes the annotations a record carries.
const recordGroup = "safetyscissors.job"

func jobListType() apiversion.TypeMeta {
	return apiversion.TypeMeta{APIVersion: RecordAPIVersion, Kind: "JobList"}
}

func jobType() apiversion.TypeMeta {
	return apiversion.TypeMeta{APIVersion: RecordAPIVersion, Kind: "Job"}
}

func attemptType() apiversion.TypeMeta {
	return apiversion.TypeMeta{APIVersion: RecordAPIVersion, Kind: "Attempt"}
}

// attemptRecord is one line of attempts.jsonl, the persistence type of
// [Attempt] (ADR 0030). The last line per job and number is the
// attempt; a dropped line ends it with its job's row.
type attemptRecord struct {
	apiversion.TypeMeta
	Spec   attemptSpec         `json:"spec"`
	Status attemptStatusRecord `json:"status"`
}

// attemptSpec names the try: an attempt has no name of its own.
type attemptSpec struct {
	Job string `json:"job"`
	N   int    `json:"n"`
}

// attemptStatusRecord is [AttemptStatus] under camelCase keys.
// JobState is what the job ended as, never the try's lifecycle.
type attemptStatusRecord struct {
	Started            int64                `json:"started,omitzero"`
	Ended              int64                `json:"ended,omitzero"`
	End                string               `json:"end,omitempty"`
	Result             *resultRecord        `json:"result,omitzero"`
	JobState           string               `json:"jobState,omitempty"`
	ObservedGeneration int                  `json:"observedGeneration,omitzero"`
	EndReason          string               `json:"endReason,omitempty"`
	ModelTier          string               `json:"modelTier,omitzero"`
	Checkpoint         string               `json:"checkpoint,omitempty"`
	ReportedBase       string               `json:"reportedBase,omitempty"`
	ReworkRounds       int                  `json:"reworkRounds,omitzero"`
	Widenings          int                  `json:"widenings,omitzero"`
	Unmet              []verificationRecord `json:"unmet,omitempty"`
	Violations         []string             `json:"violations,omitempty"`
	FailingRefs        []string             `json:"failingRefs,omitempty"`
	Denials            []denialRecord       `json:"denials,omitempty"`
	DenialsKnown       bool                 `json:"denialsKnown,omitzero"`
	Retry              retryRecord          `json:"retry,omitzero"`
	Dropped            bool                 `json:"dropped,omitzero"`
}

type resultRecord struct {
	Job                  string                 `json:"job,omitempty"`
	ChangedPaths         []string               `json:"changedPaths"`
	Validation           resultValidationRecord `json:"validation,omitzero"`
	VerificationEvidence []evidenceRecord       `json:"verificationEvidence,omitempty"`
	Descendants          []string               `json:"descendants,omitempty"`
	UnresolvedRisks      []string               `json:"unresolvedRisks"`
	FollowUps            []string               `json:"followUps,omitempty"`
	ObservedGeneration   int                    `json:"observedGeneration,omitzero"`
}

type resultValidationRecord struct {
	Command   string `json:"command"`
	OutputRef string `json:"outputRef"`
	Owed      string `json:"owed,omitempty"`
	Unrun     string `json:"unrun,omitempty"`
}

type evidenceRecord struct {
	GateID    string `json:"gateId"`
	OutputRef string `json:"outputRef"`
	Owed      string `json:"owed,omitempty"`
	Unrun     string `json:"unrun,omitempty"`
}

type denialRecord struct {
	Rule       string `json:"rule"`
	Reason     string `json:"reason,omitempty"`
	Action     string `json:"action,omitempty"`
	VerdictRef string `json:"verdictRef,omitempty"`
	Ts         int64  `json:"ts"`
}

type retryRecord struct {
	Retryable bool   `json:"retryable"`
	Change    string `json:"change,omitempty"`
	Reason    string `json:"reason,omitempty"`
	Code      Code   `json:"code,omitempty"`
}

func wireAttemptEnd() wireEnum[AttemptEnd] {
	return newWireEnum(AttemptEndExit, AttemptEndHandback, AttemptEndStop, AttemptEndDeadline, AttemptEndJob)
}

func wireJobState() wireEnum[State] { return newWireEnum(States()...) }

func wireRetryChange() wireEnum[RetryChange] {
	return newWireEnum(RetryAfterConfig, RetryAfterWidening, RetryAfterPerson, RetryAfterUpgrade)
}

func (r attemptRecord) key() string { return fmt.Sprintf("%s\x00%d", r.Spec.Job, r.Spec.N) }

// recordAttempt is a as the line attempts.jsonl stores.
func recordAttempt(a Attempt) attemptRecord {
	s := a.Status
	st := attemptStatusRecord{
		Started: s.Started, Ended: s.Ended, End: wireAttemptEnd().spell(s.End), Result: recordResult(s.Result),
		JobState: wireJobState().spell(s.State), ObservedGeneration: s.ObservedGeneration, EndReason: s.EndReason,
		ModelTier: s.ModelTier, Checkpoint: s.Checkpoint, ReportedBase: s.ReportedBase,
		ReworkRounds: s.ReworkRounds, Widenings: s.Widenings, Unmet: recordVerifications(s.Unmet),
		Violations: slices.Clone(s.Violations), FailingRefs: slices.Clone(s.FailingRefs), DenialsKnown: s.DenialsKnown,
		Retry: retryRecord{Retryable: s.Retry.Retryable, Change: wireRetryChange().spell(s.Retry.Change), Reason: s.Retry.Reason, Code: s.Retry.Code},
	}
	for _, d := range s.Denials {
		st.Denials = append(st.Denials, denialRecord(d))
	}
	return attemptRecord{TypeMeta: attemptType(), Spec: attemptSpec{Job: a.Job, N: a.N}, Status: st}
}

// attempt is r as the domain type, refusing a stored spelling no enum lists.
func (r attemptRecord) attempt() (Attempt, error) {
	st := r.Status
	var bad wireReads
	s := AttemptStatus{
		Started: st.Started, Ended: st.Ended, End: wireAttemptEnd().read(st.End, &bad), Result: st.Result.result(),
		State: wireJobState().read(st.JobState, &bad), ObservedGeneration: st.ObservedGeneration, EndReason: st.EndReason,
		ModelTier: st.ModelTier, Checkpoint: st.Checkpoint, ReportedBase: st.ReportedBase,
		ReworkRounds: st.ReworkRounds, Widenings: st.Widenings, Unmet: verifications(st.Unmet),
		Violations: st.Violations, FailingRefs: st.FailingRefs, DenialsKnown: st.DenialsKnown,
		Retry: RetryVerdict{Retryable: st.Retry.Retryable, Change: wireRetryChange().read(st.Retry.Change, &bad), Reason: st.Retry.Reason, Code: st.Retry.Code},
	}
	if err := bad.refusal(); err != nil {
		return Attempt{}, fmt.Errorf("attempt %d of job %s: %w", r.Spec.N, r.Spec.Job, err)
	}
	for _, d := range st.Denials {
		s.Denials = append(s.Denials, Denial(d))
	}
	return Attempt{Job: r.Spec.Job, N: r.Spec.N, Status: s}.Clone(), nil
}

func recordResult(r *Result) *resultRecord {
	if r == nil {
		return nil
	}
	out := &resultRecord{
		Job: r.Job, ChangedPaths: slices.Clone(r.ChangedPaths), Validation: resultValidationRecord(r.Validation),
		Descendants: slices.Clone(r.Descendants), UnresolvedRisks: slices.Clone(r.UnresolvedRisks),
		FollowUps: slices.Clone(r.FollowUps), ObservedGeneration: r.ObservedGeneration,
	}
	for _, e := range r.VerificationEvidence {
		out.VerificationEvidence = append(out.VerificationEvidence, evidenceRecord(e))
	}
	return out
}

// result is r as the result a holder filed. Its version was the input's,
// checked when it was filed, and the record's own stands for it here.
func (r *resultRecord) result() *Result {
	if r == nil {
		return nil
	}
	out := &Result{
		Job: r.Job, ChangedPaths: r.ChangedPaths, Validation: ResultValidation(r.Validation),
		Descendants: r.Descendants, UnresolvedRisks: r.UnresolvedRisks, FollowUps: r.FollowUps,
		ObservedGeneration: r.ObservedGeneration,
	}
	for _, e := range r.VerificationEvidence {
		out.VerificationEvidence = append(out.VerificationEvidence, VerificationEvidence(e))
	}
	return out
}

// UnknownMemberError is a stored record, below the top of a row or an attempt line,
// holding a member this build does not declare. Only [Job.Unknown]'s three levels keep
// such a member for the next write, so a reader refuses it rather than drop it on the
// write after.
type UnknownMemberError struct {
	// Record is the Go name of the record type that holds the members.
	Record  string
	Members []string
}

func (e *UnknownMemberError) Error() string {
	return fmt.Sprintf("job: a stored %s holds %q, which this build does not declare: a newer writer stored it, so read the store with that writer's build", e.Record, e.Members)
}

// readNested decodes data into p, refusing a member p's type does not declare.
func readNested[P any](data []byte, p *P, record string) error {
	unknown, err := codec.UnmarshalWithUnknown(data, p)
	if err != nil {
		return err
	}
	if len(unknown) > 0 {
		return &UnknownMemberError{Record: record, Members: slices.Sorted(maps.Keys(unknown))}
	}
	return nil
}

func (r *agentRecord) UnmarshalJSON(data []byte) error {
	type plain agentRecord
	return readNested(data, (*plain)(r), "agentRecord")
}

func (r *workerRecord) UnmarshalJSON(data []byte) error {
	type plain workerRecord
	return readNested(data, (*plain)(r), "workerRecord")
}

func (r *leaseCheckRecord) UnmarshalJSON(data []byte) error {
	type plain leaseCheckRecord
	return readNested(data, (*plain)(r), "leaseCheckRecord")
}

func (r *goalRecord) UnmarshalJSON(data []byte) error {
	type plain goalRecord
	return readNested(data, (*plain)(r), "goalRecord")
}

func (r *modelTierRecord) UnmarshalJSON(data []byte) error {
	type plain modelTierRecord
	return readNested(data, (*plain)(r), "modelTierRecord")
}

func (r *originRecord) UnmarshalJSON(data []byte) error {
	type plain originRecord
	return readNested(data, (*plain)(r), "originRecord")
}

func (r *credentialRecord) UnmarshalJSON(data []byte) error {
	type plain credentialRecord
	return readNested(data, (*plain)(r), "credentialRecord")
}

func (r *claimRecord) UnmarshalJSON(data []byte) error {
	type plain claimRecord
	return readNested(data, (*plain)(r), "claimRecord")
}

func (r *claimEvidenceRecord) UnmarshalJSON(data []byte) error {
	type plain claimEvidenceRecord
	return readNested(data, (*plain)(r), "claimEvidenceRecord")
}

func (r *releaseRecord) UnmarshalJSON(data []byte) error {
	type plain releaseRecord
	return readNested(data, (*plain)(r), "releaseRecord")
}

func (r *entryRecord) UnmarshalJSON(data []byte) error {
	type plain entryRecord
	return readNested(data, (*plain)(r), "entryRecord")
}

func (r *verificationRecord) UnmarshalJSON(data []byte) error {
	type plain verificationRecord
	return readNested(data, (*plain)(r), "verificationRecord")
}

func (r *rejectionRecord) UnmarshalJSON(data []byte) error {
	type plain rejectionRecord
	return readNested(data, (*plain)(r), "rejectionRecord")
}

func (r *worktreeRecord) UnmarshalJSON(data []byte) error {
	type plain worktreeRecord
	return readNested(data, (*plain)(r), "worktreeRecord")
}

func (r *runAttemptRecord) UnmarshalJSON(data []byte) error {
	type plain runAttemptRecord
	return readNested(data, (*plain)(r), "runAttemptRecord")
}

func (r *gateAttemptRecord) UnmarshalJSON(data []byte) error {
	type plain gateAttemptRecord
	return readNested(data, (*plain)(r), "gateAttemptRecord")
}

func (r *runRecord) UnmarshalJSON(data []byte) error {
	type plain runRecord
	return readNested(data, (*plain)(r), "runRecord")
}

func (r *integrationRecord) UnmarshalJSON(data []byte) error {
	type plain integrationRecord
	return readNested(data, (*plain)(r), "integrationRecord")
}

func (r *attemptRecord) UnmarshalJSON(data []byte) error {
	type plain attemptRecord
	return readNested(data, (*plain)(r), "attemptRecord")
}

func (r *attemptSpec) UnmarshalJSON(data []byte) error {
	type plain attemptSpec
	return readNested(data, (*plain)(r), "attemptSpec")
}

func (r *attemptStatusRecord) UnmarshalJSON(data []byte) error {
	type plain attemptStatusRecord
	return readNested(data, (*plain)(r), "attemptStatusRecord")
}

func (r *resultRecord) UnmarshalJSON(data []byte) error {
	type plain resultRecord
	return readNested(data, (*plain)(r), "resultRecord")
}

func (r *resultValidationRecord) UnmarshalJSON(data []byte) error {
	type plain resultValidationRecord
	return readNested(data, (*plain)(r), "resultValidationRecord")
}

func (r *evidenceRecord) UnmarshalJSON(data []byte) error {
	type plain evidenceRecord
	return readNested(data, (*plain)(r), "evidenceRecord")
}

func (r *denialRecord) UnmarshalJSON(data []byte) error {
	type plain denialRecord
	return readNested(data, (*plain)(r), "denialRecord")
}

func (r *retryRecord) UnmarshalJSON(data []byte) error {
	type plain retryRecord
	return readNested(data, (*plain)(r), "retryRecord")
}
