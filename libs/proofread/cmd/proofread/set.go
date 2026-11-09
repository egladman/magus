package main

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/egladman/magus/libs/proofread"
)

// setNode names a set's findings, which belong to no one text.
const setNode = "set"

// judgeSet returns a judge for `--set`: the NUL-separated texts on stdin,
// judged as one set. A set too short to judge says so on stderr.
func judgeSet(fromStdin bool, stderr io.Writer) func(proofread.Kind, []string, io.Reader, table, []proofread.Option) ([]finding, error) {
	return func(kind proofread.Kind, _ []string, stdin io.Reader, t table, opts []proofread.Option) ([]finding, error) {
		if !fromStdin {
			return nil, errors.New("--set reads its texts from stdin, NUL-separated: pass --stdin")
		}

		data, err := io.ReadAll(stdin)
		if err != nil {
			return nil, fmt.Errorf("read the set: %w", err)
		}

		texts := strings.Split(string(data), "\x00")

		found, judged := proofread.JudgeSet(texts, kind, append(opts, proofread.WithDecisions(t.rules))...)
		if !judged {
			fmt.Fprintf(stderr, "proofread: the set of %d texts holds under 200 words, so no set rule judged it\n", len(texts))
		}

		out := []finding{}
		for _, f := range found {
			out = append(out, toFinding(setNode, setNode, kind, f))
		}

		return out, nil
	}
}
