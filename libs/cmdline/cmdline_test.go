package cmdline

import (
	"flag"
	"fmt"
	"reflect"
	"testing"
	"time"
)

func ExampleWalk() {
	root := Command{
		Name: "tool",
		Children: []Command{
			{Name: "run", Flags: []Flag{{Name: "dry-run", Kind: Bool, Doc: "Print what would run"}}},
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
