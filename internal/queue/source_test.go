package queue

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/suite"
)

type SourceSuite struct{ suite.Suite }

func TestSourceSuite(t *testing.T) { suite.Run(t, new(SourceSuite)) }

func (s *SourceSuite) TestParseSourceReadsADirectoryOrARun() {
	for arg, want := range map[string]Source{
		"verdicts":                {Dir: "verdicts"},
		"/tmp/queue/verdicts":     {Dir: "/tmp/queue/verdicts"},
		"./x:y":                   {Dir: "./x:y"},
		"x:y":                     {Dir: "x:y"},
		`C:\queue`:                {Dir: `C:\queue`},
		"dir/a:b":                 {Dir: "dir/a:b"},
		"run:acme/widgets/runs/7": {Run: "acme/widgets/runs/7"},
	} {
		got, err := ParseSource(arg)
		s.Require().NoError(err, arg)
		s.Equal(want, got, arg)
	}
}

func (s *SourceSuite) TestParseSourceRefusesWhatItCannotRead() {
	for arg, msg := range map[string]string{
		"":                                   "<source> is empty",
		"s3:bucket/verdicts":                 `unknown scheme "s3"`,
		"github-actions:acme/widgets/runs/7": `unknown scheme "github-actions"`,
		"run:":                               "want run:<run>",
		"run: ":                              "want run:<run>",
	} {
		_, err := ParseSource(arg)
		s.Require().ErrorContains(err, msg, "%q", arg)
	}
}

func (s *SourceSuite) TestRemoteHostNamesTheHostWithoutUserinfo() {
	for remote, want := range map[string]string{
		"https://example.invalid/acme/widgets.git":     "example.invalid",
		"https://user:pw@example.invalid:8443/a/b.git": "example.invalid",
		"git@example.invalid:acme/widgets.git":         "example.invalid",
		"/srv/git/widgets.git":                         "",
		"":                                             "",
	} {
		s.Equal(want, RemoteHost(remote), remote)
	}
}

func (s *SourceSuite) TestReadChangesFileReadsStdinForADash() {
	_, err := ReadChangesFile("-", strings.NewReader(""))
	s.Require().Error(err)
	_, err = ReadChangesFile(s.T().TempDir()+"/absent.json", strings.NewReader(""))
	s.Require().Error(err)
}
