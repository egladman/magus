package guard

import (
	"cmp"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/hint"
)

// The rule catalog: what this workspace enforces, as data a reader can list.
//
// Until this existed there was no way to ask the question. A rule announced itself only
// by firing, so the only route to the set was to trip each one, and a verdict naming
// `stage-all` gave a reader a string with nowhere to take it. That is the gap
// docs/doctrine.md calls out under "automation you can interrogate": a verdict magus
// cannot account for is a defect in the same class as a wrong answer.
//
// It is a TABLE rather than a method on each rule because the rules are not objects: they
// are functions returning prose, spread over four files and matched in a switch. A table
// beside the constants is the one shape where a missing entry is visible, and
// TestEveryRuleIsCatalogued is what makes it visible.
//
// Catches is deliberately short and deliberately not the verdict text. The verdict tells
// one caller what to run instead; this tells a reader what the rule is FOR, so a list of
// thirty-eight reads as a set of conventions rather than a wall of remediation.

// RuleDoc is one catalogued rule: its stable name, which tier it lands on, and what it
// fires on.
// The json tags are the -o json and -o template field names, so they follow the
// lowercase convention every other magus output uses rather than the Go spelling.
type RuleDoc struct {
	// Name is the slug a verdict reports and a reader looks up.
	Name string `json:"name"`
	// Decision is the tier: "deny" or "advise". A rule never moves between them without
	// the move being the point of the change, so it is recorded rather than derived.
	Decision string `json:"decision"`
	// Catches says what the rule fires on, in one line, in the reader's terms.
	Catches string `json:"catches"`
}

// denyRuleDocs documents every rule that REFUSES. Ordered by name here only for review;
// Rules sorts what it returns.
var denyRuleDocs = []RuleDoc{
	{string(denyRuleBusyWait), "deny", "a loop polling for work you started, which announces its own completion"},
	{string(denyRuleCacheDirWrite), "deny", "a write into this checkout's magus cache dir, which magus alone owns"},
	{string(denyRuleCaptureFilter), "deny", "a filter over a run capture or log, which cuts the failure block apart"},
	{string(denyRuleCd), "deny", "a `cd` before a magus command, when the project is an argument"},
	{string(denyRuleCIWatch), "deny", "a `gh` invocation that BLOCKS until CI finishes, rather than asking once"},
	{string(denyRuleInterpreterRewrite), "deny", "an inline interpreter rewriting a file this tree already carries"},
	{string(denyRuleMergeSideCheckout), "deny", "a checkout of one merge side over a conflicted file, which discards the merge"},
	{string(denyRuleNotesAuthor), "deny", "an agent authoring a human's note, whose only provenance is who wrote it"},
	{string(denyRuleOutputPipe), "deny", "magus output piped into a filter, when magus projects the record itself"},
	{string(denyRuleOutputRedirect), "deny", "magus output redirected to a file, which the run log already holds"},
	{string(denyRuleProcessPoll), "deny", "a process table inspected to wait on magus work the lock already reports"},
	{string(denyRuleRawTool), "deny", "a toolchain command a spell already wraps, run outside the cache"},
	{string(denyRuleReadAck), "deny", "an agent stamping a read receipt, which records that a PERSON read a change"},
	{string(denyRuleScriptedRewrite), "deny", "a scripted substitute-and-write, which cannot tell your symbol from a dependency's"},
	{string(denyRuleSedInPlace), "deny", "`sed -i`, whose two spellings destroy each other's work across platforms"},
	{string(denyRuleSharedStash), "deny", "a bare stash push or pop, on a stack every worktree shares"},
	{string(denyRuleSiblingCheckout), "deny", "a magus command relocated into another checkout, judging a tree nobody ships"},
	{string(denyRuleStageAll), "deny", "`git add -A`, which sweeps regenerated output into a commit about something else"},
	{string(denyRuleSymbolSearch), "deny", "a recursive text search for a symbol the index defines and can enumerate"},
	{string(denyRuleThrowawayCopy), "deny", "a run inside a temp or scratchpad copy, which leaves the real tree unverified"},
	{string(denyRuleWholeTree), "deny", "a whole-tree VCS reset, checkout, restore or clean, which cannot be undone"},
	{string(denyRuleWorktreeRemove), "deny", "removing a worktree, which may hold another session's uncommitted work"},
}

// advisoryDocs documents every rule that EXPLAINS rather than refuses. Several are
// agent-shaped by construction (a lease, a focus boundary, host wiring): they are
// catalogued anyway, because a reader asking what this workspace enforces is owed the
// whole set rather than the half that happens to apply to them today.
var advisoryDocs = []RuleDoc{
	{string(advisoryCodeSearch), "advise", "a repo-wide text search that the symbol graph may answer better"},
	{string(advisoryDocSearch), "advise", "a search through markdown, where headings are indexed as doc sections"},
	{string(advisoryFocus), "advise", "a read or write outside the paths the running job declared"},
	{string(advisoryGateRepeat), "advise", "the gate run again soon after it passed, repeating work already done"},
	{string(advisoryGraphStale), "advise", "a graph read while the index is older than the sources it describes"},
	{string(advisoryHookWiring), "advise", "a write to the host wiring that decides whether these rules run at all"},
	{string(advisoryLeaseInvalid), "advise", "a call naming a lease this workspace's job store does not declare"},
	{string(advisoryLeaseTerminal), "advise", "a call naming a lease whose row has already finished"},
	{string(advisoryNewFile), "advise", "a new file in a directory whose naming has settled"},
	{string(advisoryPrecedent), "advise", "a hunt for one distinctive name, which refs answers with verified sites"},
	{string(advisoryRegenSource), "advise", "a hand edit to a file a target regenerates"},
	{string(advisorySkillSource), "advise", "a write to an installed skill copy rather than to its source"},
	{string(advisorySourceRead), "advise", "an unbounded source read the symbol index has already answered"},
	{string(advisoryStageClassify), "advise", "staging without classifying, when generated and source differ"},
	{string(advisoryStaleBinary), "advise", "a verdict from a binary older than the rules in the tree around it"},
	{string(advisoryUnleasedWrite), "advise", "a write magus cannot attribute while a fleet is running"},
}

// Rules returns the whole catalog, denies first and each tier sorted by name: the order a
// reader scans, with the tier that blocks them at the top.
func Rules() []RuleDoc {
	out := make([]RuleDoc, 0, len(denyRuleDocs)+len(advisoryDocs))
	out = append(out, denyRuleDocs...)
	out = append(out, advisoryDocs...)
	slices.SortFunc(out, func(a, b RuleDoc) int {
		// Deny sorts before advise, which is neither alphabetical nor accidental: a
		// reader opening this list is asking what stops them first.
		if a.Decision != b.Decision {
			if a.Decision == "deny" {
				return -1
			}
			return 1
		}
		return cmp.Compare(a.Name, b.Name)
	})
	return out
}

// Rule looks one rule up by name, reporting false for a name nobody declares. The
// comparison is exact: a near-miss that resolved would report a different rule's terms
// as this one's.
func Rule(name string) (RuleDoc, bool) {
	name = strings.TrimSpace(name)
	for _, r := range Rules() {
		if r.Name == name {
			return r, true
		}
	}
	return RuleDoc{}, false
}

// advisoryKinds is every enrolled kind, for the test that pairs the catalog against the
// constants. Declared here rather than derived, for the same reason the docs are: a kind
// that nothing lists is a kind nothing can miss.
var advisoryKinds = []hint.MarkerKind{
	advisoryStaleBinary, advisoryCodeSearch, advisoryDocSearch, advisorySourceRead,
	advisoryPrecedent, advisoryStageClassify, advisoryUnleasedWrite, advisorySkillSource,
	advisoryRegenSource, advisoryGraphStale, advisoryGateRepeat, advisoryFocus,
	advisoryHookWiring, advisoryNewFile, advisoryLeaseTerminal, advisoryLeaseInvalid,
}
