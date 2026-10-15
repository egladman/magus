package conllu

import (
	"bufio"
	"fmt"
	"io"
	"slices"
	"strings"
)

// Sentence is one sentence of a CoNLL-U file: its "# text = " comment and its
// syntactic words.
type Sentence struct {
	Text  string
	Words []Token
}

// Token is one syntactic word. UPOS is the Universal Dependencies tag and XPOS
// the treebank's own, Penn Treebank in UD English.
type Token struct {
	Form, UPOS, XPOS string
	// SpaceAfter is false when the word's MISC column says SpaceAfter=No. A
	// multiword token carries that on its range line, which Read skips.
	SpaceAfter bool
}

// Read reads every sentence with at least one word. It skips comments other
// than the text, multiword token ranges ("1-2 don't") and empty nodes ("8.1"):
// the words under a range carry the tags. A word line without 10 columns is an
// error.
func Read(r io.Reader) ([]Sentence, error) {
	var (
		out []Sentence
		cur Sentence
	)

	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for line := 1; sc.Scan(); line++ {
		text := sc.Text()
		if text == "" {
			if len(cur.Words) > 0 {
				out = append(out, cur)
			}

			cur = Sentence{}

			continue
		}

		if comment, ok := strings.CutPrefix(text, "#"); ok {
			if t, ok := strings.CutPrefix(comment, " text = "); ok {
				cur.Text = t
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

		cur.Words = append(cur.Words, Token{
			Form:       cols[1],
			UPOS:       cols[3],
			XPOS:       cols[4],
			SpaceAfter: !slices.Contains(strings.Split(cols[9], "|"), "SpaceAfter=No"),
		})
	}

	if err := sc.Err(); err != nil {
		return nil, err
	}

	if len(cur.Words) > 0 {
		out = append(out, cur)
	}

	return out, nil
}

// Forms returns the form of each word, in order.
func (s Sentence) Forms() []string {
	forms := make([]string, len(s.Words))
	for i, w := range s.Words {
		forms[i] = w.Form
	}

	return forms
}
