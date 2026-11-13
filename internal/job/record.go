package job

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/libs/apiversion"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/types/agent"
)

// jobRecord is one row of jobs.jsonl, the persistence type of
// [types.Job] (ADR 0030). [recordJob] and [jobRecord.job] are the one
// conversion. Unknown, here and on spec and status, is what a newer
// magus wrote, carried back out unchanged.
type jobRecord struct {
	apiversion.TypeMeta
	Metadata apiversion.Metadata        `json:"metadata"`
	Spec     jobSpec                    `json:"spec"`
	Status   jobStatus                  `json:"status"`
	Unknown  map[string]json.RawMessage `json:",unknown"`
}

// jobSpec is what a declaration writes.
type jobSpec struct {
	Parent       string                     `json:"parent,omitempty"`
	Agent        *agentRecord               `json:"agent,omitempty"`
	Checkpoint   string                     `json:"checkpoint,omitempty"`
	Paths        types.JobPaths             `json:"paths,omitzero"`
	DependsOn   []string                   `json:"dependsOn,omitempty"`
	Integrates   []string                   `json:"integrates,omitempty"`
	Breaks       []string                   `json:"breaks,omitempty"`
	BriefFormat  string                     `json:"briefFormat,omitempty"`
	Delivery     string                     `json:"delivery,omitempty"`
	ParentHead   string                     `json:"parentHead,omitempty"`
	Follows      string                     `json:"follows,omitempty"`
	MaxAttempts  int                        `json:"maxAttempts,omitzero"`
	MaxClaims    int                        `json:"maxClaims,omitzero"`
	Deferred     []types.JobDeferred        `json:"deferred,omitempty"`
	LeftUnmerged string                     `json:"leftUnmerged,omitempty"`
	Weight       float64                    `json:"weight,omitzero"`
	Check        *leaseCheckRecord          `json:"check,omitempty"`
	Validation   string                     `json:"validation,omitempty"`
	Goals        []goalRecord               `json:"goals,omitempty"`
	ReadOnly     bool                       `json:"readOnly,omitzero"`
	Deviations   []types.Deviation          `json:"deviations,omitempty"`
	Unknown      map[string]json.RawMessage `json:",unknown"`
}

// jobStatus is what the store computes. Its conditions are the row's
// lifecycle; see [lifecycleConditions].
type jobStatus struct {
	Conditions      []apiversion.Condition       `json:"conditions,omitempty"`
	ModelTier       *modelTierRecord             `json:"modelTier,omitempty"`
	ReworkRounds    int                          `json:"reworkRounds,omitzero"`
	Widenings       int                          `json:"widenings,omitzero"`
	Claims          []claimRecord                `json:"claims,omitempty"`
	Landed          bool                         `json:"landed,omitzero"`
	Paused          int64                        `json:"paused,omitzero"`
	PausedBy        originRecord                 `json:"pausedBy,omitzero"`
	PauseReason     string                       `json:"pauseReason,omitempty"`
	PausedUntil     int64                        `json:"pausedUntil,omitzero"`
	Rejection       *rejectionRecord             `json:"rejection,omitempty"`
	Releases        []releaseRecord              `json:"releases,omitempty"`
	Unattributed    []types.JobUnattributedWrite `json:"unattributed,omitempty"`
	Entries         []entryRecord                `json:"entries,omitempty"`
	ReleaseRequests []types.JobReleaseRequest    `json:"releaseRequests,omitempty"`
	WriteProof      string                       `json:"writeProof,omitempty"`
	Validated       int64                        `json:"validated,omitzero"`
	ReportedBase    string                       `json:"reportedBase,omitempty"`
	BaseVerdict     string                       `json:"baseVerdict,omitempty"`
	RegisteredBy    originRecord                 `json:"registeredBy,omitzero"`
	Authority       string                       `json:"authority,omitempty"`
	Registered      int64                        `json:"registered,omitzero"`
	HolderStopped   int64                        `json:"holderStopped,omitzero"`
	CheckoutRoot    string                       `json:"checkoutRoot,omitempty"`
	Worktree        *worktreeRecord              `json:"worktree,omitempty"`
	EndReason       string                       `json:"endReason,omitempty"`
	Created         int64                        `json:"created"`
	Updated         int64                        `json:"updated"`
	Deadline        int64                        `json:"deadline,omitzero"`
	Attempt         *runAttemptRecord            `json:"attempt,omitempty"`
	GateAttempts    []gateAttemptRecord          `json:"gateAttempts,omitempty"`
	LastRun         *runRecord                   `json:"lastRun,omitempty"`
	Integration     *integrationRecord           `json:"integration,omitempty"`
	WaiterEvidence  *integrationRecord           `json:"waiterEvidence,omitempty"`
	Unknown         map[string]json.RawMessage   `json:",unknown"`
}

// recordJob is row as the line jobs.jsonl stores. stored is the row's
// record as read, nil for a new row: what it carries that row cannot say
// is kept.
func recordJob(row types.Job, stored *jobRecord) jobRecord {
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
	return jobRecord{TypeMeta: jobType, Metadata: meta, Spec: recordSpec(row, prev.Spec.Unknown),
		Status: recordStatus(row, prev.Status), Unknown: prev.Unknown}
}

// recordSpec is the declared half of row's record.
func recordSpec(row types.Job, unknown map[string]json.RawMessage) jobSpec {
	spec := jobSpec{
		Parent: row.Parent, Agent: recordAgent(row.Agent), Checkpoint: row.Checkpoint,
		Paths:     row.Paths,
		DependsOn: row.DependsOn, Integrates: row.Integrates, Breaks: row.Breaks,
		BriefFormat: wireBriefFormat.spell(row.BriefFormat), Delivery: row.Delivery, ParentHead: row.ParentHead,
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
func recordStatus(row types.Job, prev jobStatus) jobStatus {
	st := jobStatus{
		Conditions:   lifecycleConditions(row, prev.Conditions),
		ModelTier:    recordModelTier(row.ModelTier),
		ReworkRounds: row.ReworkRounds, Widenings: row.Widenings, Landed: row.Landed,
		Paused: row.Paused, PausedBy: recordOrigin(row.PausedBy), PauseReason: row.PauseReason, PausedUntil: row.PausedUntil,
		Rejection:    recordRejection(row.Rejection),
		Unattributed: row.Unattributed, ReleaseRequests: row.ReleaseRequests,
		WriteProof: wireWriteProof.spell(row.WriteProof), Validated: row.Validated,
		ReportedBase: row.ReportedBase, BaseVerdict: wireBaseVerdict.spell(row.BaseVerdict),
		RegisteredBy: recordOrigin(row.RegisteredBy), Authority: row.Authority, Registered: row.Registered,
		HolderStopped: row.HolderStopped, CheckoutRoot: row.CheckoutRoot, EndReason: row.EndReason,
		Created: row.Created, Updated: row.Updated, Deadline: row.Deadline,
		Integration: recordIntegration(row.Integration), WaiterEvidence: recordIntegration(row.WaiterEvidence),
		Unknown: prev.Unknown,
	}
	for _, c := range row.Claims {
		st.Claims = append(st.Claims, claimRecord{
			Path: c.Path, Evidence: claimEvidenceRecord{Kind: wireClaimKind.spell(c.Evidence.Kind), Subject: c.Evidence.Subject},
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
func recordRuns(st *jobStatus, row types.Job) {
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
// version.
func (r jobRecord) job() (types.Job, error) {
	if r.TypeMeta != jobType {
		return types.Job{}, &apiversion.RefusedError{Want: jobType, Got: r.TypeMeta}
	}
	state, err := lifecycleState(r.Status.Conditions)
	if err != nil {
		return types.Job{}, fmt.Errorf("job %s: %w", r.Metadata.Name, err)
	}
	var bad wireReads
	row := r.Spec.job(&bad)
	r.Status.populate(&row, &bad)
	if err := bad.refusal(); err != nil {
		return types.Job{}, fmt.Errorf("job %s: %w", r.Metadata.Name, err)
	}
	row.ID, row.Generation, row.State = r.Metadata.Name, int(r.Metadata.Generation), state
	for _, f := range r.Metadata.Requires(recordGroup) {
		row.Requires = append(row.Requires, string(f))
	}
	row.Unknown = unknownMembers(r.Unknown, r.Spec.Unknown, r.Status.Unknown)
	return row, nil
}

// job is s as the declared members of a domain row.
func (s jobSpec) job(bad *wireReads) types.Job {
	row := types.Job{
		Parent: s.Parent, Agent: s.Agent.spec(bad), Checkpoint: s.Checkpoint,
		Paths:     s.Paths,
		DependsOn: s.DependsOn, Integrates: s.Integrates, Breaks: s.Breaks,
		BriefFormat: wireBriefFormat.read(s.BriefFormat, bad), Delivery: s.Delivery, ParentHead: s.ParentHead,
		Follows: s.Follows, MaxAttempts: s.MaxAttempts, MaxClaims: s.MaxClaims,
		Deferred: s.Deferred, LeftUnmerged: s.LeftUnmerged, Weight: s.Weight,
		Validation: s.Validation, ReadOnly: s.ReadOnly, Deviations: s.Deviations,
	}
	if s.Check != nil {
		c := types.LeaseCheck(*s.Check)
		row.Check = &c
	}
	for _, g := range s.Goals {
		row.Goals = append(row.Goals, g.goal(bad))
	}
	return row
}

// populate sets the computed members of row from st.
func (st jobStatus) populate(row *types.Job, bad *wireReads) {
	row.ModelTier = st.ModelTier.tier(bad)
	row.ReworkRounds, row.Widenings, row.Landed = st.ReworkRounds, st.Widenings, st.Landed
	row.Paused, row.PausedBy, row.PauseReason, row.PausedUntil = st.Paused, st.PausedBy.origin(bad), st.PauseReason, st.PausedUntil
	row.Rejection = st.Rejection.rejection()
	row.Unattributed, row.ReleaseRequests = st.Unattributed, st.ReleaseRequests
	row.WriteProof, row.Validated = wireWriteProof.read(st.WriteProof, bad), st.Validated
	row.ReportedBase, row.BaseVerdict = st.ReportedBase, wireBaseVerdict.read(st.BaseVerdict, bad)
	row.RegisteredBy, row.Authority, row.Registered = st.RegisteredBy.origin(bad), st.Authority, st.Registered
	row.HolderStopped, row.CheckoutRoot, row.EndReason = st.HolderStopped, st.CheckoutRoot, st.EndReason
	row.Created, row.Updated, row.Deadline = st.Created, st.Updated, st.Deadline
	row.Integration, row.WaiterEvidence = st.Integration.integration(), st.WaiterEvidence.integration()
	for _, c := range st.Claims {
		row.Claims = append(row.Claims, types.JobClaim{
			Path: c.Path, Evidence: types.ClaimEvidence{Kind: wireClaimKind.read(c.Evidence.Kind, bad), Subject: c.Evidence.Subject},
			Proof: c.Proof, At: c.At, By: c.By.origin(bad),
		})
	}
	for _, rel := range st.Releases {
		row.Releases = append(row.Releases, types.JobRelease(rel))
	}
	for _, e := range st.Entries {
		row.Entries = append(row.Entries, types.JobEntry{Path: e.Path, By: e.By.origin(bad), At: e.At, Consumed: e.Consumed})
	}
	if st.Worktree != nil {
		w := types.JobWorktree(*st.Worktree)
		row.Worktree = &w
	}
	if st.Attempt != nil {
		a := types.JobAttempt(*st.Attempt)
		row.Attempt = &a
	}
	for _, g := range st.GateAttempts {
		row.GateAttempts = append(row.GateAttempts, types.JobGateAttempt{GateID: g.GateID, Attempt: types.JobAttempt(g.Attempt)})
	}
	if st.LastRun != nil {
		run := types.JobRun(*st.LastRun)
		row.LastRun = &run
	}
}

// unknownMembers names the members of top, spec and status, as
// [types.Job.Unknown] spells them.
func unknownMembers(top, spec, status map[string]json.RawMessage) []string {
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
	state        types.JobState
	kind, reason string
}

// lifecycle is every state's condition. Failed takes two reasons, as a
// Kubernetes Job's Failed does.
var lifecycle = []lifecycleCondition{
	{types.StateDeclared, "Declared", "Declared"},
	{types.StateRunning, "Running", "Running"},
	{types.StateExited, "Exited", "Exited"},
	{types.StatePass, "Complete", "Passed"},
	{types.StateFail, "Failed", "CriteriaUnmet"},
	{types.StateNoReturn, "Failed", "NoReturn"},
}

// lifecycleConditions are row's conditions: its state's, then every
// condition of stored no state names, kept as stored. A state's condition
// keeps its stored transition time; a new one transitioned at Updated.
func lifecycleConditions(row types.Job, stored []apiversion.Condition) []apiversion.Condition {
	var out []apiversion.Condition
	for _, l := range lifecycle {
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
		if !slices.ContainsFunc(lifecycle, func(l lifecycleCondition) bool { return l.kind == s.Type }) {
			out = append(out, s)
		}
	}
	return out
}

// lifecycleState is the state conditions hold True, "" for none. Two
// is a record no magus writes.
func lifecycleState(conditions []apiversion.Condition) (types.JobState, error) {
	var state types.JobState
	for _, c := range conditions {
		if c.Status != apiversion.ConditionTrue {
			continue
		}
		for _, l := range lifecycle {
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
// list goes on bad: passed on, it reached a Buzz enum conversion that panics.
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

var (
	wireWriteProof  = newWireEnum(types.WriteProofAlone, types.WriteProofDisjoint, types.WriteProofOverlapping)
	wireBaseVerdict = newWireEnum(types.BaseMatch, types.BaseRevisionMatch, types.BaseDiverged, types.BaseUnknown)
	wireBriefFormat = newWireEnum(types.BriefFormatMarkdown, types.BriefFormatBuzz)
	wireGoalKind    = newWireEnum(types.GoalKinds()...)
	wireGoalExpect  = newWireEnum(types.GoalExpects()...)
	wireClaimKind   = newWireEnum(types.ClaimRefs, types.ClaimTest, types.ClaimOutput, types.ClaimReleased, types.ClaimDeadHolder)
	wireEntryPoint  = newWireEnum(types.EntryPointCLI, types.EntryPointHook, types.EntryPointMCP, types.EntryPointRPC, types.EntryPointServer)
	wireCredential  = newWireEnum(types.KindOperator, types.KindStored, types.KindShare, types.KindExchange, types.KindStdio, types.KindSocketPeer)
	wireWorkerType  = newWireEnum(agent.WorkerTypeResumed, agent.WorkerTypeNew)
	wireTierSource  = newWireEnum(agent.ModelTierSourceJob, agent.ModelTierSourceMatrix, agent.ModelTierSourceUserMatrix, agent.ModelTierSourceDefault)
	wireSkills      = newWireEnum(agent.SkillVariantShort, agent.SkillVariantFull)
)

// The records below mirror domain types under camelCase keys. A domain
// type whose keys are all one word is stored as it is.

type agentRecord struct {
	Model        agent.Model  `json:"model,omitzero"`
	Instructions string       `json:"instructions,omitempty"`
	Worker       workerRecord `json:"worker"`
}

type workerRecord struct {
	Type   string `json:"type"`
	Reason string `json:"reason,omitempty"`
}

func recordAgent(s *agent.Spec) *agentRecord {
	if s == nil {
		return nil
	}
	return &agentRecord{Model: s.Model, Instructions: s.Instructions,
		Worker: workerRecord{Type: wireWorkerType.spell(s.Worker.Type), Reason: s.Worker.Reason}}
}

func (r *agentRecord) spec(bad *wireReads) *agent.Spec {
	if r == nil {
		return nil
	}
	return &agent.Spec{Model: r.Model, Instructions: r.Instructions,
		Worker: agent.Worker{Type: wireWorkerType.read(r.Worker.Type, bad), Reason: r.Worker.Reason}}
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
}

func recordGoal(g types.Goal) goalRecord {
	return goalRecord{ID: g.ID, Description: g.Description, DependsOn: g.DependsOn,
		Kind: wireGoalKind.spell(g.Kind), Expect: wireGoalExpect.spell(g.Expect), Check: leaseCheckRecord(g.Check),
		Paths: g.Paths, Symbols: g.Symbols, Signature: g.Signature}
}

func (r goalRecord) goal(bad *wireReads) types.Goal {
	return types.Goal{ID: r.ID, Description: r.Description, DependsOn: r.DependsOn,
		Kind: wireGoalKind.read(r.Kind, bad), Expect: wireGoalExpect.read(r.Expect, bad), Check: types.LeaseCheck(r.Check),
		Paths: r.Paths, Symbols: r.Symbols, Signature: r.Signature}
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

func recordModelTier(t *agent.JobModelTier) *modelTierRecord {
	if t == nil {
		return nil
	}
	return &modelTierRecord{Name: t.Name, Rank: t.Rank, Skills: wireSkills.spell(t.Skills), Source: wireTierSource.spell(t.Source),
		Reason: t.Reason, Site: t.Site, Model: t.Model, Shared: t.Shared}
}

func (r *modelTierRecord) tier(bad *wireReads) *agent.JobModelTier {
	if r == nil {
		return nil
	}
	return &agent.JobModelTier{Name: r.Name, Rank: r.Rank, Skills: wireSkills.read(r.Skills, bad), Source: wireTierSource.read(r.Source, bad),
		Reason: r.Reason, Site: r.Site, Model: r.Model, Shared: r.Shared}
}

type originRecord struct {
	User       string           `json:"user,omitempty"`
	UID        string           `json:"uid,omitempty"`
	EntryPoint string           `json:"entryPoint,omitempty"`
	Host       string           `json:"host,omitempty"`
	Session    string           `json:"session,omitempty"`
	Agent      string           `json:"agent,omitempty"`
	Credential credentialRecord `json:"credential,omitzero"`
}

type credentialRecord struct {
	Kind  string      `json:"class,omitempty"`
	ID    string      `json:"id,omitempty"`
	Name  string      `json:"name,omitempty"`
	Grant types.Grant `json:"grant,omitzero"`
}

func recordOrigin(o types.Origin) originRecord {
	return originRecord{User: o.User, UID: o.UID, EntryPoint: wireEntryPoint.spell(o.EntryPoint), Host: o.Host,
		Session: o.Session, Agent: o.Agent, Credential: credentialRecord{
			Kind: wireCredential.spell(o.Credential.Kind), ID: o.Credential.ID, Name: o.Credential.Name, Grant: o.Credential.Grant,
		}}
}

func (r originRecord) origin(bad *wireReads) types.Origin {
	return types.Origin{User: r.User, UID: r.UID, EntryPoint: wireEntryPoint.read(r.EntryPoint, bad), Host: r.Host,
		Session: r.Session, Agent: r.Agent, Credential: types.Credential{
			Kind: wireCredential.read(r.Credential.Kind, bad), ID: r.Credential.ID, Name: r.Credential.Name, Grant: r.Credential.Grant,
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

func recordVerifications(in []types.VerificationStatus) []verificationRecord {
	if len(in) == 0 {
		return nil
	}
	out := make([]verificationRecord, 0, len(in))
	for _, v := range in {
		out = append(out, verificationRecord(v))
	}
	return out
}

func verifications(in []verificationRecord) []types.VerificationStatus {
	if len(in) == 0 {
		return nil
	}
	out := make([]types.VerificationStatus, 0, len(in))
	for _, v := range in {
		out = append(out, types.VerificationStatus(v))
	}
	return out
}

type rejectionRecord struct {
	Unmet              []verificationRecord `json:"unmet,omitempty"`
	Violations         []string             `json:"violations,omitempty"`
	At                 int64                `json:"at"`
	ObservedGeneration int                  `json:"observedGeneration,omitzero"`
}

func recordRejection(r *types.JobRejection) *rejectionRecord {
	if r == nil {
		return nil
	}
	return &rejectionRecord{Unmet: recordVerifications(r.Unmet), Violations: r.Violations, At: r.At, ObservedGeneration: r.ObservedGeneration}
}

func (r *rejectionRecord) rejection() *types.JobRejection {
	if r == nil {
		return nil
	}
	return &types.JobRejection{Unmet: verifications(r.Unmet), Violations: r.Violations, At: r.At, ObservedGeneration: r.ObservedGeneration}
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

func recordIntegration(i *types.JobIntegration) *integrationRecord {
	if i == nil {
		return nil
	}
	return &integrationRecord{Checkout: i.Checkout, At: i.At, Verified: i.Verified, Gates: recordVerifications(i.Gates), ObservedGeneration: i.ObservedGeneration}
}

func (r *integrationRecord) integration() *types.JobIntegration {
	if r == nil {
		return nil
	}
	return &types.JobIntegration{Checkout: r.Checkout, At: r.At, Verified: r.Verified, Gates: verifications(r.Gates), ObservedGeneration: r.ObservedGeneration}
}
