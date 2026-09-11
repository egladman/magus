package main

import (
	"fmt"
	"path"
	"strings"

	"github.com/egladman/magus/internal/hint"
)

// The guard's own installation.
//
// Every rule in this package is enforced by a hook the HOST runs, wired up by an ordinary
// file in the repository: an agent that edits it disarms every other rule here from the
// host's next session on, and a disarmed guard looks identical to a clean session from the
// inside. So it is the one write a declared boundary cannot make legitimate.

// hookWiringFiles are the paths a documented host reads its guard wiring from, taken from
// the per-host guide pages under docs/guides/integrations/agents/ rather than guessed.
//
// The entries name PATHS and the descriptions name none of the hosts, which is the rule
// TestNoHostSpecificBehaviorInCode enforces and also the honest division: a reader looking
// at a verdict about their own config file knows which host it belongs to, and a magus
// that enumerated host names in prose would need a release per host.
//
// Matched as a repository-relative prefix or an exact name, and also as a suffix, because
// three of the four documented hosts read the same file out of the user's home directory
// and the edit a worker makes there lands outside the workspace entirely.
//
// The SKILL directories are deliberately absent. An installed skill is generated and
// regraded on every `magus doctor`, which is a different failure with a rule of its own
// (adviseInstalledSkillWrite); these files are the ones nothing checks afterwards.
var hookWiringFiles = []struct {
	match string
	what  string
}{
	{".claude/settings.json", "a host's hook wiring"},
	{".claude/settings.local.json", "a host's per-checkout hook wiring"},
	{".claude/hooks/", "a guard script a host's hooks run"},
	{".cursor/hooks.json", "a host's hook wiring"},
	{".cursor/hooks/", "a guard script a host's hooks run"},
	{".codex/hooks.json", "a host's hook wiring"},
	{".opencode/plugins/", "a host plugin carrying both guard surfaces"},
	{".config/opencode/plugins/", "a host plugin carrying both guard surfaces"},
}

// hookWiringSubject names what a path IS when it is host guard wiring, or "" otherwise.
//
// Slash-separated and compared against the tail of the path, so an absolute path from a
// host's editor tool, a workspace-relative one, and a home-directory one all read the
// same. A directory entry matches everything beneath it; a file entry matches exactly.
func hookWiringSubject(writePath string) string {
	clean := path.Clean(strings.ReplaceAll(strings.TrimSpace(writePath), "\\", "/"))
	if clean == "" || clean == "." {
		return ""
	}
	for _, w := range hookWiringFiles {
		if strings.HasSuffix(w.match, "/") {
			if strings.Contains(clean+"/", "/"+w.match) || strings.HasPrefix(clean, w.match) {
				return w.what
			}
			continue
		}
		if clean == w.match || strings.HasSuffix(clean, "/"+w.match) {
			return w.what
		}
	}
	return ""
}

// gradeHookWiringWrite judges a write to the guard's own installation: a deny under any
// bound lease, and a once-per-session advisory for everybody else.
//
// The asymmetry is the whole rule. A bound lease was handed a scope by somebody else, and
// rewiring the host is outside every scope anybody hands out. An unbound session is the
// orchestrator, or a person in their own checkout, and both of them legitimately edit
// these files: what they are owed is the sentence saying which file this is, since the
// consequence of getting it wrong is invisible rather than loud.
//
// It says nothing at all about any other path, like every rule here.
func gradeHookWiringWrite(actingLease, writePath string) writeGrade {
	what := hookWiringSubject(writePath)
	if what == "" {
		return writeGrade{}
	}
	if actingLease != "" {
		return writeGrade{Decision: "deny", Reason: fmt.Sprintf(
			"magus workspace: leave the host's wiring alone. "+leaseActorClause("rewire a host")+"\n"+
				"%s is %s: it is the guard's own installation, so an edit here decides whether every rule you are being graded by runs at all from the host's next session on. Lease %s is bound to this checkout, and no lane anybody hands out includes that switch.",
			writePath, what, actingLease)}
	}
	return writeGrade{Decision: "advise", Kind: advisoryHookWiring, Context: fmt.Sprintf(
		"magus workspace: keep the guard armed while you edit this, and re-read it afterwards: `"+hint.Doctor.String()+"` grades the wiring and the binary it resolves.\n"+
			"%s is %s. It takes effect at the host's next session start, and a wiring that stopped working is silent: a disarmed guard and a clean session produce the same output. This is an advisory because you are not acting under a lease, and rewiring a host is an unbound session's job.",
		writePath, what)}
}
