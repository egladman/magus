package main

import (
	"bufio"
	"bytes"
	"fmt"
	"strings"
)

type token struct{ form, upos, xpos string }

type sentence []token

// parseCoNLLU reads the syntactic words of each sentence. Multiword token
// ranges ("1-2 don't") and empty nodes ("8.1") are skipped: the words under a
// range carry the tags.
func parseCoNLLU(b []byte) ([]sentence, error) {
	var out []sentence
	var cur sentence
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for line := 1; sc.Scan(); line++ {
		text := sc.Text()
		switch {
		case text == "":
			if len(cur) > 0 {
				out = append(out, cur)
				cur = nil
			}
			continue
		case strings.HasPrefix(text, "#"):
			continue
		}
		cols := strings.Split(text, "\t")
		if len(cols) != 10 {
			return nil, fmt.Errorf("line %d: %d columns, want 10", line, len(cols))
		}
		if strings.ContainsAny(cols[0], "-.") {
			continue
		}
		cur = append(cur, token{form: cols[1], upos: cols[3], xpos: cols[4]})
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(cur) > 0 {
		out = append(out, cur)
	}
	return out, nil
}
