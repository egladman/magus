package replay

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/suite"

)

type StoreSuite struct {
	suite.Suite
	dir    string
	header Header
}

func TestStore(t *testing.T) { suite.Run(t, new(StoreSuite)) }

func (s *StoreSuite) SetupTest() {
	s.dir = s.T().TempDir()
	s.header = Header{Script: "hack/dev/land.buzz", Args: []string{"--dry-run"}}
}

func call(seq, parent int, name string) Call {
	return Call{Seq: seq, Parent: parent, Name: name, ArgsDigest: "d" + name, End: callOpen, StartedAt: int64(seq)}
}

func (s *StoreSuite) TestStoreFoldsBeginAndEnd() {
	r, err := OpenLog(s.dir, "out1", s.header)
	s.Require().NoError(err)
	s.Require().NoError(r.begin(call(0, -1, "fs.readFile")))
	s.Require().NoError(r.begin(call(1, 0, "os.time")))
	s.Require().NoError(r.end(1, callReturned, outcome{Result: "12"}))
	s.Require().NoError(r.noteImports(map[string]string{"/w/lib.buzz": "ab12"}))
	s.Require().NoError(r.end(0, callRaised, outcome{Error: "no such file"}))
	s.Require().NoError(r.Close())

	raw, err := os.ReadFile(filepath.Join(s.dir, "out1", "calls.jsonl"))
	s.Require().NoError(err)
	s.Contains(string(raw), `"format":"magus.replay/v1alpha1"`)
	s.Contains(string(raw), `{"imports":{"/w/lib.buzz":"ab12"}}`, "the digests are a line of their own")

	h, calls, err := Read(s.dir, "out1")
	s.Require().NoError(err)
	want := s.header
	want.Format, want.Imports = format, map[string]string{"/w/lib.buzz": "ab12"}
	s.Equal(want, h)
	s.Equal([]Call{
		{Seq: 0, Parent: -1, Name: "fs.readFile", ArgsDigest: "dfs.readFile", End: callRaised, Error: "no such file", StartedAt: 0},
		{Seq: 1, Parent: 0, Name: "os.time", ArgsDigest: "dos.time", End: callReturned, Result: "12", StartedAt: 1},
	}, calls)
}

func (s *StoreSuite) TestStoreLeavesACrashedCallOpen() {
	r, err := OpenLog(s.dir, "out1", s.header)
	s.Require().NoError(err)
	s.Require().NoError(r.begin(call(0, -1, "os.time")))
	s.Require().NoError(r.end(0, callReturned, outcome{Result: "1"}))
	s.Require().NoError(r.begin(call(1, -1, "proc.exec")))
	s.Require().NoError(r.Close())
	// A torn end line is what a reboot mid-write leaves.
	path := filepath.Join(s.dir, "out1", "calls.jsonl")
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	s.Require().NoError(err)
	_, err = f.WriteString(`{"seq":1,"parent":-1,"na`)
	s.Require().NoError(err)
	s.Require().NoError(f.Close())

	_, calls, err := Read(s.dir, "out1")
	s.Require().NoError(err)
	s.Equal([]Call{
		{Seq: 0, Parent: -1, Name: "os.time", ArgsDigest: "dos.time", End: callReturned, Result: "1", StartedAt: 0},
		{Seq: 1, Parent: -1, Name: "proc.exec", ArgsDigest: "dproc.exec", End: callOpen, StartedAt: 1},
	}, calls)

	again, err := OpenLog(s.dir, "out1", s.header)
	s.Require().NoError(err, "a dead writer's record opens to append")
	s.Require().NoError(again.end(1, callReturned, outcome{Result: `"ok"`}))
	s.Require().NoError(again.begin(call(2, -1, "os.time")))
	s.Require().NoError(again.Close())
	_, calls, err = Read(s.dir, "out1")
	s.Require().NoError(err)
	s.Equal([]Call{
		{Seq: 0, Parent: -1, Name: "os.time", ArgsDigest: "dos.time", End: callReturned, Result: "1", StartedAt: 0},
		{Seq: 1, Parent: -1, Name: "proc.exec", ArgsDigest: "dproc.exec", End: callReturned, Result: `"ok"`, StartedAt: 1},
		{Seq: 2, Parent: -1, Name: "os.time", ArgsDigest: "dos.time", End: callOpen, StartedAt: 2},
	}, calls)
}

func (s *StoreSuite) TestStoreRefusesASecondWriter() {
	first, err := OpenLog(s.dir, "out1", s.header)
	s.Require().NoError(err)
	_, err = OpenLog(s.dir, "out1", s.header)
	s.ErrorContains(err, fmt.Sprintf("is being written by pid %d", os.Getpid()))
	s.Require().NoError(first.Close())

	second, err := OpenLog(s.dir, "out1", s.header)
	s.Require().NoError(err, "a released claim can be taken")
	s.Require().NoError(second.Close())
}

func (s *StoreSuite) TestARunThatEndsWithoutImportsSaysSo() {
	r, err := OpenLog(s.dir, "out1", s.header)
	s.Require().NoError(err)
	s.Require().NoError(r.Close())

	h, _, err := Read(s.dir, "out1")
	s.Require().NoError(err)
	s.Nil(h.Imports, "no digests arrived, so none are read back")
	s.Equal("the run ended before its entry compiled", h.ImportsUnread)

	again, err := OpenLog(s.dir, "out1", s.header)
	s.Require().NoError(err)
	s.ErrorContains(again.noteImports(map[string]string{}), "already holds its imports", "a record holds one imports line")
	s.Require().NoError(again.Close())
}

func (s *StoreSuite) TestAReopenedRecordMustBeItsOwn() {
	r, err := OpenLog(s.dir, "out1", s.header)
	s.Require().NoError(err)
	s.Require().NoError(r.Close())
	other := s.header
	other.Args = []string{"--for-real"}
	_, err = OpenLog(s.dir, "out1", other)
	s.ErrorContains(err, "was recorded for hack/dev/land.buzz [--dry-run]")
}

func (s *StoreSuite) TestAMissingRecordIsNoRecord() {
	_, _, err := Read(s.dir, "out9")
	s.ErrorIs(err, RefusalNoRecord)
}

func (s *StoreSuite) TestBeginAndEndRefuseWhatTheyCannotRecord() {
	r, err := OpenLog(s.dir, "out1", s.header)
	s.Require().NoError(err)
	ended := call(0, -1, "os.time")
	ended.End = callReturned
	s.ErrorContains(r.begin(ended), "a begun call is open")
	s.Require().NoError(r.begin(call(0, -1, "os.time")))
	s.ErrorContains(r.begin(call(0, -1, "os.time")), "already open")
	s.ErrorContains(r.end(5, callReturned, outcome{Result: "1"}), "no open call has that seq")
	s.ErrorContains(r.end(0, callOpen, outcome{}), "ends returned or raised")
	s.Require().NoError(r.Close())
	s.ErrorContains(r.Close(), "already closed")
}

func (s *StoreSuite) TestAFinishedRecordIsMarkedAndRefusesAppends() {
	r, err := OpenLog(s.dir, "out1", s.header)
	s.Require().NoError(err)
	s.Require().NoError(r.begin(call(0, -1, "os.time")))
	s.Require().NoError(r.end(0, callReturned, outcome{Result: "1"}))
	done, err := Finished(s.dir, "out1")
	s.Require().NoError(err)
	s.False(done, "an open record is not finished")
	s.Require().NoError(r.Finish())
	s.Require().NoError(r.Close())

	done, err = Finished(s.dir, "out1")
	s.Require().NoError(err)
	s.True(done)
	_, calls, err := Read(s.dir, "out1")
	s.Require().NoError(err)
	s.Len(calls, 1, "the footer is not a call")
	_, err = OpenLog(s.dir, "out1", s.header)
	s.ErrorContains(err, "is finished")
	_, err = Finished(s.dir, "out9")
	s.ErrorIs(err, RefusalNoRecord)
}

func (s *StoreSuite) TestDiscardDeletesTheRecordAndRefusesALiveWriter() {
	r, err := OpenLog(s.dir, "out1", s.header)
	s.Require().NoError(err)
	s.ErrorContains(Discard(s.dir, "out1"), "is being written by")
	s.Require().NoError(r.Close())

	s.Require().NoError(Discard(s.dir, "out1"))
	_, _, err = Read(s.dir, "out1")
	s.ErrorIs(err, RefusalNoRecord)
	s.Require().NoError(Discard(s.dir, "out1"), "a missing record is not an error")
}

func (s *StoreSuite) TestARefIsOnePathElement() {
	_, err := OpenLog(s.dir, "../out1", s.header)
	s.ErrorContains(err, "is not one path element")
}
