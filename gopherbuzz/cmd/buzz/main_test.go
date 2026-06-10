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
		{"bare file", []string{"x.bzz"}, opts{args: []string{"x.bzz"}}},
		{"file then script args", []string{"x.bzz", "a", "b"}, opts{args: []string{"x.bzz", "a", "b"}}},
		{"check long", []string{"--check", "x.bzz"}, opts{check: true, args: []string{"x.bzz"}}},
		{"check short", []string{"-c", "x.bzz"}, opts{check: true, args: []string{"x.bzz"}}},
		{"test short", []string{"-t", "x.bzz"}, opts{test: true, args: []string{"x.bzz"}}},
		{"test long", []string{"--test", "x.bzz"}, opts{test: true, args: []string{"x.bzz"}}},
		{"eval", []string{"-e", "code"}, opts{eval: "code"}},
		{"eval equals", []string{"--eval=code"}, opts{eval: "code"}},
		{"ast", []string{"--ast", "x.bzz"}, opts{dumpAST: true, args: []string{"x.bzz"}}},
		{"version", []string{"-v"}, opts{showVer: true}},
		{"help", []string{"--help"}, opts{showHelp: true}},
		{"repeatable -L", []string{"-L", "a", "-L", "b", "x.bzz"}, opts{libDirs: []string{"a", "b"}, args: []string{"x.bzz"}}},
		{"stdin dash", []string{"-"}, opts{args: []string{"-"}}},
		{"options stop at script", []string{"x.bzz", "-c"}, opts{args: []string{"x.bzz", "-c"}}},
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
	if _, _, err := source(opts{eval: "x", args: []string{"f.bzz"}}); err == nil {
		t.Error("source(-e + file): want error")
	}
}
