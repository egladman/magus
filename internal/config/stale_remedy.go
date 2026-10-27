package config

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
	"mvdan.cc/sh/v3/syntax"

	"github.com/egladman/magus/internal/ward"
	"github.com/egladman/magus/types"
)

// staleCase is why a magus cannot load the workspace it runs in, as far as it decides the fix.
type staleCase int

const (
	// staleRelease is an installed release older than the workspace: install another.
	staleRelease staleCase = iota + 1
	// staleOwnSource is a binary built from the checkout it sits in, older than its sources:
	// rebuild it from them, without loading the tree it cannot load.
	staleOwnSource
)

// staleRemedy is the fix for a binary that cannot load its workspace, as data. Every report
// of a stale binary computes one and renders it where it prints, so the guard, MGS1021 and
// doctor cannot word the same fix three ways.
type staleRemedy struct {
	kind staleCase
	// Version is the release a staleRelease installs, "" for the newest: ward.PinnedRelease,
	// so the guard names the same release as MGS1021. A required_version bounded from above
	// may refuse the newest, and self update does not read magus.yaml.
	Version string
	// Checkout, Package and Output are a staleOwnSource's link: `go -C Checkout build -o
	// Output ./Package`. Bootstrap replaces the link with ward.BootstrapCommand, the
	// module's own build, for a checkout of magus itself.
	Checkout, Package, Output string
	Bootstrap                 bool
	// Scripts are what the workspace declares for the situation, served after the fix
	// because each runs on the binary the fix produces.
	Scripts []types.NextStep
}

// ReleaseRemedy is the release fix for a workspace declaring constraint as its
// required_version, "" for none.
func ReleaseRemedy(constraint string) staleRemedy {
	return staleRemedy{kind: staleRelease, Version: ward.PinnedRelease(constraint)}
}

// SourceRemedy is the fix for a binary built from checkout's own sources and older than
// them: link pkg into output, or run the module's own bootstrap when bootstrap is set.
func SourceRemedy(checkout, pkg, output string, bootstrap bool) staleRemedy {
	return staleRemedy{kind: staleOwnSource, Checkout: checkout, Package: pkg, Output: output, Bootstrap: bootstrap}
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

// Steps are the fix's commands as argv, in the order they run.
func (r staleRemedy) Steps() [][]string {
	switch r.kind {
	case staleRelease:
		if r.Version != "" {
			return [][]string{{"magus", "self", "update", "--version", r.Version}}
		}
		return [][]string{{"magus", "self", "update"}}
	case staleOwnSource:
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
func (r staleRemedy) Lines() []string {
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
			quoted[i] = fmt.Sprintf("%s=%s", name, shellWord(value))
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
