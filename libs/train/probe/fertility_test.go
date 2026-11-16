package probe

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/suite"
)

type FertilitySuite struct{ tempDir }

func TestFertility(t *testing.T) { suite.Run(t, new(FertilitySuite)) }

func (s *FertilitySuite) TestHeldInKeepsOnlyHandWrittenTrainFiles() {
	rows := []Source{
		{Kind: "file", Path: "a.buzz", Language: "buzz", Split: "train"},
		{Kind: "file", Path: "b.buzz", Language: "buzz", Split: "holdout"},
		{Kind: "file", Path: "c.buzz", Language: "buzz", Split: "train", Generated: true},
		{Kind: "file", Path: "d.md", Language: "markdown", Split: "train"},
		{Kind: "input", Path: "e.buzz", Language: "buzz", Split: "train"},
	}
	var b strings.Builder
	for _, r := range rows {
		line, err := json.Marshal(r)
		s.Require().NoError(err)
		b.Write(append(line, '\n'))
	}
	got, err := HeldIn(s.write("sources.jsonl", b.String()), "buzz", "go")
	s.Require().NoError(err)
	s.Equal([]Source{rows[0]}, got)
}

func (s *FertilitySuite) TestReadHeldInCountsAFileThatChangedAsStale() {
	s.write("root/a.buzz", "kept")
	s.write("root/b.buzz", "edited")
	texts, stale, err := ReadHeldIn(filepath.Join(s.dir, "root"), []Source{
		{Path: "a.buzz", SHA256: sum("kept")},
		{Path: "b.buzz", SHA256: sum("original")},
		{Path: "gone.buzz", SHA256: sum("x")},
	})
	s.Require().NoError(err)
	s.Equal(2, stale)
	s.Require().Len(texts, 1)
	s.Equal("kept", string(texts[0].Body))
}

// byteEncoder is one token per byte, wrapped in a begin and an end token.
type byteEncoder struct{}

func (byteEncoder) Encode(text string) []int { return make([]int, len(text)+2) }

func (s *FertilitySuite) TestMeasureFertilityLeavesOutTheTemplateTokens() {
	got := MeasureFertility(byteEncoder{}, []Text{
		{Source: Source{Language: "go"}, Body: []byte("abcd")},
		{Source: Source{Language: "go"}, Body: []byte("ef")},
	})
	s.Equal(map[string]Fertility{"go": {Files: 2, Bytes: 6, Tokens: 6, TokensPerByte: 1}}, got)
}
