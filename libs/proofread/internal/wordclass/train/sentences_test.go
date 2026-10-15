package train

import (
	"reflect"
	"strings"
	"testing"
)

func TestReadSentences(t *testing.T) {
	const doc = "# newdoc id = d\n" +
		"# text = I don't.\n" +
		"1-2\tdon't\t_\t_\t_\t_\t_\t_\t_\tSpaceAfter=No\n" +
		"1\tI\tI\tPRON\tPRP\t_\t2\tnsubj\t_\t_\n" +
		"2\tdo\tdo\tAUX\tVBP\t_\t0\troot\t_\t_\n" +
		"3\tn't\tnot\tPART\tRB\t_\t2\tadvmod\t_\tSpaceAfter=No\n" +
		"3.1\tgone\t_\t_\t_\t_\t_\t_\t_\t_\n" +
		"4\t.\t.\tPUNCT\t.\t_\t2\tpunct\t_\tGloss=x|SpaceAfter=No\n" +
		"\n" +
		"\n" +
		"# text = Yes\n" +
		"1\tYes\tyes\tINTJ\tUH\t_\t0\troot\t_\t_\n"

	got, err := readSentences(strings.NewReader(doc))
	if err != nil {
		t.Fatal(err)
	}

	want := []sentence{
		{text: "I don't.", words: []token{
			{"I", "PRON", "PRP", true}, {"do", "AUX", "VBP", true},
			{"n't", "PART", "RB", false}, {".", "PUNCT", ".", false},
		}},
		{text: "Yes", words: []token{{"Yes", "INTJ", "UH", true}}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("readSentences\n got %+v\nwant %+v", got, want)
	}

	if forms := got[0].forms(); !reflect.DeepEqual(forms, []string{"I", "do", "n't", "."}) {
		t.Errorf("forms() = %q", forms)
	}
}

func TestReadSentencesColumns(t *testing.T) {
	_, err := readSentences(strings.NewReader("# text = x\n1\tx\n"))
	if err == nil || err.Error() != "line 2: 2 columns, want 10" {
		t.Errorf("readSentences of a short line: %v", err)
	}
}
