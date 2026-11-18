package job

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/libs/codec"
	"github.com/egladman/magus/libs/safetyscissors/tier"
)

type RecordSuite struct {
	suite.Suite
}

func TestRecordSuite(t *testing.T) { suite.Run(t, new(RecordSuite)) }

func (s *RecordSuite) record(source string) jobRecord {
	r := recordJob(Job{ID: "a", State: StateDeclared,
		ModelTier: &tier.Assignment{Name: "bounded", Source: tier.SourceJob}}, nil)
	r.Status.ModelTier.Source = source
	return r
}

func (s *RecordSuite) TestAnUnlistedStoredValueIsRefused() {
	_, err := s.record("row").job(newWireEnum[CredentialKind]())

	s.Require().Error(err)
	s.ErrorContains(err, `job a: "row" is not a stored Source`)
}

func (s *RecordSuite) TestAListedValueReadsAsItself() {
	got, err := s.record(string(tier.SourceMatrix)).job(newWireEnum[CredentialKind]())

	s.Require().NoError(err)
	s.Equal(tier.SourceMatrix, got.ModelTier.Source)
}

// The CamelCase an older build stored is no value of the set, so it is refused, never
// read as the value it once spelled.
func (s *RecordSuite) TestAnOlderBuildsCamelCaseValueIsRefused() {
	_, err := s.record("Matrix").job(newWireEnum[CredentialKind]())

	s.ErrorContains(err, `job a: "Matrix" is not a stored Source`)
}

func (s *RecordSuite) TestAnEmptyValueReadsEmpty() {
	got, err := s.record("").job(newWireEnum[CredentialKind]())

	s.Require().NoError(err)
	s.Empty(got.ModelTier.Source)
}

func (s *RecordSuite) TestACredentialKindTheStoreAdmitsRoundTrips() {
	kinds := newWireEnum[CredentialKind]("socket-peer")
	row := Job{ID: "a", State: StateDeclared, RegisteredBy: Origin{User: "eli", Credential: Credential{Kind: "socket-peer", ID: "c1"}}}

	got, err := recordJob(row, nil).job(kinds)

	s.Require().NoError(err)
	s.Equal(CredentialKind("socket-peer"), got.RegisteredBy.Credential.Kind)
}

func (s *RecordSuite) TestACredentialKindTheStoreDoesNotAdmitIsRefused() {
	row := Job{ID: "a", State: StateDeclared, RegisteredBy: Origin{Credential: Credential{Kind: "socket-peer"}}}

	_, err := recordJob(row, nil).job(newWireEnum[CredentialKind]())

	s.ErrorContains(err, `"socket-peer" is not a stored CredentialKind`)
}

func (s *RecordSuite) TestAMemberANewerWriterStoredGoesBackOut() {
	line := []byte(`{"api_version":"safetyscissors.job/v1alpha1","kind":"Job","metadata":{"name":"a"},"spec":{"later":1},"status":{"created":0,"updated":0},"extra":true}`)

	row, ok, err := ReadRecord(line)
	s.Require().NoError(err)
	s.Require().True(ok)
	s.Equal([]string{"extra", "spec.later"}, row.Unknown)

	var r jobRecord
	s.Require().NoError(codec.Unmarshal(line, &r))
	out, err := MarshalRecord(row, r.Unknown)
	s.Require().NoError(err)
	s.Contains(string(out), `"extra":true`)
}

func (s *RecordSuite) TestAGoalsBasesRoundTrip() {
	row := Job{ID: "a", State: StateDeclared, Goals: []Goal{{ID: "tests", Kind: GoalKindCheck, Expect: ExpectPassed,
		Check: checkJSON("test", "."), Accept: []Basis{BasisWitnessed, BasisAttested}}}}

	r := recordJob(row, nil)
	got, err := r.job(newWireEnum[CredentialKind]())

	s.Require().NoError(err)
	s.Equal([]string{"witnessed", "attested"}, r.Spec.Goals[0].Accept)
	s.Equal([]Basis{BasisWitnessed, BasisAttested}, got.Goals[0].Accept)
}

func (s *RecordSuite) TestAStoredBasisNoBuildSpellsIsRefused() {
	r := recordJob(Job{ID: "a", State: StateDeclared, Goals: []Goal{{ID: "tests", Kind: GoalKindCheck, Check: checkJSON("test", ".")}}}, nil)
	r.Spec.Goals[0].Accept = []string{"trusted"}

	_, err := r.job(newWireEnum[CredentialKind]())

	s.ErrorContains(err, `job a: "trusted" is not a stored Basis`)
}

func (s *RecordSuite) nestedRefusal(err error) *UnknownMemberError {
	refused, ok := errors.AsType[*UnknownMemberError](err)
	s.Require().True(ok, err)
	return refused
}

func (s *RecordSuite) TestAMemberANewerWriterStoredInAGoalIsRefused() {
	line := []byte(`{"api_version":"safetyscissors.job/v1alpha1","kind":"Job","metadata":{"name":"a"},` +
		`"spec":{"goals":[{"id":"tests","kind":"check","later":1}]},"status":{"created":0,"updated":0}}`)

	_, _, err := ReadRecord(line)

	refused := s.nestedRefusal(err)
	s.Equal("goalRecord", refused.Record)
	s.Equal([]string{"later"}, refused.Members)
}

func (s *RecordSuite) TestAMemberANewerWriterStoredTwoLevelsDownIsRefused() {
	line := []byte(`{"api_version":"safetyscissors.job/v1alpha1","kind":"Job","metadata":{"name":"a"},` +
		`"spec":{},"status":{"created":0,"updated":0,"registered_by":{"user":"eli","credential":{"id":"c1","later":true}}}}`)

	_, _, err := ReadRecord(line)

	s.Equal("credentialRecord", s.nestedRefusal(err).Record)
}

func (s *RecordSuite) TestAMemberANewerWriterStoredOnAnAttemptIsRefused() {
	line := []byte(`{"api_version":"safetyscissors.job/v1alpha1","kind":"Attempt","spec":{"job":"a","n":1},"status":{"started":1,"later":1}}`)

	var r attemptRecord
	err := codec.Unmarshal(line, &r)

	s.Equal("attemptStatusRecord", s.nestedRefusal(err).Record)
}

func (s *RecordSuite) TestEveryNestedRecordRoundTrips() {
	row := Job{ID: "a", State: StateDeclared, Agent: &Agent{Instructions: "go", Worker: Worker{Type: WorkerTypeNew}},
		Goals:        []Goal{{ID: "tests", Kind: GoalKindCheck, Expect: ExpectPassed, Check: checkJSON("test", "."), Accept: []Basis{BasisWitnessed}}},
		RegisteredBy: Origin{User: "eli"}, Worktree: &Worktree{Root: "/w", Source: "/s", Base: "abc"},
		Rejection: &Rejection{Unmet: []VerificationStatus{{ID: "tests"}}, At: 1}}
	out, err := MarshalRecord(row, nil)
	s.Require().NoError(err)

	got, ok, err := ReadRecord(out)

	s.Require().NoError(err)
	s.Require().True(ok)
	s.Equal(row.Agent, got.Agent)
	s.Equal(row.Goals, got.Goals)
	s.Equal(row.Worktree, got.Worktree)
	s.Equal(row.Rejection, got.Rejection)
}

// A camelCase key an older build stored is no member of a nested record, so it is
// refused rather than read as a zero.
func (s *RecordSuite) TestAnOlderBuildsCamelCaseNestedKeyIsRefused() {
	line := []byte(`{"api_version":"safetyscissors.job/v1alpha1","kind":"Job","metadata":{"name":"a"},` +
		`"spec":{},"status":{"created":0,"updated":0,"registered_by":{"user":"eli","entryPoint":"cli"}}}`)

	_, _, err := ReadRecord(line)

	refused := s.nestedRefusal(err)
	s.Equal("originRecord", refused.Record)
	s.Equal([]string{"entryPoint"}, refused.Members)
}

// The byte-for-byte line jobs.jsonl stores: snake_case keys, Go enum values, and the check
// as the bytes the caller's CheckValidate kept.
func (s *RecordSuite) TestARowsLineIsPinned() {
	row := Job{ID: "a", State: StateRunning, Generation: 2, Created: 10, Updated: 20, Check: checkJSON("test", "."),
		DependsOn: []string{"b"}, BriefFormat: BriefFormatBuzz, ReadOnly: true, MaxAttempts: 3,
		Goals:        []Goal{{ID: "lint", Kind: GoalKindCheck, Expect: ExpectPassed, Check: checkJSON("lint", "p"), Accept: []Basis{BasisWitnessed}}},
		RegisteredBy: Origin{User: "eli", EntryPoint: EntryPointCLI, Credential: Credential{Kind: "socket-peer", ID: "c1"}},
		WriteProof:   WriteProofDisjoint, BaseVerdict: BaseRevisionMatch, ReportedBase: "abc",
		ModelTier: &tier.Assignment{Name: "bounded", Rank: 2, Skills: tier.SkillVariantShort, Source: tier.SourceUserMatrix}}

	out, err := MarshalRecord(row, nil)

	s.Require().NoError(err)
	s.Equal(`{"api_version":"safetyscissors.job/v1alpha1","kind":"Job","metadata":{"name":"a","generation":2},`+
		`"spec":{"depends_on":["b"],"brief_format":"buzz","max_attempts":3,"check":{"target":"test","project":"."},`+
		`"goals":[{"id":"lint","kind":"check","expect":"passed","check":{"target":"lint","project":"p"},"accept":["witnessed"]}],"read_only":true},`+
		`"status":{"conditions":[{"type":"Running","status":"True","observed_generation":2,"last_transition_time":"1970-01-01T00:00:20Z","reason":"Running","message":""}],`+
		`"model_tier":{"name":"bounded","rank":2,"skills":"short","source":"user-matrix"},"write_proof":"disjoint","reported_base":"abc","base_verdict":"revision-match",`+
		`"registered_by":{"user":"eli","entry_point":"cli","credential":{"class":"socket-peer","id":"c1"}},"created":10,"updated":20}}`, string(out))
}

func (s *RecordSuite) TestAnAttemptsLineIsPinned() {
	a := Attempt{Job: "a", N: 2, Status: AttemptStatus{Started: 1, Ended: 2, End: AttemptEndHandback, State: StateNoReturn,
		DenialsKnown: true, Retry: RetryVerdict{Retryable: true, Change: RetryAfterWidening}}}

	out, err := MarshalAttempt(a, false)

	s.Require().NoError(err)
	s.Equal(`{"api_version":"safetyscissors.job/v1alpha1","kind":"Attempt","spec":{"job":"a","n":2},`+
		`"status":{"started":1,"ended":2,"end":"handback","job_state":"no_return","denials_known":true,"retry":{"retryable":true,"change":"widening"}}}`, string(out))
}
