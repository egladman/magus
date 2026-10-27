package config

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
	"mvdan.cc/sh/v3/syntax"

	"github.com/egladman/magus/internal/ward"
	"github.com/egladman/magus/types"
)

// StaleCase is why a magus cannot load the workspace it runs in, as far as it decides the fix.
type StaleCase int

const (
	// StaleRelease is an installed release older than the workspace: install another.
	StaleRelease StaleCase = iota + 1
	// StaleSkew is a daemon or hook binary that differs from the checkout's own: restart it.
	StaleSkew
	// StaleOwnSource is a binary built from the checkout it sits in, older than its sources:
	// rebuild it from them, without loading the tree it cannot load.
	StaleOwnSource
)

// StaleRemedy is the fix for a binary that cannot load its workspace, as data. Every report
// of a stale binary computes one and renders it where it prints, so the guard, MGS1021 and
// doctor cannot word the same fix three ways.
type StaleRemedy struct {
	Case StaleCase
	// Version is the release a StaleRelease installs, "" for the newest. A workspace whose
	// required_version pins one names it: the newest may be one it refuses, and self update
	// does not read magus.yaml.
	Version string
	// Checkout, Package and Output are a StaleOwnSource's link: `go -C Checkout build -o
	// Output ./Package`. Bootstrap replaces the link with ward.BootstrapCommand, the
	// module's own build, for a checkout of magus itself.
	Checkout, Package, Output string
	Bootstrap                 bool
	// Scripts are what the workspace declares for the situation, served after the fix
	// because each runs on the binary the fix produces.
	Scripts []types.NextStep
}

// ReleaseRemedy is the StaleRelease fix for a workspace declaring constraint as its
// required_version, "" for none.
func ReleaseRemedy(constraint string) StaleRemedy {
	return StaleRemedy{Case: StaleRelease, Version: PinnedVersion(constraint)}
}

// RequiredVersionAt is the required_version root's magus.yaml declares, read leniently: a
// binary too old to decode the rest of the file still reads its floor. "" for none.
func RequiredVersionAt(root string) string {
	for _, name := range []string{Filename, DottedFilename} {
		if data, err := os.ReadFile(filepath.Join(root, name)); err == nil {
			return requiredVersion(data)
		}
	}
	return ""
}

func requiredVersion(data []byte) string {
	var head struct {
		RequiredVersion string `yaml:"required_version"`
	}
	if yaml.Unmarshal(data, &head) != nil {
		return ""
	}
	return head.RequiredVersion
}

var exactPin =regexp.MustCompile(`^\s*=?=?\s*v?(\d+\.\d+\.\d+(?:-[0-9A-Za-z.]+)?)\s*$`)

// PinnedVersion is the one release constraint admits, "vX.Y.Z", or "" when it admits a
// range or is empty.
func PinnedVersion(constraint string) string {
	m := exactPin.FindStringSubmatch(constraint)
	if m == nil {
		return ""
	}
	return "v" + m[1]
}

// Steps are the fix's commands as argv, in the order they run.
func (r StaleRemedy) Steps() [][]string {
	switch r.Case {
	case StaleRelease:
		if r.Version != "" {
			return [][]string{{"magus", "self", "update", "--version", r.Version}}
		}
		return [][]string{{"magus", "self", "update"}}
	case StaleSkew:
		return [][]string{{"magus", "server", "stop"}, {"magus", "server", "start"}}
	case StaleOwnSource:
		if r.Bootstrap {
			return [][]string{bootstrapIn(r.Checkout)}
		}
		return [][]string{{"go", "-C", r.Checkout, "build", "-o", r.Output, "./" + r.Package}}
	}
	return nil
}

// bootstrapIn is ward.BootstrapCommand with -C naming dir, so it builds that checkout from
// whatever directory the host runs it in.
func bootstrapIn(dir string) []string {
	words := strings.Fields(ward.BootstrapCommand)
	at := slices.Index(words, "go")
	if at < 0 {
		return words
	}
	return slices.Concat(words[:at+1], []string{"-C", dir}, words[at+1:])
}

// Lines are Steps quoted for a POSIX shell, one command per line.
func (r StaleRemedy) Lines() []string {
	steps := r.Steps()
	lines := make([]string, len(steps))
	for i, argv := range steps {
		lines[i] = ShellLine(argv)
	}
	return lines
}

// ShellLine joins argv into one line a POSIX shell splits back into argv. A leading
// NAME=value word that needs no quoting stays an assignment.
func ShellLine(argv []string) string {
	quoted := make([]string, len(argv))
	for i, a := range argv {
		name, value, assigns := strings.Cut(a, "=")
		if i == 0 && assigns && syntax.ValidName(name) {
			quoted[i] = name + "=" + shellWord(value)
			continue
		}
		quoted[i] = shellWord(a)
	}
	return strings.Join(quoted, " ")
}

func shellWord(s string) string {
	q, err := syntax.Quote(s, syntax.LangPOSIX)
	if err != nil {
		// Only a NUL byte fails, and no path, flag or version holds one.
		return strconv.Quote(s)
	}
	return q
}
