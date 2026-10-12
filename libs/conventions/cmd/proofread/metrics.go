package main

import (
	"fmt"
	"io"
	"os"

	"github.com/egladman/magus/libs/conventions/proofread"
)

// measured is one text's metrics beside the name a finding from it carries.
type measured struct {
	Source string `json:"source"`
	proofread.Metrics
}

// runMetrics writes the readability metrics of each text kind names, as a
// JSON array in input order: each file of a kind read from files, each
// symbol's doc of a doc-comment array, or the one text on stdin.
func runMetrics(kind proofread.Kind, paths []string, stdin io.Reader, stdout, stderr io.Writer) int {
	out, err := measure(kind, paths, stdin)
	if err != nil {
		return failure(stderr, err)
	}

	if err := writeJSON(stdout, out); err != nil {
		return failure(stderr, err)
	}

	return 0
}

func measure(kind proofread.Kind, paths []string, stdin io.Reader) ([]measured, error) {
	out := []measured{}

	switch {
	case kind == proofread.KindDocComment:
		if len(paths) > 0 {
			return nil, fmt.Errorf("a %s is read from stdin, not from a path", kind)
		}

		records, err := decode(stdin)
		if err != nil {
			return nil, err
		}

		for _, r := range records {
			out = append(out, measured{r.Source, proofread.Measure(r.Doc, kind)})
		}
	case readsFiles(kind):
		for _, p := range paths {
			text, err := os.ReadFile(p)
			if err != nil {
				return nil, fmt.Errorf("read %s: %w", p, err)
			}

			out = append(out, measured{p, proofread.Measure(string(text), kind)})
		}
	default:
		if len(paths) > 0 {
			return nil, fmt.Errorf("a %s is read from stdin, not from a path", kind)
		}

		text, err := io.ReadAll(stdin)
		if err != nil {
			return nil, fmt.Errorf("read the %s: %w", kind, err)
		}

		out = append(out, measured{string(kind), proofread.Measure(string(text), kind)})
	}

	return out, nil
}
