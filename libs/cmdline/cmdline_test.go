package cmdline

import (
	"flag"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"
)

func ExampleWalk() {
	root := Command{
		Name: "tool",
		Children: []Command{
			{Name: "run", Flags: []Flag{{Name: "dry-run", Kind: Bool, Default: false, Doc: "Print what would run"}}},
			{Name: "config", Children: []Command{{Name: "cache", Children: []Command{{Name: "prune"}}}}},
		},
	}
	Walk(root, func(path string, c Command) {
		fmt.Println(path, len(c.Flags))
	})
	// Output:
	// run 1
	// config 0
	// config cache 0
	// config cache prune 0
}

func TestBindFlagsUsesEachKindsDefault(t *testing.T) {
	c := Command{Flags: []Flag{
		{Name: "b", Kind: Bool, Default: true},
		{Name: "s", Kind: String, Default: "x"},
		{Name: "i", Kind: Int},
		{Name: "d", Kind: Duration, Default: 5 * time.Second},
		{Name: "c", Kind: Custom},
	}}
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	c.BindFlags(fs)
	got := map[string]string{}
	fs.VisitAll(func(f *flag.Flag) { got[f.Name] = f.DefValue })
	want := map[string]string{"b": "true", "s": "x", "i": "0", "d": "5s", "c": ""}
	if !reflect.DeepEqual(want, got) {
		t.Errorf("defaults: want %v, got %v", want, got)
	}
}

func TestBindFlagsRefusesAMistypedDefault(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("an int flag with a string default bound without a panic")
		}
	}()
	Command{Flags: []Flag{{Name: "n", Kind: Int, Default: "8"}}}.BindFlags(flag.NewFlagSet("t", flag.ContinueOnError))
}

func TestBindFlagsRefusesAnUnknownKind(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("a flag with no Kind bound without a panic")
		}
	}()
	Command{Flags: []Flag{{Name: "n"}}}.BindFlags(flag.NewFlagSet("t", flag.ContinueOnError))
}

func negatableFlagSet(t *testing.T) (*flag.FlagSet, *bool) {
	t.Helper()
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	on := true
	fs.BoolVar(&on, "cache", true, "Replay hits")
	NegatedBoolVar(fs, &on, "cache")
	return fs, &on
}

func TestNegationTurnsABoolOffAndTakesNoValue(t *testing.T) {
	cases := []struct {
		args    []string
		want    bool
		wantErr bool
	}{
		{nil, true, false},
		{[]string{"--no-cache"}, false, false},
		{[]string{"-no-cache"}, false, false},
		{[]string{"--cache=false"}, false, false},
		{[]string{"--no-cache", "--cache"}, true, false},
		{[]string{"--no-cache=false"}, true, true},
		{[]string{"--no-cache=0"}, true, true},
	}
	for _, c := range cases {
		fs, on := negatableFlagSet(t)
		err := fs.Parse(c.args)
		if (err != nil) != c.wantErr {
			t.Errorf("%v: err = %v, want error %v", c.args, err, c.wantErr)
		}
		if err == nil && *on != c.want {
			t.Errorf("%v: cache = %v, want %v", c.args, *on, c.want)
		}
	}
}

func TestNegationRefusalNamesTheSpellingToUse(t *testing.T) {
	fs, _ := negatableFlagSet(t)
	err := fs.Parse([]string{"--no-cache=false"})
	if err == nil || !strings.Contains(err.Error(), "write --cache") {
		t.Errorf("want a refusal naming --cache, got %v", err)
	}
}

func TestShortBoolsGetNoNegation(t *testing.T) {
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	Command{Flags: []Flag{{Name: "v", Kind: Bool}}}.BindFlags(fs)
	if fs.Lookup("no-v") != nil {
		t.Error("a one-character flag registered --no-v")
	}
}

func TestBindFlagsRefusesWhatCheckRefuses(t *testing.T) {
	for name, f := range map[string]Flag{
		"no- prefix":      {Name: "no-cache", Kind: Bool, Default: false},
		"missing default": {Name: "cache", Kind: Bool},
	} {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Error("bound without a panic")
				}
			}()
			Command{Flags: []Flag{f}}.BindFlags(flag.NewFlagSet("t", flag.ContinueOnError))
		})
	}
}

func TestFlagHelpFoldsTheNegationAndPrintsTheDefault(t *testing.T) {
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	Command{Flags: []Flag{
		{Name: "cache", Kind: Bool, Default: true, Doc: "Replay hits"},
		{Name: "symbols", Kind: Bool, Default: false, Doc: "Include a `magus diff --baseline` input"},
		{Name: "depth", Kind: Int, Default: 3, Doc: "How `deep`"},
		{Name: "c", Kind: Bool, AliasOf: "cache", Doc: "Short for --cache"},
	}}.BindFlags(fs)

	got := map[string]Help{}
	for _, h := range FlagHelp(fs, nil) {
		got[h.Name] = h
	}
	if h := got["--[no-]cache"]; h.Default != "on" || h.Usage != "Replay hits" {
		t.Errorf("--[no-]cache = %+v", h)
	}
	if h := got["--[no-]symbols"]; h.Default != "off" || h.Value != "" || h.Usage != "Include a magus diff --baseline input" {
		t.Errorf("--[no-]symbols = %+v: a backquoted word in a bool's usage is prose, not a value name", h)
	}
	if h := got["--depth"]; h.Value != "deep" || h.Default != "3" {
		t.Errorf("--depth = %+v", h)
	}
	if _, ok := got["-c"]; !ok {
		t.Error("the short alias lost its own line, or lost its single dash")
	}
	for name := range got {
		if strings.HasPrefix(name, "--no-") {
			t.Errorf("%s listed on its own: it folds into --[no-]<name>", name)
		}
	}
}
