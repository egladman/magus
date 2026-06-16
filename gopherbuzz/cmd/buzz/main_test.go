package main

import (
	"reflect"
	"testing"
)

func TestParseArgs(t *testing.T) {
	cases := []struct {
		name string
		argv []string
		want opts
	}{
		{"bare file", []string{"x.buzz"}, opts{args: []string{"x.buzz"}}},
		{"file then script args", []string{"x.buzz", "a", "b"}, opts{args: []string{"x.buzz", "a", "b"}}},
		{"check long", []string{"--check", "x.buzz"}, opts{check: true, args: []string{"x.buzz"}}},
		{"check short", []string{"-c", "x.buzz"}, opts{check: true, args: []string{"x.buzz"}}},
		{"test short", []string{"-t", "x.buzz"}, opts{test: true, args: []string{"x.buzz"}}},
		{"test long", []string{"--test", "x.buzz"}, opts{test: true, args: []string{"x.buzz"}}},
		{"eval", []string{"-e", "code"}, opts{eval: "code"}},
		{"eval equals", []string{"--eval=code"}, opts{eval: "code"}},
		{"ast", []string{"--ast", "x.buzz"}, opts{dumpAST: true, args: []string{"x.buzz"}}},
		{"version", []string{"-v"}, opts{showVer: true}},
		{"help", []string{"--help"}, opts{showHelp: true}},
		{"repeatable -L", []string{"-L", "a", "-L", "b", "x.buzz"}, opts{libDirs: []string{"a", "b"}, args: []string{"x.buzz"}}},
		{"stdin dash", []string{"-"}, opts{args: []string{"-"}}},
		{"options stop at script", []string{"x.buzz", "-c"}, opts{args: []string{"x.buzz", "-c"}}},
		{"double dash ends options", []string{"--", "-c"}, opts{args: []string{"-c"}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := parseArgs(c.argv)
			if err != nil {
				t.Fatalf("parseArgs(%v) error: %v", c.argv, err)
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("parseArgs(%v) = %+v, want %+v", c.argv, got, c.want)
			}
		})
	}
}

func TestParseArgsErrors(t *testing.T) {
	for _, argv := range [][]string{
		{"--bogus"},
		{"-e"},             // missing value
		{"--library-path"}, // missing value
	} {
		if _, err := parseArgs(argv); err == nil {
			t.Errorf("parseArgs(%v): want error, got nil", argv)
		}
	}
}

func TestSourceRejectsEvalWithFile(t *testing.T) {
	if _, _, err := source(opts{eval: "x", args: []string{"f.buzz"}}); err == nil {
		t.Error("source(-e + file): want error")
	}
}
