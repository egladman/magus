// Command judge-docs judges symbol docs against the prose rules. It reads a
// JSON array of symbol records on stdin and writes a JSON array of findings on
// stdout, in input order and then the order [prose.Judge] reports them.
//
// This repository's lint rule pipes the symbols of `magus\symbols()` through it.
// The magus module never imports libs/conventions, so the rules stay this
// repository's policy.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/egladman/magus/libs/conventions/prose"
)

// record is one symbol as `magus\symbols()` describes it.
type record struct {
	Node     string `json:"node"`
	Source   string `json:"source"`
	Language string `json:"language"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	Owner    string `json:"owner"`
	Doc      string `json:"doc"`
}

// finding is one rule's verdict, carrying the record's identity so a caller can
// point at the declaration.
type finding struct {
	Node     string `json:"node"`
	Source   string `json:"source"`
	Language string `json:"language"`
	Rule     string `json:"rule"`
	Message  string `json:"message"`
	Match    string `json:"match"`
}

func main() {
	os.Exit(run(os.Stdin, os.Stdout, os.Stderr))
}

// run returns the process exit code: 0 once the findings are written, 1 when
// stdin is not an array of records.
func run(stdin io.Reader, stdout, stderr io.Writer) int {
	records, err := decode(stdin)
	if err != nil {
		fmt.Fprintf(stderr, "judge-docs: %v\n", err)

		return 1
	}

	out := []finding{}

	for _, r := range records {
		symbol := prose.Symbol{
			Name:     r.Name,
			Owner:    r.Owner,
			Callable: r.Kind == "function" || r.Kind == "method",
			Doc:      r.Doc,
		}

		for _, f := range prose.Judge(symbol) {
			out = append(out, finding{
				Node: r.Node, Source: r.Source, Language: r.Language,
				Rule: string(f.Rule), Message: f.Message, Match: f.Match,
			})
		}
	}

	if err := json.NewEncoder(stdout).Encode(out); err != nil {
		fmt.Fprintf(stderr, "judge-docs: write findings: %v\n", err)

		return 1
	}

	return 0
}

func decode(stdin io.Reader) ([]record, error) {
	dec := json.NewDecoder(stdin)
	dec.DisallowUnknownFields()

	var records []record
	if err := dec.Decode(&records); err != nil {
		return nil, fmt.Errorf("read symbols: %w", err)
	}

	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("read symbols: data after the array")
	}

	return records, nil
}
