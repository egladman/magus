package wordclass

import (
	"bufio"
	"fmt"
	"io"
	"slices"
	"strings"
)

// sentence is one sentence of a CoNLL-U file: its "# text = " comment and its
// syntactic words.
type sentence struct {
	text  string
	words []token
}

// token is one syntactic word. upos is the Universal Dependencies tag and xpos
// the treebank's own, Penn Treebank in UD English.
type token struct {
	form, upos, xpos string
	// spaceAfter is false when the word's MISC column says SpaceAfter=No. A
	// multiword token carries that on its range line, which readSentences skips.
	spaceAfter bool
}

// readSentences reads every sentence with at least one word. It skips comments
// other than the text, multiword token ranges ("1-2 don't") and empty nodes
// ("8.1"): the words under a range carry the tags. A word line without 10
// columns is an error.
func readSentences(r io.Reader) ([]sentence, error) {
	var (
		out []sentence
		cur sentence
	)

	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for line := 1; sc.Scan(); line++ {
		text := sc.Text()
		if text == "" {
			if len(cur.words) > 0 {
				out = append(out, cur)
			}

			cur = sentence{}

			continue
		}

		if comment, ok := strings.CutPrefix(text, "#"); ok {
			if t, ok := strings.CutPrefix(comment, " text = "); ok {
				cur.text = t
			}

			continue
		}

		cols := strings.Split(text, "\t")
		if len(cols) != 10 {
			return nil, fmt.Errorf("line %d: %d columns, want 10", line, len(cols))
		}

		if strings.ContainsAny(cols[0], "-.") {
			continue
		}

		cur.words = append(cur.words, token{
			form:       cols[1],
			upos:       cols[3],
			xpos:       cols[4],
			spaceAfter: !slices.Contains(strings.Split(cols[9], "|"), "SpaceAfter=No"),
		})
	}

	if err := sc.Err(); err != nil {
		return nil, err
	}

	if len(cur.words) > 0 {
		out = append(out, cur)
	}

	return out, nil
}

func (s sentence) forms() []string {
	forms := make([]string, len(s.words))
	for i, w := range s.words {
		forms[i] = w.form
	}

	return forms
}
