package guard

import (
	"fmt"
	"path"
	"path/filepath"
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
// the hostagnostic linter enforces and also the honest division: a reader looking
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
	// root matches the path only at the workspace root, where no other checkout or home
	// directory holds the same name.
	root bool
}{
	{match: "magus", what: "the guard binary every hook runs", root: true},
	{match: ".claude/settings.json", what: "a host's hook wiring"},
	{match: ".claude/settings.local.json", what: "a host's per-checkout hook wiring"},
	{match: ".claude/hooks/", what: "a guard script a host's hooks run"},
	{match: ".cursor/hooks.json", what: "a host's hook wiring"},
	{match: ".cursor/hooks/", what: "a guard script a host's hooks run"},
	{match: ".codex/hooks.json", what: "a host's hook wiring"},
	{match: ".opencode/plugins/", what: "a host plugin carrying both the file-write and shell-command guards"},
	{match: ".config/opencode/plugins/", what: "a host plugin carrying both the file-write and shell-command guards"},
}

// hookWiringSubject names what a path IS when it is host guard wiring, or "" otherwise. A
// root entry reads a relative path against workspace and matches nothing without one.
//
// Slash-separated and compared against the tail of the path, so an absolute path from a
// host's editor tool, a workspace-relative one, and a home-directory one all read the
// same. A directory entry matches everything beneath it; a file entry matches exactly.
func hookWiringSubject(workspace, writePath string) string {
	clean := path.Clean(strings.ReplaceAll(strings.TrimSpace(writePath), "\\", "/"))
	if clean == "" || clean == "." {
		return ""
	}
	for _, w := range hookWiringFiles {
		if w.root {
			if workspace != "" && atWorkspaceRoot(workspace, writePath, w.match) {
				return w.what
			}
			continue
		}
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

// atWorkspaceRoot reports writePath as name directly under workspace.
func atWorkspaceRoot(workspace, writePath, name string) bool {
	if !filepath.IsAbs(writePath) {
		writePath = filepath.Join(workspace, writePath)
	}
	return filepath.Clean(writePath) == filepath.Join(workspace, name)
}

// gradeHookWiringWrite judges a write to the guard's own installation: a deny under any
// bound lease, a deny for any agent-attributed session even without one, and a
// once-per-session advisory for a person's own unattributed session.
//
// The asymmetry is the whole rule, and it is drawn on ATTRIBUTION, not on binding. A bound
// lease was handed a scope by somebody else, and rewiring the host is outside every scope
// anybody hands out; an unleased subagent was handed no scope at all, and "nobody told me
// not to" is not consent. Only a session the host attributes to no agent legitimately
// edits these files: a person in their own checkout, or the orchestrator relaying for one.
// What it is owed is the sentence saying which file this is, since the consequence of
// getting it wrong is invisible rather than loud.
//
// It says nothing at all about any other path, like every rule here.
func gradeHookWiringWrite(workspace, actingLease string, agentAttributed bool, writePath string) writeGrade {
	what := hookWiringSubject(workspace, writePath)
	if what == "" {
		return writeGrade{}
	}
	say := fmt.Sprintf("magus workspace: %s is %s, the guard's own installation; "+leaseActorClause("rewire a host"), writePath, what)
	switch {
	case actingLease != "":
		return writeGrade{Decision: decisionDeny, Reason: denial{
			Say: say,
			Why: fmt.Sprintf("An edit here decides whether every rule you are being graded by runs at all from the host's next session on. Lease %s is bound to this checkout, and no write paths anybody hands out include that switch.\n"+
				leaseActorWhy,
				actingLease),
		}.full()}
	case agentAttributed:
		return writeGrade{Decision: decisionDeny, Reason: denial{
			Say: say,
			Why: "An edit here decides whether every rule you are being graded by runs at all from the host's next session on. The host attributes this call to an agent, and no scope anybody hands out includes that switch, leased or not.\n" +
				"Have a person make this edit instead. If you need the wiring itself, `magus agent harness verify` reports a descriptor's current state without writing anything: magus prints host config for a person to merge, it does not author it.\n" +
				leaseActorWhy,
		}.full()}
	default:
		return writeGrade{Decision: decisionAdvise, Kind: advisoryHookWiring,
			Context: fmt.Sprintf("magus workspace: %s is %s; keep the guard armed and re-read it afterwards with `"+hint.Doctor.String()+"`.", writePath, what),
			Why: "The edit takes effect at the host's next session start, and a wiring that stopped working is silent: a disarmed guard and a clean session produce the same output. " +
				"`" + hint.Doctor.String() + "` grades the wiring and the binary it resolves. This is an advisory because the host attributes this call to no agent: a person's own session, or the orchestrator relaying for one."}
	}
}
