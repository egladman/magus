package agent

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// sessionAdapterPrefix names a session-load adapter. Registration is by NAME rather
// than by a hand-kept second list alone, so a fourth host's adapter dropped into
// the directory is claimed by the gates below the moment it exists.
const sessionAdapterPrefix = "magus-session-load-"

// sessionAdapters are the per-host extraction adapters. Every one of them must also
// appear in hookTemplates, which is what gets it embedded and version-stamped;
// this list is what the coverage and parity gates iterate.
var sessionAdapters = []string{
	"magus-session-load-claude-code.sh",
	"magus-session-load-codex.sh",
	"magus-session-load-opencode.sh",
}

// sessionGuideDoc is the page that embeds the adapters and carries the parity
// table they are checked against.
const sessionGuideDoc = "docs/guides/integrations/agents/session-load.md"

// TestEverySessionAdapterIsRegistered gives the session adapters the property the
// guard templates already have: a fourth host arrives and nothing stays green by
// accident.
//
// Two directions, because either one alone leaves a hole. An adapter in the
// directory that no list names answers to no gate; an adapter listed here but
// absent from hookTemplates is neither version-stamped nor embedded in its page,
// so a reader browsing the docs site never sees it.
func TestEverySessionAdapterIsRegistered(t *testing.T) {
	registered := make(map[string]bool, len(sessionAdapters))
	for _, name := range sessionAdapters {
		registered[name] = true
		assert.Contains(t, hookTemplates, name,
			"%s is a session adapter but is not in hookTemplates, so it carries no version marker\n"+
				"and the guide is not required to embed it.", name)
	}

	entries, err := os.ReadDir(hookTemplateDir)
	require.NoError(t, err, "read %s", hookTemplateDir)
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasPrefix(name, sessionAdapterPrefix) {
			continue
		}
		assert.True(t, registered[name],
			"%s ships %s, but sessionAdapters does not list it, so the parity gates skip it:\n"+
				"no coverage declaration is demanded of it and the table in %s owes it no row.",
			hookTemplateDir, name, sessionGuideDoc)
	}
}

// sessionCoverage is host -> dimension -> stance.
type sessionCoverage map[string]map[string]string

// parseSessionCoverage reads every session-coverage declaration out of the
// adapters, and is the sibling of parseGuardCoverage: same shape, different
// contract. An adapter that names a dimension the contract does not have, or omits
// one it does, fails here rather than in a report that quietly reads the gap as
// a measured zero.
func parseSessionCoverage(t *testing.T) sessionCoverage {
	t.Helper()
	stances := map[string]bool{}
	for _, s := range SessionStances() {
		stances[s] = true
	}

	cov := sessionCoverage{}
	for _, name := range sessionAdapters {
		body, err := os.ReadFile(filepath.Join(hookTemplateDir, name))
		require.NoError(t, err, "read %s", name)

		found := false
		for _, line := range strings.Split(string(body), "\n") {
			_, decl, ok := strings.Cut(line, SessionCoverageMarker)
			if !ok {
				continue
			}
			found = true
			fields := map[string]string{}
			for _, kv := range strings.Fields(decl) {
				key, value, split := strings.Cut(kv, "=")
				require.True(t, split, "%s: coverage declaration field %q is not key=value", name, kv)
				fields[key] = value
			}
			require.Equal(t, strconv.Itoa(SessionSchemaVersion), fields["schema"],
				"%s declares session schema %q; the contract is agent.SessionSchemaVersion=%d.\n"+
					"A schema bump means every adapter must be updated and re-downloaded before it loads again.",
				name, fields["schema"], SessionSchemaVersion)
			host := fields["host"]
			require.NotEmpty(t, host, "%s: coverage declaration names no host", name)
			require.Nil(t, cov[host],
				"host %q has two session-coverage declarations; one adapter per host, or the gate cannot tell which is true", host)

			declared := map[string]string{}
			for _, dimension := range SessionDimensions() {
				stance, ok := fields[dimension]
				require.True(t, ok,
					"%s declares session coverage for host %q but says nothing about %q.\n"+
						"Every dimension in agent.SessionDimensions needs an explicit stance (yes or none):\n"+
						"an undeclared dimension is one nobody asked about, and a report reads that as a zero it never measured.",
					name, host, dimension)
				require.True(t, stances[stance], "%s: unknown stance %q for %q (want yes or none)", name, stance, dimension)
				declared[dimension] = stance
			}
			cov[host] = declared
		}
		assert.True(t, found, "%s carries no %s line, so nothing states what its host can supply", name, SessionCoverageMarker)
	}
	require.NotEmpty(t, cov, "no %s declarations found in any session adapter", SessionCoverageMarker)
	return cov
}

// TestSessionAdaptersDeclareTheContract holds every adapter's coverage line to the
// session contract: its schema, and a stance for every dimension. The table a reader
// judges a report by is compared with these lines in the docs project's conventions
// target (docs/lib/crosscheck.buzz), which can only compare what this makes complete.
func TestSessionAdaptersDeclareTheContract(t *testing.T) {
	assert.Len(t, parseSessionCoverage(t), len(sessionAdapters), "one declaration per adapter")
}

// sessionExitArmRe matches an adapter giving up: a bare `exit 0` inside a
// conditional block, which is how every one of these arms ends.
var sessionExitArmRe = regexp.MustCompile(`^exit 0$`)

// TestSessionAdapterFailOpenArmsAnnounceThemselves is the session half of the
// doctrine's enforcement point, and it exists for the same reason its guard
// sibling does: an adapter that extracted nothing looks exactly like a host nobody
// used, and that reading is the one the audit must never invite.
//
// Structural, so rewording a message costs nothing and DELETING one fails.
func TestSessionAdapterFailOpenArmsAnnounceThemselves(t *testing.T) {
	for _, name := range sessionAdapters {
		body, err := os.ReadFile(filepath.Join(hookTemplateDir, name))
		require.NoError(t, err, "read %s", name)

		lines := strings.Split(string(body), "\n")
		arms := 0
		for i, line := range lines {
			if !strings.HasPrefix(strings.TrimSpace(line), "if ") {
				continue
			}
			block := lines[i:failOpenArmEnd(lines, i)]
			if !sessionArmGivesUp(block) {
				continue
			}
			arms++
			assert.True(t, strings.Contains(strings.Join(block, "\n"), ">&2"),
				"%s stops extracting at line %d and says nothing.\n"+
					"An adapter that loaded no events looks exactly like a host nobody used, so every arm that\n"+
					"gives up announces why on stderr.", name, i+1)
		}
		assert.NotZero(t, arms,
			"%s has no arm that gives up, so either it now fails some other way - update sessionExitArmRe -\n"+
				"or it aborts on a missing tool, which is a change this gate should have been told about.", name)
	}
}

// sessionArmGivesUp reports whether a conditional block ends the run rather than
// skipping one input.
func sessionArmGivesUp(block []string) bool {
	for _, l := range block {
		if sessionExitArmRe.MatchString(strings.TrimSpace(l)) {
			return true
		}
	}
	return false
}
