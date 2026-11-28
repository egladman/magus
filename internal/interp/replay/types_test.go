package replay

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/libs/codec"
)

type TypesSuite struct {
	suite.Suite
}

func TestTypes(t *testing.T) { suite.Run(t, new(TypesSuite)) }

func (s *TypesSuite) TestCallEntryRoundTrips() {
	ended := Call{
		Seq: 0, Parent: -1, Name: "fs.readFile", ArgsDigest: "ab12", End: callReturned,
		Result: `{"map":[["k",1]]}`, StartedAt: 1764200000000,
	}
	line, err := codec.Marshal(ended)
	s.Require().NoError(err)
	s.JSONEq(`{"seq":0,"parent":-1,"name":"fs.readFile","args_digest":"ab12","end":"returned",`+
		`"result":{"map":[["k",1]]},"started_at":1764200000000}`, string(line))

	var back Call
	s.Require().NoError(codec.Unmarshal(line, &back))
	s.Equal(ended, back)

	open := Call{Seq: 3, Parent: 0, Name: "proc.exec", ArgsDigest: "cd34", End: callOpen, StartedAt: 1}
	line, err = codec.Marshal(open)
	s.Require().NoError(err)
	s.NotContains(string(line), `"result"`)
	back = Call{}
	s.Require().NoError(codec.Unmarshal(line, &back))
	s.Equal(open, back)

	raised := Call{Seq: 5, Parent: -1, Name: "fs.readFile", ArgsDigest: "ef56", End: callRaised, Error: "no such file", StartedAt: 2}
	line, err = codec.Marshal(raised)
	s.Require().NoError(err)
	s.JSONEq(`{"seq":5,"parent":-1,"name":"fs.readFile","args_digest":"ef56","end":"raised","error":"no such file","started_at":2}`, string(line))
	back = Call{}
	s.Require().NoError(codec.Unmarshal(line, &back))
	s.Equal(raised, back)

	unencodable := Call{Seq: 4, Parent: -1, Name: "io.open", End: callReturned, Unencodable: "direct at [\"write\"]"}
	line, err = codec.Marshal(unencodable)
	s.Require().NoError(err)
	back = Call{}
	s.Require().NoError(codec.Unmarshal(line, &back))
	s.Equal(unencodable, back)
}

func (s *TypesSuite) TestALineMissingSeqOrParentIsRefused() {
	var c Call
	s.ErrorContains(codec.Unmarshal([]byte(`{"name":"x","end":"open","parent":-1}`), &c), "seq and parent are required")
	s.ErrorContains(codec.Unmarshal([]byte(`{"seq":1,"name":"x","end":"open"}`), &c), "seq and parent are required")
}

func (s *TypesSuite) TestAnUnknownEndIsRefused() {
	var c Call
	s.ErrorContains(codec.Unmarshal([]byte(`{"seq":1,"parent":-1,"name":"x","end":"done"}`), &c), `end "done" is not one of [open returned raised]`)
	_, err := codec.Marshal(Call{Seq: 1, Parent: -1, Name: "x"})
	s.ErrorContains(err, `end "" is not one of`)
}

func (s *TypesSuite) TestAResultThatIsNotJSONIsRefused() {
	_, err := codec.Marshal(Call{Seq: 1, Parent: -1, Name: "x", End: callReturned, Result: "{"})
	s.ErrorContains(err, "result is not JSON")
}

func (s *TypesSuite) TestARefusalIsAnErrorValue() {
	err := fmt.Errorf("no record at r: %w", RefusalNoRecord)
	s.True(errors.Is(err, RefusalNoRecord))
	s.False(errors.Is(err, RefusalDiverged))
	s.Equal("no record at r: resume refused: no-record", err.Error())
}
