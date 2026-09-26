// Command magus-filelint holds this repository's non-Go files to the conventions
// no compiler or off-the-shelf linter checks: lockfile order, workflow trust,
// magusfile footprints, hook wiring, the installed-skill declarations, and the
// landing page's headline rotator. The root project's lint target runs it.
//
// Each finding names the file and line, what is wrong, and what to do. It exits
// 1 when there is any finding.
//
// Usage:
//
//	go run ./cmd/magus-filelint [-root .]
package main

import (
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"
)

// finding is one violation. line is 0 when the rule has no single line to
// point at, such as a file that is missing. run stamps rule.
type finding struct {
	path    string
	line    int
	problem string
	fix     string
	rule    string
}

func (f finding) String() string {
	pos := f.path
	if f.line > 0 {
		pos = fmt.Sprintf("%s:%d", f.path, f.line)
	}
	s := pos + ": " + f.problem
	if f.rule != "" {
		s += " [" + f.rule + "]"
	}
	if f.fix != "" {
		s += "\n    " + strings.ReplaceAll(f.fix, "\n", "\n    ")
	}
	return s
}

// rule checks one convention against a repository tree rooted at fsys.
type rule struct {
	name  string
	check func(fsys fs.FS) []finding
}

var rules = []rule{
	{"lockfiles-are-sorted", lockfilesAreSorted},
	{"trusted-jobs-restore-no-actions-cache", trustedJobsRestoreNoActionsCache},
	{"setup-magus-mints-the-queue-app-token-only-as-an-output", setupMagusMintsTheQueueAppTokenOnlyAsAnOutput},
	{"package-install-targets-never-replay", packageInstallTargetsNeverReplay},
	{"whole-tree-targets-key-on-the-go-tree", wholeTreeTargetsKeyOnTheGoTree},
	{"root-project-declares-the-configs-its-tools-read", rootProjectDeclaresTheConfigsItsToolsRead},
	{"skills-generate-declares-every-shipped-skill", skillsGenerateDeclaresEveryShippedSkill},
	{"generate-drift-throw-names-the-engine-drift-code", generateDriftThrowNamesTheEngineDriftCode},
	{"landing-rotator-slots-match-headline-count", landingRotatorSlotsMatchHeadlineCount},
	{"buzz-glue-is-wired-as-plain-argv", buzzGlueIsWiredAsPlainArgv},
}

// run applies every rule to fsys, writes each finding to w under its rule's
// name, and returns how many it wrote.
func run(fsys fs.FS, w io.Writer) int {
	total := 0
	for _, r := range rules {
		for _, f := range r.check(fsys) {
			f.rule = r.name
			fmt.Fprintln(w, f)
			total++
		}
	}
	return total
}

func main() {
	root := flag.String("root", ".", "repository root to check")
	flag.Parse()

	n := run(os.DirFS(*root), os.Stdout)
	if n > 0 {
		fmt.Fprintf(os.Stderr, "magus-filelint: %d finding(s)\n", n)
		os.Exit(1)
	}
	fmt.Printf("magus-filelint: %d rules, no findings\n", len(rules))
}

// readFile reads path, turning a failure into the finding a rule reports in
// place of its own verdict.
func readFile(fsys fs.FS, path string) (string, []finding) {
	body, err := fs.ReadFile(fsys, path)
	if err != nil {
		return "", []finding{{path: path, problem: "cannot read: " + err.Error(),
			fix: "this rule reads it; restore the file or move the rule with it"}}
	}
	return string(body), nil
}

// lineAt returns the 1-based line holding byte offset off of src.
func lineAt(src string, off int) int {
	return strings.Count(src[:min(max(off, 0), len(src))], "\n") + 1
}

// lineOf returns the line of the first occurrence of needle in src, or 0. A
// needle may lead with newlines to anchor at a line start; the line reported is
// the one its text starts on.
func lineOf(src, needle string) int {
	i := strings.Index(src, needle)
	if i < 0 {
		return 0
	}
	return lineAt(src, i+len(needle)-len(strings.TrimLeft(needle, "\n")))
}
