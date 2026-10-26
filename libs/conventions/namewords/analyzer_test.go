package namewords

import (
	"slices"
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"
)

func options() Options {
	return Options{
		Suffixes: []string{"To", "For", "Of"},
		ShortForms: []string{
			"Src", "Dst", "Cfg", "Msg", "Idx", "Tmp", "Ptr",
			"Ctx", "Req", "Resp", "Rsp", "Buf", "Fn", "Opts", "Str", "Strs", "Err", "Cmd", "Cwd", "Prev", "Impl", "Repo",
		},
	}
}

// TestAnalyzer covers each word list, both name spellings, a method, and each
// exemption: entry points, a method satisfying a standard library interface,
// and a generated file.
func TestAnalyzer(t *testing.T) {
	analyzer, err := New(options())
	if err != nil {
		t.Fatal(err)
	}
	analysistest.Run(t, analysistest.TestData(), analyzer, "names", "generated")
}

func TestNewRequiresAWordList(t *testing.T) {
	if _, err := New(Options{}); err == nil {
		t.Fatal("expected empty word lists to fail at construction")
	}
}

func TestSplitWords(t *testing.T) {
	for name, want := range map[string][]string{
		"graphOf":       {"graph", "Of"},
		"URLFor":        {"URL", "For"},
		"value_of":      {"value", "of"},
		"parseHTTPSrc":  {"parse", "HTTP", "Src"},
		"base64Decode":  {"base", "64", "Decode"},
		"Of":            {"Of"},
		"ensureDstSafe": {"ensure", "Dst", "Safe"},
	} {
		if got := splitWords(name); !slices.Equal(got, want) {
			t.Errorf("splitWords(%q) = %q, want %q", name, got, want)
		}
	}
}
