package e2e

import (
	"fmt"
	"os/exec"
	"strings"
	"time"
)

const versionTimeout = 30 * time.Second

// Binary is what a search found out about a program on this machine.
type Binary struct {
	Name    string
	Path    string
	Version string
	// Note says where Path came from and what was passed over, or why
	// nothing was found.
	Note string
}

// Found reports whether the search ended at a program that answered
// --version.
func (b Binary) Found() bool { return b.Path != "" }

// Candidate is a place a program might be, and the name a note gives
// for that place.
type Candidate struct {
	Path   string
	Source string
}

// LocateBinary tries candidates in order and then PATH, keeping the
// first that answers --version under env. An empty candidate is passed
// over, so an unset variable can be one. It never installs anything.
func LocateBinary(name string, env []string, candidates ...Candidate) Binary {
	if p, err := exec.LookPath(name); err == nil {
		candidates = append(candidates[:len(candidates):len(candidates)], Candidate{Path: p, Source: "PATH"})
	}
	b := Binary{Name: name}
	var tried []string
	for _, c := range candidates {
		if c.Path == "" {
			continue
		}
		version, err := BinaryVersion(c.Path, env)
		if err != nil {
			tried = append(tried, fmt.Sprintf("%s (%s): %v", c.Path, c.Source, err))
			continue
		}
		b.Path, b.Version = c.Path, version
		b.Note = "from " + c.Source
		if len(tried) > 0 {
			b.Note += "; passed over " + strings.Join(tried, "; ")
		}
		return b
	}
	b.Note = "absent: " + strings.Join(append(tried, name+" not on PATH"), "; ")
	return b
}

// BinaryVersion runs path --version under env. It returns the output,
// or an error carrying the output's first line.
func BinaryVersion(path string, env []string) (string, error) {
	r := Command{Path: path, Args: []string{"--version"}, Env: env, Timeout: versionTimeout}.Run()
	output := strings.TrimSpace(strings.Join([]string{r.Stdout, r.Stderr}, "\n"))
	if r.Err != nil {
		first, _, _ := strings.Cut(output, "\n")
		return "", fmt.Errorf("%w: %s", r.Err, first)
	}
	return output, nil
}
