package job

//go:generate go -C .. run ./internal/cmd/jobschema

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/egladman/magus/libs/apiversion"
	"github.com/egladman/magus/libs/codec"
	"github.com/egladman/magus/libs/safetyscissors/job/internal/gen"
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
	Parent         string                      `json:"parent,omitempty"`
	Agent          *agentRecord                `json:"agent,omitzero"`
	Checkpoint     string                      `json:"checkpoint,omitempty"`
	Paths          Paths                       `json:"paths,omitzero"`
	DependsOn      []string                    `json:"depends_on,omitempty"`
	Integrates     []string                    `json:"integrates,omitempty"`
	Breaks         []string                    `json:"breaks,omitempty"`
	Creates        []string                    `json:"creates,omitempty"`
	BriefFormat    string                      `json:"brief_format,omitempty"`
	Delivery       string                      `json:"delivery,omitempty"`
	ParentHead     string                      `json:"parent_head,omitempty"`
	Follows        string                      `json:"follows,omitempty"`
	MaxAttempts    int                         `json:"max_attempts,omitzero"`
	MaxClaims      int                         `json:"max_claims,omitzero"`
	Deferred       []Deferred                  `json:"deferred,omitempty"`
	LeftUnmerged   string                      `json:"left_unmerged,omitempty"`
	Weight         float64                     `json:"weight,omitzero"`
	Check          Check                       `json:"check,omitzero"`
	Validation     string                      `json:"validation,omitempty"`
	Postconditions []postconditionRecord       `json:"postconditions,omitempty"`
	ReadOnly       bool                        `json:"read_only,omitzero"`
	Deviations     []Deviation                 `json:"deviations,omitempty"`
	Unknown        map[string]codec.RawMessage `json:"-" codec:"unknown"`
}

// jobStatus is what the store computes. Its conditions are the row's
// lifecycle; see [lifecycleConditions].
type jobStatus struct {
	Conditions            []apiversion.Condition       `json:"conditions,omitempty"`
	ModelTier             *modelTierRecord             `json:"model_tier,omitzero"`
	ReworkRounds          int                          `json:"rework_rounds,omitzero"`
	Widenings             int                          `json:"widenings,omitzero"`
	Claims                []claimRecord                `json:"claims,omitempty"`
	Landed                bool                         `json:"landed,omitzero"`
	Head                  string                       `json:"head,omitempty"`
	HeadUnread            string                       `json:"head_unread,omitempty"`
	Paused                int64                        `json:"paused,omitzero"`
	PausedBy              originRecord                 `json:"paused_by,omitzero"`
	PauseReason           string                       `json:"pause_reason,omitempty"`
	PausedUntil           int64                        `json:"paused_until,omitzero"`
	Rejection             *rejectionRecord             `json:"rejection,omitzero"`
	Releases              []releaseRecord              `json:"releases,omitempty"`
	Unattributed          []UnattributedWrite          `json:"unattributed,omitempty"`
	Entries               []entryRecord                `json:"entries,omitempty"`
	ReleaseRequests       []ReleaseRequest             `json:"release_requests,omitempty"`
	WriteProof            string                       `json:"write_proof,omitempty"`
	Validated             int64                        `json:"validated,omitzero"`
	ReportedBase          string                       `json:"reported_base,omitempty"`
	BaseVerdict           string                       `json:"base_verdict,omitempty"`
	RegisteredBy          originRecord                 `json:"registered_by,omitzero"`
	Authority             string                       `json:"authority,omitempty"`
	Registered            int64                        `json:"registered,omitzero"`
	HolderStopped         int64                        `json:"holder_stopped,omitzero"`
	CheckoutRoot          string                       `json:"checkout_root,omitempty"`
	ForkedIn              string                       `json:"forked_in,omitempty"`
	Worktree              *worktreeRecord              `json:"worktree,omitzero"`
	EndReason             string                       `json:"end_reason,omitempty"`
	Created               int64                        `json:"created"`
	Updated               int64                        `json:"updated"`
	Deadline              int64                        `json:"deadline,omitzero"`
	Attempt               *runAttemptRecord            `json:"attempt,omitzero"`
	PostconditionAttempts []postconditionAttemptRecord `json:"postcondition_attempts,omitempty"`
	LastRun               *runRecord                   `json:"last_run,omitzero"`
	Integration           *integrationRecord           `json:"integration,omitzero"`
	WaiterEvidence        *integrationRecord           `json:"waiter_evidence,omitzero"`
	Unknown               map[string]codec.RawMessage  `json:"-" codec:"unknown"`
}

// MarshalJSON writes the record with the members it kept in Unknown, so a member a
// newer writer stored goes back out under encoding/json and encoding/json/v2 alike.
func (r jobRecord) MarshalJSON() ([]byte, error) {
	type plain jobRecord
	return codec.MarshalWithUnknown(plain(r), r.Unknown)
}

// UnmarshalJSON reads the record, keeping the members it does not declare in Unknown.
func (r *jobRecord) UnmarshalJSON(data []byte) error {
	fields, err := jobgen.Job.Inspect(data)
	if err != nil {
		return err
	}
	return r.decode(fields)
}

func (r *jobRecord) decode(fields codec.InspectedObject) error {
	if err := rejectUnknown(fields.Rejected); err != nil {
		return err
	}
	type plain jobRecord
	var p plain
	if err := codec.Unmarshal(fields.Known, &p); err != nil {
		return err
	}
	*r = jobRecord(p)
	r.Unknown = fields.Extensions[""]
	r.Spec.Unknown = fields.Extensions["spec"]
	r.Status.Unknown = fields.Extensions["status"]
	return nil
}

// MarshalJSON writes the spec with the members it kept in Unknown.
func (s jobSpec) MarshalJSON() ([]byte, error) {
	type plain jobSpec
	return codec.MarshalWithUnknown(plain(s), s.Unknown)
}

// MarshalJSON writes the status with the members it kept in Unknown.
func (st jobStatus) MarshalJSON() ([]byte, error) {
	type plain jobStatus
	return codec.MarshalWithUnknown(plain(st), st.Unknown)
}

// recordJob is row as the line jobs.jsonl stores. stored is the row's
// record as read, nil for a new row: what it carries that row cannot say
// is kept.
func recordJob(row Job, stored *jobRecord) jobRecord {
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
		Status: recordStatus(row, prev.Status), Unknown: prev.Unknown}
}

// recordSpec is the declared half of row's record.
func recordSpec(row Job, unknown map[string]codec.RawMessage) jobSpec {
	spec := jobSpec{
		Parent: row.Parent, Agent: recordAgent(row.Agent), Checkpoint: row.Checkpoint,
		Paths:     row.Paths,
		DependsOn: row.DependsOn, Integrates: row.Integrates, Breaks: row.Breaks, Creates: row.Creates,
		BriefFormat: string(row.BriefFormat), Delivery: row.Delivery, ParentHead: row.ParentHead,
		Follows: row.Follows, MaxAttempts: row.MaxAttempts, MaxClaims: row.MaxClaims,
		Deferred: row.Deferred, LeftUnmerged: row.LeftUnmerged, Weight: row.Weight,
		Validation: row.Validation, ReadOnly: row.ReadOnly, Deviations: row.Deviations,
		Unknown: unknown,
	}
	spec.Check = row.Check
	for _, g := range row.Postconditions {
		spec.Postconditions = append(spec.Postconditions, recordPostcondition(g))
	}
	return spec
}

// recordStatus is the computed half of row's record. prev is the stored
// status, whose conditions and unknown members carry over.
func recordStatus(row Job, prev jobStatus) jobStatus {
	st := jobStatus{
		Conditions:   lifecycleConditions(row, prev.Conditions),
		ModelTier:    recordModelTier(row.ModelTier),
		ReworkRounds: row.ReworkRounds, Widenings: row.Widenings, Landed: row.Landed,
		Head: row.Head, HeadUnread: row.HeadUnread,
		Paused: row.Paused, PausedBy: recordOrigin(row.PausedBy), PauseReason: row.PauseReason, PausedUntil: row.PausedUntil,
		Rejection:    recordRejection(row.Rejection),
		Unattributed: row.Unattributed, ReleaseRequests: row.ReleaseRequests,
		WriteProof: string(row.WriteProof), Validated: row.Validated,
		ReportedBase: row.ReportedBase, BaseVerdict: string(row.BaseVerdict),
		RegisteredBy: recordOrigin(row.RegisteredBy), Authority: row.Authority, Registered: row.Registered,
		HolderStopped: row.HolderStopped, CheckoutRoot: row.CheckoutRoot, ForkedIn: row.ForkedIn, EndReason: row.EndReason,
		Created: row.Created, Updated: row.Updated, Deadline: row.Deadline,
		Integration: recordIntegration(row.Integration), WaiterEvidence: recordIntegration(row.WaiterEvidence),
		Unknown: prev.Unknown,
	}
	for _, c := range row.Claims {
		st.Claims = append(st.Claims, claimRecord{
			Path: c.Path, Evidence: claimEvidenceRecord{Kind: string(c.Evidence.Kind), Subject: c.Evidence.Subject},
			Proof: c.Proof, At: c.At, By: recordOrigin(c.By),
		})
	}
	for _, rel := range row.Releases {
		st.Releases = append(st.Releases, releaseRecord(rel))
	}
	for _, e := range row.Entries {
		st.Entries = append(st.Entries, entryRecord{Path: e.Path, By: recordOrigin(e.By), At: e.At, Consumed: e.Consumed})
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
	for _, g := range row.PostconditionAttempts {
		st.PostconditionAttempts = append(st.PostconditionAttempts, postconditionAttemptRecord{PostconditionID: g.PostconditionID, Attempt: runAttemptRecord(g.Attempt)})
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
	if err := refuseRetired(r.Metadata.Name, r.Spec.Unknown, r.Metadata.Requires(recordGroup)); err != nil {
		return Job{}, err
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
		DependsOn: s.DependsOn, Integrates: s.Integrates, Breaks: s.Breaks, Creates: s.Creates,
		BriefFormat: wireBriefFormat().read(s.BriefFormat, bad), Delivery: s.Delivery, ParentHead: s.ParentHead,
		Follows: s.Follows, MaxAttempts: s.MaxAttempts, MaxClaims: s.MaxClaims,
		Deferred: s.Deferred, LeftUnmerged: s.LeftUnmerged, Weight: s.Weight,
		Validation: s.Validation, ReadOnly: s.ReadOnly, Deviations: s.Deviations,
	}
	row.Check = s.Check
	for _, g := range s.Postconditions {
		row.Postconditions = append(row.Postconditions, g.postcondition(bad))
	}
	return row
}

// populate sets the computed members of row from st.
func (st jobStatus) populate(row *Job, bad *wireReads, creds wireEnum[CredentialKind]) {
	row.ModelTier = st.ModelTier.tier(bad)
	row.ReworkRounds, row.Widenings, row.Landed = st.ReworkRounds, st.Widenings, st.Landed
	row.Head, row.HeadUnread = st.Head, st.HeadUnread
	row.Paused, row.PausedBy, row.PauseReason, row.PausedUntil = st.Paused, st.PausedBy.origin(bad, creds), st.PauseReason, st.PausedUntil
	row.Rejection = st.Rejection.rejection()
	row.Unattributed, row.ReleaseRequests = st.Unattributed, st.ReleaseRequests
	row.WriteProof, row.Validated = wireWriteProof().read(st.WriteProof, bad), st.Validated
	row.ReportedBase, row.BaseVerdict = st.ReportedBase, wireBaseVerdict().read(st.BaseVerdict, bad)
	row.RegisteredBy, row.Authority, row.Registered = st.RegisteredBy.origin(bad, creds), st.Authority, st.Registered
	row.HolderStopped, row.CheckoutRoot, row.EndReason = st.HolderStopped, st.CheckoutRoot, st.EndReason
	row.ForkedIn = st.ForkedIn
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
	for _, g := range st.PostconditionAttempts {
		row.PostconditionAttempts = append(row.PostconditionAttempts, PostconditionAttempt{PostconditionID: g.PostconditionID, Attempt: CapturedRun(g.Attempt)})
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

// wireEnum is an enum's closed set as a record stores it: each value as its Go value. A
// writer stores any value unchanged, and a reader refuses one the set does not list.
type wireEnum[T ~string] struct {
	known map[string]T
}

// wireReads collects the stored values no wireEnum lists, so a record
// carrying one is refused with an error rather than read.
type wireReads struct{ errs []error }

func (r *wireReads) refusal() error { return errors.Join(r.errs...) }

func newWireEnum[T ~string](values ...T) wireEnum[T] {
	e := wireEnum[T]{known: make(map[string]T, len(values))}
	for _, v := range values {
		e.known[string(v)] = v
	}
	return e
}

// read is the value w stores, empty for an empty w. A value e does not
// list goes on bad: a reader switching on the value meets only values it knows.
func (e wireEnum[T]) read(w string, bad *wireReads) T {
	if v, ok := e.known[w]; ok || w == "" {
		return v
	}
	bad.errs = append(bad.errs, fmt.Errorf("%q is not a stored %s", w, reflect.TypeFor[T]().Name()))
	return ""
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
func wirePostconditionKind() wireEnum[PostconditionKind] { return newWireEnum(postconditionKinds()...) }
func wirePostconditionExpect() wireEnum[PostconditionExpect] {
	return newWireEnum(postconditionExpects()...)
}

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

// The records below mirror domain types under snake_case keys, and each refuses a member
// it does not declare (see [checkStoredMembers]), which a domain type does not.

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
		Worker: workerRecord{Type: string(s.Worker.Type), Reason: s.Worker.Reason}}
}

func (r *agentRecord) spec(bad *wireReads) *Agent {
	if r == nil {
		return nil
	}
	return &Agent{Model: r.Model, Instructions: r.Instructions,
		Worker: Worker{Type: wireWorkerType().read(r.Worker.Type, bad), Reason: r.Worker.Reason}}
}

type postconditionRecord struct {
	ID          string   `json:"id"`
	Description string   `json:"description,omitempty"`
	DependsOn   []string `json:"depends_on,omitempty"`
	Kind        string   `json:"kind"`
	Expect      string   `json:"expect,omitempty"`
	Check       Check    `json:"check,omitzero"`
	Paths       []string `json:"paths,omitempty"`
	Symbols     []string `json:"symbols,omitempty"`
	Signature   string   `json:"signature,omitempty"`
	// Accept is written by every build since [AcceptWitnessedMigration], which wrote it
	// onto each check postcondition stored before it, so a reader never defaults it.
	Accept  []string `json:"accept,omitempty"`
	Derived bool     `json:"derived,omitzero"`
}

func recordPostcondition(g Postcondition) postconditionRecord {
	r := postconditionRecord{ID: g.ID, Description: g.Description, DependsOn: g.DependsOn,
		Kind: string(g.Kind), Expect: string(g.Expect), Check: g.Check,
		Paths: g.Paths, Symbols: g.Symbols, Signature: g.Signature, Derived: g.Derived}
	for _, b := range g.Accept {
		r.Accept = append(r.Accept, string(b))
	}
	return r
}

func (r postconditionRecord) postcondition(bad *wireReads) Postcondition {
	g := Postcondition{ID: r.ID, Description: r.Description, DependsOn: r.DependsOn,
		Kind: wirePostconditionKind().read(r.Kind, bad), Expect: wirePostconditionExpect().read(r.Expect, bad), Check: r.Check,
		Paths: r.Paths, Symbols: r.Symbols, Signature: r.Signature, Derived: r.Derived}
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
	return &modelTierRecord{Name: t.Name, Rank: t.Rank, Skills: string(t.Skills), Source: string(t.Source),
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
	EntryPoint string           `json:"entry_point,omitempty"`
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

func recordOrigin(o Origin) originRecord {
	return originRecord{User: o.User, UID: o.UID, EntryPoint: string(o.EntryPoint), Host: o.Host,
		Session: o.Session, Agent: o.Agent, Credential: credentialRecord{
			Kind: string(o.Credential.Kind), ID: o.Credential.ID, Name: o.Credential.Name,
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
	ReleasedAt  int64  `json:"released_at"`
	Revoked     bool   `json:"revoked,omitzero"`
	TakenBy     string `json:"taken_by,omitempty"`
	DeadBecause string `json:"dead_because,omitempty"`
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
	OutputRef  string   `json:"output_ref,omitempty"`
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
	Submission         int                  `json:"submission,omitempty"`
	Unmet              []verificationRecord `json:"unmet,omitempty"`
	Violations         []string             `json:"violations,omitempty"`
	At                 int64                `json:"at"`
	ObservedGeneration int                  `json:"observed_generation,omitzero"`
}

func recordRejection(r *Rejection) *rejectionRecord {
	if r == nil {
		return nil
	}
	return &rejectionRecord{Submission: r.Submission, Unmet: recordVerifications(r.Unmet), Violations: r.Violations, At: r.At, ObservedGeneration: r.ObservedGeneration}
}

func (r *rejectionRecord) rejection() *Rejection {
	if r == nil {
		return nil
	}
	return &Rejection{Submission: r.Submission, Unmet: verifications(r.Unmet), Violations: r.Violations, At: r.At, ObservedGeneration: r.ObservedGeneration}
}

type worktreeRecord struct {
	Root         string `json:"root"`
	Source       string `json:"source"`
	Base         string `json:"base"`
	Created      int64  `json:"created"`
	Binary       string `json:"binary,omitempty"`
	BinaryDigest string `json:"binary_digest,omitempty"`
	Removed      int64  `json:"removed,omitzero"`
	Kept         string `json:"kept,omitempty"`
	Made         bool   `json:"made,omitzero"`
}

type runAttemptRecord struct {
	Found              bool     `json:"found"`
	Ref                string   `json:"ref,omitempty"`
	TimestampMs        int64    `json:"timestamp_ms,omitzero"`
	Project            string   `json:"project,omitempty"`
	Target             string   `json:"target,omitempty"`
	Spell              string   `json:"spell,omitempty"`
	Failed             bool     `json:"failed,omitzero"`
	AttemptID          string   `json:"attempt_id,omitempty"`
	DurationMs         int64    `json:"duration_ms,omitzero"`
	Revision           string   `json:"revision,omitempty"`
	Dirty              bool     `json:"dirty,omitzero"`
	Lease              string   `json:"lease,omitempty"`
	ExtraArgs          []string `json:"extra_args,omitempty"`
	ObservedGeneration int      `json:"observed_generation,omitzero"`
}

type postconditionAttemptRecord struct {
	PostconditionID string           `json:"postcondition_id"`
	Attempt         runAttemptRecord `json:"attempt"`
}

type runRecord struct {
	Invocation     string `json:"invocation,omitempty"`
	Ended          int64  `json:"ended,omitzero"`
	DurationMs     int64  `json:"duration_ms,omitzero"`
	OK             bool   `json:"ok"`
	Error          string `json:"error,omitempty"`
	ItemsRemoved   int64  `json:"items_removed,omitzero"`
	BytesReclaimed int64  `json:"bytes_reclaimed,omitzero"`
}

type integrationRecord struct {
	Checkout           string               `json:"checkout"`
	At                 int64                `json:"at"`
	Verified           bool                 `json:"verified"`
	Gates              []verificationRecord `json:"gates,omitempty"`
	ObservedGeneration int                  `json:"observed_generation,omitzero"`
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

// recordGroup prefixes the annotations a record carries.
const recordGroup = "safetyscissors.job"

func jobListType() apiversion.TypeMeta {
	return apiversion.TypeMeta{APIVersion: APIVersion, Kind: "JobList"}
}

func jobType() apiversion.TypeMeta {
	return apiversion.TypeMeta{APIVersion: APIVersion, Kind: "Job"}
}

func attemptType() apiversion.TypeMeta {
	return apiversion.TypeMeta{APIVersion: APIVersion, Kind: "Attempt"}
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

// attemptStatusRecord is [AttemptStatus] as attempts.jsonl stores it.
// JobState is what the job ended as, never the try's lifecycle.
type attemptStatusRecord struct {
	Rejections         []rejectionRecord    `json:"rejections,omitempty"`
	Started            int64                `json:"started,omitzero"`
	Ended              int64                `json:"ended,omitzero"`
	End                string               `json:"end,omitempty"`
	Result             *resultRecord        `json:"result,omitzero"`
	JobState           string               `json:"job_state,omitempty"`
	ObservedGeneration int                  `json:"observed_generation,omitzero"`
	EndReason          string               `json:"end_reason,omitempty"`
	ModelTier          string               `json:"model_tier,omitzero"`
	Checkpoint         string               `json:"checkpoint,omitempty"`
	ReportedBase       string               `json:"reported_base,omitempty"`
	ReworkRounds       int                  `json:"rework_rounds,omitzero"`
	Widenings          int                  `json:"widenings,omitzero"`
	Unmet              []verificationRecord `json:"unmet,omitempty"`
	Violations         []string             `json:"violations,omitempty"`
	FailingRefs        []string             `json:"failing_refs,omitempty"`
	Denials            []denialRecord       `json:"denials,omitempty"`
	DenialsKnown       bool                 `json:"denials_known,omitzero"`
	Retry              retryRecord          `json:"retry,omitzero"`
	Dropped            bool                 `json:"dropped,omitzero"`
}

type resultRecord struct {
	Job                  string                 `json:"job,omitempty"`
	ChangedPaths         []string               `json:"changed_paths"`
	Findings             []string               `json:"findings,omitempty"`
	Validation           resultValidationRecord `json:"validation,omitzero"`
	VerificationEvidence []evidenceRecord       `json:"verification_evidence,omitempty"`
	Descendants          []string               `json:"descendants,omitempty"`
	UnresolvedRisks      []string               `json:"unresolved_risks"`
	FollowUps            []string               `json:"follow_ups,omitempty"`
	ObservedGeneration   int                    `json:"observed_generation,omitzero"`
}

type resultValidationRecord struct {
	Command   string `json:"command"`
	OutputRef string `json:"output_ref"`
	Owed      string `json:"owed,omitempty"`
	Unrun     string `json:"unrun,omitempty"`
}

type evidenceRecord struct {
	GateID    string `json:"gate_id"`
	OutputRef string `json:"output_ref"`
	Owed      string `json:"owed,omitempty"`
	Unrun     string `json:"unrun,omitempty"`
}

type denialRecord struct {
	Rule       string `json:"rule"`
	Reason     string `json:"reason,omitempty"`
	Action     string `json:"action,omitempty"`
	VerdictRef string `json:"verdict_ref,omitempty"`
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

func wireJobState() wireEnum[State] { return newWireEnum(states()...) }

func wireRetryChange() wireEnum[RetryChange] {
	return newWireEnum(RetryAfterConfig, RetryAfterWidening, RetryAfterPerson, RetryAfterUpgrade)
}

func (r attemptRecord) key() string { return fmt.Sprintf("%s\x00%d", r.Spec.Job, r.Spec.N) }

// recordAttempt is a as the line attempts.jsonl stores.
func recordAttempt(a Attempt) attemptRecord {
	s := a.Status
	st := attemptStatusRecord{
		Started: s.Started, Ended: s.Ended, End: string(s.End), Result: recordResult(s.Result),
		JobState: string(s.State), ObservedGeneration: s.ObservedGeneration, EndReason: s.EndReason,
		ModelTier: s.ModelTier, Checkpoint: s.Checkpoint, ReportedBase: s.ReportedBase,
		ReworkRounds: s.ReworkRounds, Widenings: s.Widenings, Unmet: recordVerifications(s.Unmet),
		Violations: slices.Clone(s.Violations), FailingRefs: slices.Clone(s.FailingRefs), DenialsKnown: s.DenialsKnown,
		Retry: retryRecord{Retryable: s.Retry.Retryable, Change: string(s.Retry.Change), Reason: s.Retry.Reason, Code: s.Retry.Code},
	}
	for _, d := range s.Denials {
		st.Denials = append(st.Denials, denialRecord(d))
	}
	for _, c := range s.Rejections {
		st.Rejections = append(st.Rejections, *recordRejection(&c))
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
	for _, c := range st.Rejections {
		s.Rejections = append(s.Rejections, *c.rejection())
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
		Findings:    slices.Clone(r.Findings),
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
		Findings:    slices.Clone(r.Findings),
		Descendants: r.Descendants, UnresolvedRisks: r.UnresolvedRisks, FollowUps: r.FollowUps,
		ObservedGeneration: r.ObservedGeneration,
	}
	for _, e := range r.VerificationEvidence {
		out.VerificationEvidence = append(out.VerificationEvidence, VerificationEvidence(e))
	}
	return out
}

// UnknownMemberError reports the fields this build would lose on rewriting a stored line.
type UnknownMemberError struct {
	// Record is the Go type holding the members, or empty if several types are involved.
	Record  string
	Members []string
	// Paths names each rejected field from the root of the entry.
	Paths []string
}

func (e *UnknownMemberError) Error() string {
	return fmt.Sprintf("job: unknown stored fields %s (read the store with the writer's build)", strings.Join(e.Paths, ", "))
}

// Only the store owns the schemas below. Embedded domain types keep their
// own decoding rules, and the job envelope carries unknown fields forward.
func rejectUnknown(fields []codec.UnknownField) error {
	if len(fields) == 0 {
		return nil
	}
	refusal := &UnknownMemberError{Record: fields[0].Object}
	for _, f := range fields {
		if refusal.Record != f.Object {
			refusal.Record = ""
		}
		refusal.Members = append(refusal.Members, f.Name)
		refusal.Paths = append(refusal.Paths, f.Path)
	}
	slices.Sort(refusal.Members)
	slices.Sort(refusal.Paths)
	return refusal
}

func unmarshalWithSchema(data []byte, dst any, schema *codec.ObjectSchema) error {
	fields, err := schema.Inspect(data)
	if err != nil {
		return err
	}
	if err := rejectUnknown(fields.Rejected); err != nil {
		return err
	}
	return codec.Unmarshal(fields.Known, dst)
}

func (r *attemptRecord) UnmarshalJSON(data []byte) error {
	type plain attemptRecord
	return unmarshalWithSchema(data, (*plain)(r), &jobgen.Attempt)
}
