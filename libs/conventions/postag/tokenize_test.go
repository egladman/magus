package postag

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"os"
	"slices"
	"strings"
	"testing"
)

func TestTokenize(t *testing.T) {
	cases := map[string]struct {
		text string
		want []string
	}{
		"punctuation": {
			`He said "wait," then (quietly) left; why?`,
			[]string{"He", "said", `"`, "wait", ",", `"`, "then", "(", "quietly", ")", "left", ";", "why", "?"},
		},
		"runs of end punctuation": {"Really?! No... fine!!", []string{"Really", "?!", "No", "...", "fine", "!!"}},
		"clitics": {
			"I don't think they're sure we've seen what'll happen, and I'd say I'm late.",
			[]string{
				"I", "do", "n't", "think", "they", "'re", "sure", "we", "'ve", "seen", "what", "'ll", "happen", ",",
				"and", "I", "'d", "say", "I", "'m", "late", ".",
			},
		},
		"clitics with a curly apostrophe": {"It’s fine, isn’t it", []string{"It", "’s", "fine", ",", "is", "n’t", "it"}},
		"split stems":                     {"You can't, won't, cannot.", []string{"You", "ca", "n't", ",", "wo", "n't", ",", "can", "not", "."}},
		"a possessive":                    {"The team's build and the users' logs", []string{"The", "team", "'s", "build", "and", "the", "users", "'", "logs"}},
		"hyphenated words": {
			"A well-known, long-running job.",
			[]string{"A", "well-known", ",", "long-running", "job", "."},
		},
		"dots between words": {"at this point...lol", []string{"at", "this", "point", "...", "lol"}},
		"dashes":             {"It ran—twice -- then stopped", []string{"It", "ran", "—", "twice", "--", "then", "stopped"}},
		"URLs and paths": {
			"See https://example.com/a?b=1. Or ./cmd/magus, /etc/hosts and ~/.config (or www.example.org).",
			[]string{
				"See", "https://example.com/a?b=1", ".", "Or", "./cmd/magus", ",", "/etc/hosts", "and", "~/.config",
				"(", "or", "www.example.org", ")", ".",
			},
		},
		"an email address": {"Mail eli@example.com.", []string{"Mail", "eli@example.com", "."}},
		"numbers":          {"It took 3.14 s, 1,000 runs, $5 and 50% of 2.", []string{"It", "took", "3.14", "s", ",", "1,000", "runs", ",", "$", "5", "and", "50", "%", "of", "2", "."}},
		"abbreviations": {
			"Dr. Smith, e.g. the U.S. team, J. Doe etc. and I.",
			[]string{"Dr.", "Smith", ",", "e.g.", "the", "U.S.", "team", ",", "J.", "Doe", "etc.", "and", "I", "."},
		},
		"emphasis": {"a **bold** word", []string{"a", "*", "*", "bold", "*", "*", "word"}},
		"no words": {"  \t ", nil},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := Tokenize(tc.text); !slices.Equal(got, tc.want) {
				t.Errorf("Tokenize(%q)\n got %q\nwant %q", tc.text, got, tc.want)
			}
		})
	}
}

// TestTagTokenized checks the tagger over Tokenize's words against tags
// checked by hand, so a tokenization the model was not trained on shows.
func TestTagTokenized(t *testing.T) {
	cases := []struct {
		text string
		want []UPOS
	}{
		{"I don't think it's ready.", []UPOS{PRON, AUX, PART, VERB, PRON, AUX, ADJ, PUNCT}},
		{
			"The report, written by the team, wasn't reviewed.",
			[]UPOS{DET, NOUN, PUNCT, VERB, ADP, DET, NOUN, PUNCT, AUX, PART, VERB, PUNCT},
		},
		{
			"Running the tests takes time, though it's worth it.",
			[]UPOS{VERB, DET, NOUN, VERB, NOUN, PUNCT, SCONJ, PRON, AUX, ADJ, PRON, PUNCT},
		},
		{
			"We'll ship the well-known fix (see https://example.com) today.",
			[]UPOS{PRON, AUX, VERB, DET, ADJ, NOUN, PUNCT, VERB, X, PUNCT, NOUN, PUNCT},
		},
		{
			"She can't find the file and the directory it named.",
			[]UPOS{PRON, AUX, PART, VERB, DET, NOUN, CCONJ, DET, NOUN, PRON, VERB, PUNCT},
		},
		{
			"Dr. Lee measured 3.5 seconds, nearly twice the old time!",
			[]UPOS{PROPN, PROPN, VERB, NUM, NOUN, PUNCT, ADV, ADV, DET, ADJ, NOUN, PUNCT},
		},
	}

	var words, right int
	for _, tc := range cases {
		tokens := Tokenize(tc.text)
		if len(tokens) != len(tc.want) {
			t.Fatalf("Tokenize(%q) = %q, want %d words", tc.text, tokens, len(tc.want))
		}
		for i, got := range Tag(tokens) {
			words++
			if got.UPOS == tc.want[i] {
				right++
			} else {
				t.Logf("%q: %q is %v, want %v", tc.text, tokens[i], got.UPOS, tc.want[i])
			}
		}
	}
	if acc := float64(right) / float64(words); acc < 0.93 {
		t.Errorf("UPOS accuracy over Tokenize's words %.4f, want >= 0.93", acc)
	}
}

// TestTokenizeAgreesWithEWT reads each EWT test sentence's text and counts the
// sentences Tokenize splits exactly as EWT did, once EWT's split hyphenated
// words are joined back, since Tokenize keeps them whole.
func TestTokenizeAgreesWithEWT(t *testing.T) {
	gz, err := os.ReadFile("testdata/en_ewt-ud-test.conllu.gz")
	if err != nil {
		t.Fatal(err)
	}
	zr, err := gzip.NewReader(bytes.NewReader(gz))
	if err != nil {
		t.Fatal(err)
	}
	type sentence struct {
		text  string
		forms []string
	}
	var sentences []sentence
	var cur sentence
	attached, joining := false, false
	sc := bufio.NewScanner(zr)
	for sc.Scan() {
		line := sc.Text()
		if text, ok := strings.CutPrefix(line, "# text = "); ok {
			cur.text = text
			continue
		}
		if line == "" {
			if len(cur.forms) > 0 {
				sentences = append(sentences, cur)
			}
			cur, attached, joining = sentence{}, false, false
			continue
		}
		cols := strings.Split(line, "\t")
		if strings.HasPrefix(line, "#") || len(cols) != 10 || strings.ContainsAny(cols[0], "-.") {
			continue
		}
		form, glued := cols[1], strings.Contains(cols[9], "SpaceAfter=No")
		switch {
		case joining:
			cur.forms[len(cur.forms)-1] += form
		case form == "-" && attached && glued:
			cur.forms[len(cur.forms)-1] += form
		default:
			cur.forms = append(cur.forms, form)
		}
		joining = form == "-" && attached && glued
		attached = glued
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}

	exact := 0
	for _, s := range sentences {
		if got := Tokenize(s.text); slices.Equal(got, s.forms) {
			exact++
		} else if testing.Verbose() {
			t.Logf("%q\n got %q\nwant %q", s.text, got, s.forms)
		}
	}
	share := float64(exact) / float64(len(sentences))
	t.Logf("Tokenize matches EWT on %d of %d test sentences (%.4f)", exact, len(sentences), share)
	// The misses left are web text: typos EWT splits ("dont"), slashes between
	// words and emoticons.
	if share < 0.89 {
		t.Errorf("Tokenize matches EWT on %.4f of its test sentences, want >= 0.89", share)
	}
}
