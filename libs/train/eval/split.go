// Package eval holds the datasets stage S1 builds for evaluation (the FIM dev and test
// sets and the Buzz pass@k task set), the 13-gram decontamination that keeps training text
// away from them, the temporal holdout listing and the G1 report. From stage S3 it also
// scores a model on them: the completion metrics of plan 6.1, pass@k of 6.2 and the
// latency replay, each with the interval and the clustered p-value plan 6 asks for.
package eval

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// Sides a file of the split is on.
const (
	SideTrain = "train"
	SideTest  = "test"
)

// DevTailShare is the share of a dev file's lines, counted from its end, reserved for the
// dev set. Training reads the file without them.
const DevTailShare = 0.2

// Entry is one row of split.jsonl, which split.buzz writes: a hand-written file, its
// provenance group, and its side. Dev marks a train-side file whose last DevTailShare of
// lines belong to the dev set.
type Entry struct {
	Path     string `json:"path"`
	Language string `json:"language"`
	Bytes    int    `json:"bytes"`
	Group    string `json:"group"`
	Side     string `json:"side"`
	Dev      bool   `json:"dev"`
}

// ReadSplit reads split.jsonl. A row with no path, an unknown side or a dev test file is
// an error: a test file has no dev tail.
func ReadSplit(path string) ([]Entry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []Entry
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for n := 1; sc.Scan(); n++ {
		var e Entry
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			return nil, fmt.Errorf("%s:%d: %w", path, n, err)
		}
		if e.Path == "" || e.Group == "" || (e.Side != SideTrain && e.Side != SideTest) || (e.Dev && e.Side == SideTest) {
			return nil, fmt.Errorf("%s:%d: needs a path, a group, side train or test, and no dev flag on a test file", path, n)
		}
		out = append(out, e)
	}
	return out, sc.Err()
}

// DevTailStart is the byte offset where the last DevTailShare of text's lines begin.
func DevTailStart(text string) int {
	lines := strings.SplitAfter(text, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	keep := int(float64(len(lines)) * (1 - DevTailShare))
	off := 0
	for _, l := range lines[:keep] {
		off += len(l)
	}
	return off
}
