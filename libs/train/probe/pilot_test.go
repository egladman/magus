package probe

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/suite"
)

type PilotSuite struct{ tempDir }

func TestPilot(t *testing.T) { suite.Run(t, new(PilotSuite)) }

func (s *PilotSuite) TestChunksEndAtLineEnds() {
	got := Chunks([]byte("aa\nbb\ncc\n"), 6)
	s.Equal([][]byte{[]byte("aa\nbb\n"), []byte("cc\n")}, got)
}

func (s *PilotSuite) TestChunksKeepALongLineWhole() {
	got := Chunks([]byte("a\nbbbbbbbb\nc"), 4)
	s.Equal([][]byte{[]byte("a\n"), []byte("bbbbbbbb\n"), []byte("c")}, got)
}

func (s *PilotSuite) TestPackJoinsShortFilesUpToTheLimit() {
	got := Pack([]Text{{Body: []byte("a\n")}, {Body: []byte("b\n")}, {Body: []byte("c\n")}}, 7)
	s.Equal([][]byte{[]byte("a\n\n\nb\n"), []byte("c\n")}, got)
}

func (s *PilotSuite) TestParseReportsIgnoresColourCodes() {
	got := ParseReports("  \x1b[38;5;244m  10\x1b[0m    \x1b[1;32m2.971 ▼\x1b[0m    \x1b[1m  423\x1b[0m    \x1b[38;5;244m  8.1k\x1b[0m\n")
	s.Equal([]Report{{Step: 10, Loss: 2.971, TokensPerSec: 423, TrainedTokens: 8100}}, got)
}

func (s *PilotSuite) TestWriteDataSendsEveryNthChunkToValid() {
	texts := []Text{{Body: []byte("1\n2\n3\n4\n")}}
	d, err := WriteData(filepath.Join(s.dir, "data"), texts, 2, 2)
	s.Require().NoError(err)
	s.Equal(2, d.Train)
	s.Equal(2, d.Valid)
	s.Equal([]string{"1\n", "3\n"}, s.readTexts(filepath.Join(d.Dir, "train.jsonl")))
	s.Equal([]string{"2\n", "4\n"}, s.readTexts(filepath.Join(d.Dir, "valid.jsonl")))
}

func (s *PilotSuite) readTexts(path string) []string {
	f, err := os.Open(path)
	s.Require().NoError(err)
	defer f.Close()
	var out []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var row struct{ Text string }
		s.Require().NoError(json.Unmarshal(sc.Bytes(), &row))
		out = append(out, row.Text)
	}
	return out
}

func (s *PilotSuite) TestWriteDataRefusesTooFewChunksForBothFiles() {
	_, err := WriteData(filepath.Join(s.dir, "data"), []Text{{Body: []byte("1\n")}}, 2, 2)
	s.ErrorContains(err, "and mlx-lm needs both")
}

const mlxOutput = `Loading pretrained model
  step    loss       tok/s    trained
    10    2.500 ▼    1,000     40.0k
    20    2.600 ▲    2,000     80.0k
    30    2.100 ▼    4,000    120.0k
`

func (s *PilotSuite) TestParseReportsReadsEachReportLine() {
	got := ParseReports(mlxOutput)
	s.Equal([]Report{
		{Step: 10, Loss: 2.5, TokensPerSec: 1000, TrainedTokens: 40000},
		{Step: 20, Loss: 2.6, TokensPerSec: 2000, TrainedTokens: 80000},
		{Step: 30, Loss: 2.1, TokensPerSec: 4000, TrainedTokens: 120000},
	}, got)
}

func (s *PilotSuite) TestSteadyTokensPerSecSkipsTheFirstWindow() {
	// 40k tokens at 2,000/s and 40k at 4,000/s: 80k tokens in 30 s.
	s.InDelta(80000.0/30, SteadyTokensPerSec(ParseReports(mlxOutput)), 1e-6)
}

func (s *PilotSuite) TestSummarizeProjectsTheCompletionRun() {
	var r PilotRow
	r.Summarize([]Report{{Step: 10, TrainedTokens: 1000, TokensPerSec: 1}, {Step: 20, Loss: 1.5, TrainedTokens: 2000, TokensPerSec: 1000}})
	s.Equal(20, r.Steps)
	s.Equal(int64(2000), r.Tokens)
	s.InDelta(1.5, r.LastLoss, 1e-9)
	s.InDelta(CompletionTokens/1000.0/3600, r.ProjectedHours, 1e-9)
}

func (s *PilotSuite) TestParseUsageReadsBothMemoryLines() {
	got, err := ParseUsage("        1.00 real\n  123456  maximum resident set size\n  98  page reclaims\n  654321  peak memory footprint\n")
	s.Require().NoError(err)
	s.Equal(Usage{MaxRSSBytes: 123456, PeakFootprintBytes: 654321}, got)
}

func (s *PilotSuite) TestParseUsageRefusesOutputWithoutTimeLines() {
	_, err := ParseUsage("done\n")
	s.ErrorContains(err, "peak memory footprint")
}

func (s *PilotSuite) TestOutOfMemoryReadsMetalsError() {
	s.True(OutOfMemory("[METAL] Command buffer execution failed: Insufficient Memory (00000008:kIOGPUCommandBufferCallbackErrorOutOfMemory)"))
	s.False(OutOfMemory("Traceback: ValueError: bad config"))
}
