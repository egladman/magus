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
)

// ReleaseCommand is the shell line that installs a release for a workspace declaring
// constraint as its required_version, "" for none. It names ward.PinnedRelease, so the
// guard and MGS1021 name the same release: a range bounded from above may refuse the
// newest, and self update does not read magus.yaml.
func ReleaseCommand(constraint string) string {
	argv := []string{"magus", "self", "update"}
	if pin := ward.PinnedRelease(constraint); pin != "" {
		argv = append(argv, "--version", pin)
	}
	return ShellLine(argv)
}

// SourceCommand is the shell line that rebuilds a binary built from checkout's own
// sources and older than them: link pkg into output, or, when bootstrap is set, run
// ward.BootstrapCommand in checkout, the module's own build for a checkout of magus.
func SourceCommand(checkout, pkg, output string, bootstrap bool) string {
	if bootstrap {
		return ShellLine(bootstrapIn(checkout))
	}
	return ShellLine([]string{"go", "-C", checkout, "build", "-o", output, "./" + pkg})
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
