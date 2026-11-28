package wake

import (
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/libs/apiversion"
	"github.com/egladman/magus/libs/codec"
	"github.com/egladman/magus/libs/safetyscissors/job"
)

type EntrySuite struct{ suite.Suite }

func TestEntry(t *testing.T) { suite.Run(t, new(EntrySuite)) }

func (s *EntrySuite) TestTermsCarryTheirVersionAndKindAndRoundTrip() {
	in := terms("prs")
	in.At, in.Subject, in.MaxUSD = t0, "open pull requests", 4
	in.By = job.Origin{User: "eli"}
	b, err := codec.Marshal(in)
	s.Require().NoError(err)
	s.Contains(string(b), `"api_version":"safetyscissors.wake/v1alpha1"`)
	s.Contains(string(b), `"kind":"Terms"`)
	var out Terms
	s.Require().NoError(codec.Unmarshal(b, &out))
	s.Equal(in, out)
}

func (s *EntrySuite) TestAReportRoundTrips() {
	in := Report{
		Wake: "prs", N: 2, Started: t0, Ended: t0 + 9, Acted: 1, Pending: 2,
		Held:  []Held{{Item: "#7", Why: "draft", Command: "gh pr ready 7"}},
		Model: "m", Calls: []Usage{{Input: 1, CacheWrite5m: 2, CacheWrite1h: 3, CacheRead: 4, Output: 5}},
		USD: 0.25, Evidence: []string{"out-1"},
	}
	b, err := codec.Marshal(in)
	s.Require().NoError(err)
	var out Report
	s.Require().NoError(codec.Unmarshal(b, &out))
	s.Equal(in, out)
}

func (s *EntrySuite) TestAStartAndAStopRoundTrip() {
	start := Start{Wake: "prs", N: 1, At: t0}
	b, err := codec.Marshal(start)
	s.Require().NoError(err)
	var gotStart Start
	s.Require().NoError(codec.Unmarshal(b, &gotStart))
	s.Equal(start, gotStart)

	stop := Stop{Wake: "prs", At: t0, By: job.Origin{User: "eli"}, Why: "done by hand"}
	b, err = codec.Marshal(stop)
	s.Require().NoError(err)
	var gotStop Stop
	s.Require().NoError(codec.Unmarshal(b, &gotStop))
	s.Equal(stop, gotStop)
}

func (s *EntrySuite) TestAReaderRefusesAnotherVersion() {
	var t Terms
	err := codec.Unmarshal([]byte(`{"api_version":"safetyscissors.wake/v1beta1","kind":"Terms","id":"prs"}`), &t)
	var refused *apiversion.RefusedError
	s.Require().ErrorAs(err, &refused)
	s.Equal(APIVersion, refused.Want.APIVersion)
}

func (s *EntrySuite) TestAReaderRefusesAnotherKind() {
	b, err := codec.Marshal(Start{Wake: "prs", N: 1})
	s.Require().NoError(err)
	var r Report
	err = codec.Unmarshal(b, &r)
	var refused *apiversion.RefusedError
	s.Require().ErrorAs(err, &refused)
	s.Equal(KindStart, refused.Got.Kind)
}

func (s *EntrySuite) TestAReaderRefusesALineWithNoVersion() {
	var st Stop
	s.ErrorContains(codec.Unmarshal([]byte(`{"wake":"prs"}`), &st), "carries no api_version")
}
