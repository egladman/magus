package job

import (
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
		ModelTier: &tier.Assignment{Name: "bounded", Source: tier.SourceJob}}, nil, newWireEnum[CredentialKind]())
	r.Status.ModelTier.Source = source
	return r
}

func (s *RecordSuite) TestAnUnlistedStoredSpellingIsRefused() {
	_, err := s.record("Row").job(newWireEnum[CredentialKind]())

	s.Require().Error(err)
	s.ErrorContains(err, `job a: "Row" is not a stored ModelTierSource`)
}

func (s *RecordSuite) TestAListedSpellingReadsAsItsValue() {
	got, err := s.record(wireTierSource.spell(tier.SourceMatrix)).job(newWireEnum[CredentialKind]())

	s.Require().NoError(err)
	s.Equal(tier.SourceMatrix, got.ModelTier.Source)
}

func (s *RecordSuite) TestAnEmptySpellingReadsEmpty() {
	got, err := s.record("").job(newWireEnum[CredentialKind]())

	s.Require().NoError(err)
	s.Empty(got.ModelTier.Source)
}

func (s *RecordSuite) TestACredentialKindTheStoreAdmitsRoundTrips() {
	kinds := newWireEnum[CredentialKind]("socket-peer")
	row := Job{ID: "a", State: StateDeclared, RegisteredBy: Origin{User: "eli", Credential: Credential{Kind: "socket-peer", ID: "c1"}}}

	got, err := recordJob(row, nil, kinds).job(kinds)

	s.Require().NoError(err)
	s.Equal(CredentialKind("socket-peer"), got.RegisteredBy.Credential.Kind)
}

func (s *RecordSuite) TestACredentialKindTheStoreDoesNotAdmitIsRefused() {
	row := Job{ID: "a", State: StateDeclared, RegisteredBy: Origin{Credential: Credential{Kind: "socket-peer"}}}

	_, err := recordJob(row, nil, newWireEnum[CredentialKind]("socket-peer")).job(newWireEnum[CredentialKind]())

	s.ErrorContains(err, `"SocketPeer" is not a stored CredentialKind`)
}

func (s *RecordSuite) TestAMemberANewerWriterStoredGoesBackOut() {
	line := []byte(`{"apiVersion":"safetyscissors.job/v1alpha1","kind":"Job","metadata":{"name":"a"},"spec":{"later":1},"status":{"created":0,"updated":0},"extra":true}`)

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
		Check: LeaseCheck{Target: "test"}, Accept: []Basis{BasisWitnessed, BasisAttested}}}}

	r := recordJob(row, nil, newWireEnum[CredentialKind]())
	got, err := r.job(newWireEnum[CredentialKind]())

	s.Require().NoError(err)
	s.Equal([]string{"Witnessed", "Attested"}, r.Spec.Goals[0].Accept)
	s.Equal([]Basis{BasisWitnessed, BasisAttested}, got.Goals[0].Accept)
}

func (s *RecordSuite) TestAStoredBasisNoBuildSpellsIsRefused() {
	r := recordJob(Job{ID: "a", State: StateDeclared, Goals: []Goal{{ID: "tests", Kind: GoalKindCheck, Check: LeaseCheck{Target: "test"}}}},
		nil, newWireEnum[CredentialKind]())
	r.Spec.Goals[0].Accept = []string{"Trusted"}

	_, err := r.job(newWireEnum[CredentialKind]())

	s.ErrorContains(err, `job a: "Trusted" is not a stored Basis`)
}
