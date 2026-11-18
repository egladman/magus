package job

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/libs/safetyscissors/job"
	"github.com/egladman/magus/types"
)

// fixtureCheck is c as the job store keeps it, written out by hand so a fixture needs no error
// path: the fixtures name ASCII targets and paths, which strconv.Quote spells as JSON does.
func fixtureCheck(c types.LeaseCheck) job.Check {
	members := []string{`"target":` + strconv.Quote(c.Target)}
	if c.Script != "" {
		members = append(members, `"script":`+strconv.Quote(c.Script))
	}
	if c.Project != "" {
		members = append(members, `"project":`+strconv.Quote(c.Project))
	}
	if len(c.Args) > 0 {
		quoted := make([]string, len(c.Args))
		for i, a := range c.Args {
			quoted[i] = strconv.Quote(a)
		}
		members = append(members, fmt.Sprintf(`"args":[%s]`, strings.Join(quoted, ",")))
	}
	if c.NoDefaultCharms {
		members = append(members, `"no_default_charms":true`)
	}
	if c.Test {
		members = append(members, `"test":true`)
	}
	return storedJSON(fmt.Sprintf("{%s}", strings.Join(members, ",")))
}

// storedJSON is the check json spells, as a row stores it. Every json here is a literal
// the test spells, so it reads, and a [job.Check] reads any JSON value: what a check says
// is the spec's to refuse.
func storedJSON(json string) job.Check {
	var c job.Check
	_ = c.UnmarshalJSON([]byte(json))
	return c
}

type LeaseCheckSuite struct{ suite.Suite }

func TestLeaseCheck(t *testing.T) { suite.Run(t, new(LeaseCheckSuite)) }

func (s *LeaseCheckSuite) TestParseReadsTargetProjectAndArgs() {
	c, err := parseLeaseCheck("go-test internal/job/ --no-default-charms -- -run X")
	s.Require().NoError(err)
	s.Equal(types.LeaseCheck{Target: "go-test", Project: "internal/job", Args: []string{"-run", "X"}, NoDefaultCharms: true}, c)
}

func (s *LeaseCheckSuite) TestParseRefusesAFlagBeforeTheArgs() {
	_, err := parseLeaseCheck("-o json test .")
	var refused *checkError
	s.Require().ErrorAs(err, &refused)
	s.Contains(refused.Reason, "carries the flag -o")
}

func (s *LeaseCheckSuite) TestValidateDropsAScriptsProjectAndRefusesTwoSubjects() {
	c, err := validateLeaseCheck(types.LeaseCheck{Script: "p.buzz", Project: ".", Test: true})
	s.Require().NoError(err)
	s.Equal(types.LeaseCheck{Script: "p.buzz", Test: true}, c)

	_, err = validateLeaseCheck(types.LeaseCheck{Script: "p.buzz", Target: "test"})
	var shape *checkShapeError
	s.Require().ErrorAs(err, &shape)
	s.Equal("script", shape.Field)

	_, err = validateLeaseCheck(types.LeaseCheck{Target: "test", Test: true})
	s.Require().ErrorAs(err, &shape)
	s.Equal("test", shape.Field)
}

func (s *LeaseCheckSuite) TestRawCheckStoresTheCleanedProject() {
	raw, err := rawCheck(types.LeaseCheck{Target: "lint", Project: "libs/x/", NoDefaultCharms: true})
	s.Require().NoError(err)
	s.JSONEq(`{"target":"lint","project":"libs/x","no_default_charms":true}`, raw.String())
}

// The spec's Validate returns a check it accepts again unchanged, which the store relies on
// to validate a row on every write.
func (s *LeaseCheckSuite) TestValidateIsAFixedPoint() {
	once, err := leaseChecks{}.Validate(storedJSON(`{"target":"test","project":"./a/"}`))
	s.Require().NoError(err)
	twice, err := leaseChecks{}.Validate(once)
	s.Require().NoError(err)
	s.Equal(once.String(), twice.String())
}

func (s *LeaseCheckSuite) TestValidateRefusesAnUnknownMember() {
	_, err := leaseChecks{}.Validate(storedJSON(`{"target":"test","noDefaultCharms":true}`))
	var decode *types.CheckDecodeError
	s.Require().ErrorAs(err, &decode)
}

func (s *LeaseCheckSuite) TestEqualReadsAnOmittedProjectAsTheRoot() {
	same, err := leaseChecks{}.Equal(fixtureCheck(types.LeaseCheck{Target: "test"}), fixtureCheck(types.LeaseCheck{Target: "test", Project: "."}))
	s.Require().NoError(err)
	s.True(same)
	same, err = leaseChecks{}.Equal(fixtureCheck(types.LeaseCheck{Target: "test"}), fixtureCheck(types.LeaseCheck{Target: "test", NoDefaultCharms: true}))
	s.Require().NoError(err)
	s.False(same)
}

func (s *LeaseCheckSuite) TestTheWorkspaceRootRendersAsAll() {
	line := CheckCommand(types.LeaseCheck{Target: "format", Project: "/"})
	s.True(strings.HasSuffix(line, " run format --all"), line)
	back, err := ParseCheckCommand(line)
	s.Require().NoError(err)
	s.Equal(types.LeaseCheck{Target: "format", Project: "/"}, back)
}

func (s *LeaseCheckSuite) TestRenderIsTheCommandLine() {
	line, err := leaseChecks{}.Render(fixtureCheck(types.LeaseCheck{Target: "test", Project: "libs/x", Args: []string{"-run", "A"}}))
	s.Require().NoError(err)
	s.True(strings.HasSuffix(line, " run test libs/x -- -run A"), line)
}

func (s *LeaseCheckSuite) TestGoalCheckRefusesAGoalWithNone() {
	_, err := GoalCheck(job.Goal{ID: "g", Kind: job.GoalKindCheck})
	s.Require().ErrorIs(err, types.ErrNoLeaseCheck)
}

func (s *LeaseCheckSuite) TestRowCheckSaysWhetherTheRowDeclaresOne() {
	_, ok, err := rowCheck(job.Job{ID: "w"})
	s.Require().NoError(err)
	s.False(ok)
	c, ok, err := rowCheck(job.Job{ID: "w", Check: fixtureCheck(types.LeaseCheck{Target: "test", Project: "."})})
	s.Require().NoError(err)
	s.True(ok)
	s.Equal("test", c.Target)
}

func (s *LeaseCheckSuite) TestDeclarationProblemsNameTheCheckThatDoesNotRead() {
	ps := declarationProblems(job.Declaration{ID: "w", Check: storedJSON(`{"target":"test","bogus":1}`)})
	s.Require().Len(ps, 1)
	s.Equal(types.JobProblem{Code: types.JobRecordInvalid, Field: "check", Message: ps[0].Message, Fix: ps[0].Fix}, ps[0])

	ps = declarationProblems(job.Declaration{ID: "w", Goals: []job.Goal{{ID: "g", Kind: job.GoalKindCheck,
		Check: fixtureCheck(types.LeaseCheck{Target: "test", Script: "p.buzz"}), Accept: []job.Basis{job.BasisWitnessed}}}})
	s.Require().Len(ps, 1)
	s.Equal(types.JobProblem{Code: types.JobGoalInvalid, Field: "goals[0].check.script", Message: ps[0].Message, Fix: ps[0].Fix}, ps[0])
}

// checkIn is stored read as a lease check, failing t when it does not read.
func checkIn(t testing.TB, stored job.Check) types.LeaseCheck {
	t.Helper()
	c, err := types.DecodeLeaseCheck(stored)
	require.NoError(t, err)
	return c
}
