package magus_test

// magus is agent-host agnostic, and this test is the only thing that enforces it.
// The rule was written down twice - in docs/guides/integrations/agents.md ("magus
// owns the guard rules and the verdict, not integration code for each host") and
// in the skill-authoring skill ("no host name appears in code") - and honored
// nowhere mechanically, which is how `magus mcp` came to print Codex and Claude
// Desktop setup instructions. A change to any one of those clients then meant a
// magus release.
//
// The rule, precisely: a host's NAME may appear only as part of a filesystem
// path, because naming the directory a host discovers skills in is the one
// host-specific step magus is allowed to know about (agents.md says so). Anywhere
// else - prose, help text, printed setup instructions, a per-host branch - the
// host-specific part belongs in documentation the reader owns.

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// hostNames are agent hosts magus must not encode behavior for. Deliberately
// omits "cursor": magus uses it as an ordinary pagination term, so matching it
// would be noise rather than signal.
var hostNames = regexp.MustCompile(`(?i)\b(claude|opencode|codex|aider|windsurf)\b`)

// hostPathUse allows a host name that names something ON DISK: a path
// (`.claude/skills`, `~/.config/opencode/skills`, `.codex/config.toml`) or a bare
// quoted filename stem (`case "agents", "claude":` classifying AGENTS.md and
// CLAUDE.md). Recognizing a well-known file is the sanctioned exception - it is a
// destination, not a code path branching on which host is running.
var hostPathUse = regexp.MustCompile(`(?i)([./~][a-z0-9_.-]*\b(claude|opencode|codex|aider|windsurf)\b[a-z0-9_.-]*)|("(claude|opencode|codex|aider|windsurf)")`)

// hostAgnosticSkipDirs are trees this rule does not govern: generated output,
// vendored/third-party code, and the embedded skill bodies (which are
// documentation, and already ASCII- and drift-checked elsewhere).
var hostAgnosticSkipDirs = map[string]bool{
	".git": true, ".magus": true, ".claude": true, ".agents": true, ".opencode": true,
	"node_modules": true, "gen": true, "testdata": true, "docs": true, "blog": true,
	"skills": true, "releases": true, "manpage": true, "schema": true,
}

func TestNoHostSpecificBehaviorInCode(t *testing.T) {
	var violations []string

	err := filepath.WalkDir(".", func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if hostAgnosticSkipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		// Tests may name a host: a test asserting the guard's behavior against a
		// real host event is describing the world, not encoding a code path.
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer func() { _ = f.Close() }()

		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for line := 1; sc.Scan(); line++ {
			text := sc.Text()
			if !hostNames.MatchString(text) {
				continue
			}
			// Strip every path-shaped use, then re-test: a line may legitimately
			// carry both (an example destination plus surrounding prose).
			if !hostNames.MatchString(hostPathUse.ReplaceAllString(text, "")) {
				continue
			}
			violations = append(violations, fmt.Sprintf("%s:%d: %s", path, line, strings.TrimSpace(text)))
		}
		return sc.Err()
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}

	assert.Empty(t, violations,
		"magus must not encode agent-host specifics.\n"+
			"A host name is allowed only inside a filesystem path (e.g. .claude/skills), because naming\n"+
			"the directory a host reads is the one host-specific step magus owns. Everything else - setup\n"+
			"instructions, help text, a per-host branch - belongs in docs the reader owns, or the next\n"+
			"change to that host becomes a magus release.\n\nviolations:\n%s",
		strings.Join(violations, "\n"))
}
