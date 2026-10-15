package wordclass

import (
	"slices"
	"testing"
)

func BenchmarkSplit(b *testing.B) {
	sentences := ewt(b)
	b.ReportAllocs()

	for b.Loop() {
		for _, s := range sentences {
			Split(s.Text)
		}
	}
}

func TestSplit(t *testing.T) {
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
			if got := Split(tc.text); !slices.Equal(got, tc.want) {
				t.Errorf("Split(%q)\n got %q\nwant %q", tc.text, got, tc.want)
			}
		})
	}
}

// TestTagSplitWords checks the tagger over Split's words against classes
// checked by hand, so a tokenization the model was not trained on shows.
func TestTagSplitWords(t *testing.T) {
	const (
		adj, adp, adv, aux, cconj, det = Adjective, Adposition, Adverb, Auxiliary, CoordinatingConjunction, Determiner
		noun, num, part, pron, propn   = Noun, Numeral, Particle, Pronoun, ProperNoun
		punct, sconj, verb, x          = Punctuation, SubordinatingConjunction, Verb, Other
	)

	cases := []struct {
		text string
		want []Class
	}{
		{"I don't think it's ready.", []Class{pron, aux, part, verb, pron, aux, adj, punct}},
		{
			"The report, written by the team, wasn't reviewed.",
			[]Class{det, noun, punct, verb, adp, det, noun, punct, aux, part, verb, punct},
		},
		{
			"Running the tests takes time, though it's worth it.",
			[]Class{verb, det, noun, verb, noun, punct, sconj, pron, aux, adj, pron, punct},
		},
		{
			"We'll ship the well-known fix (see https://example.com) today.",
			[]Class{pron, aux, verb, det, adj, noun, punct, verb, x, punct, noun, punct},
		},
		{
			"She can't find the file and the directory it named.",
			[]Class{pron, aux, part, verb, det, noun, cconj, det, noun, pron, verb, punct},
		},
		{
			"Dr. Lee measured 3.5 seconds, nearly twice the old time!",
			[]Class{propn, propn, verb, num, noun, punct, adv, adv, det, adj, noun, punct},
		},
	}

	var words, right int
	for _, tc := range cases {
		tokens := Split(tc.text)
		if len(tokens) != len(tc.want) {
			t.Fatalf("Split(%q) = %q, want %d words", tc.text, tokens, len(tc.want))
		}
		for i, got := range Tag(tokens) {
			words++
			if got.Class == tc.want[i] {
				right++
			} else {
				t.Logf("%q: %q is %v, want %v", tc.text, tokens[i], got.Class, tc.want[i])
			}
		}
	}
	if acc := float64(right) / float64(words); acc < 0.93 {
		t.Errorf("UPOS accuracy over Split's words %.4f, want >= 0.93", acc)
	}
}

// TestSplitAgreesWithEWT reads each EWT test sentence's text and counts the
// sentences Split splits exactly as EWT did, once EWT's split hyphenated
// words are joined back, since Split keeps them whole.
func TestSplitAgreesWithEWT(t *testing.T) {
	sentences := ewt(t)

	exact := 0
	for _, s := range sentences {
		var forms []string
		attached, joining := false, false
		for _, w := range s.Words {
			glued := !w.SpaceAfter
			switch {
			case joining:
				forms[len(forms)-1] += w.Form
			case w.Form == "-" && attached && glued:
				forms[len(forms)-1] += w.Form
			default:
				forms = append(forms, w.Form)
			}
			joining = w.Form == "-" && attached && glued
			attached = glued
		}

		if got := Split(s.Text); slices.Equal(got, forms) {
			exact++
		} else if testing.Verbose() {
			t.Logf("%q\n got %q\nwant %q", s.Text, got, forms)
		}
	}
	share := float64(exact) / float64(len(sentences))
	t.Logf("Split matches EWT on %d of %d test sentences (%.4f)", exact, len(sentences), share)
	// The misses left are web text: typos EWT splits ("dont"), slashes between
	// words and emoticons.
	if share < 0.89 {
		t.Errorf("Split matches EWT on %.4f of its test sentences, want >= 0.89", share)
	}
}
