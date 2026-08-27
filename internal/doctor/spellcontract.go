package doctor

import (
	"fmt"
	"sort"
	"strings"

	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
)

// checkSpellContract reports what each registered spell actually implements of the
// mgs_ contract, and fails on the parts that are REQUIRED.
//
// The optional hooks are genuinely optional and a check that demanded all of them
// would be wrong: the buzz spell declares no version command because there is no
// useful Buzz-toolchain version to probe, and saying so is correct, not a defect.
// So this reports coverage rather than mandating it - a reader can see at a glance
// that a spell claims no language or no probe, and decide whether that is intended.
//
// What it DOES fail on is the one thing no spell can function without: a name.
//
// It deliberately does NOT fail on an empty target list. That looked like an obvious
// defect and is not: the magusfile spell contributes the targets a magusfile itself
// exports, so it declares none statically and is entirely correct. A check is only
// worth having if it is quiet when nothing is wrong, and this one flagged two healthy
// built-ins the first time it ran.
//
// LIMITATION worth stating, because it is the bug this check was asked for and does
// not catch: a spell whose DECLARED hook never reaches the registered spell looks
// identical here to one that never declared it. That happened - workspace-local Buzz
// spells silently lost their version probe, language, and opaque flag because one
// construction path omitted the options - and doctor cannot see it, because doctor
// only ever observes the registered spell, never the descriptor it was built from.
// Catching declaration-versus-application drift needs a test over magus's own
// construction paths, not a workspace diagnostic. See TestSpellOptionsApplied.
func checkSpellContract(spells []*spells.Spell) types.DoctorCheck {
	const name = "spell-contract"
	if len(spells) == 0 {
		return types.DoctorCheck{Name: name, Status: types.DoctorOK, Message: "no spells registered"}
	}

	var broken, details []string
	for _, s := range spells {
		sn := s.Name()
		if sn == "" {
			broken = append(broken, "<unnamed>: mgs_getName returned an empty name")
			continue
		}
		hooks := implementedHooks(s)
		if len(s.Targets()) == 0 {
			// Reported, not failed: see the note above on the magusfile spell.
			hooks = append(hooks, "targets supplied by the magusfile, not the spell")
		}
		details = append(details, sn+": "+strings.Join(hooks, ", "))
	}
	sort.Strings(details)

	if len(broken) > 0 {
		sort.Strings(broken)
		return types.DoctorCheck{
			Name:    name,
			Status:  types.DoctorFail,
			Message: fmt.Sprintf("%d spell(s) do not satisfy the required mgs_ contract", len(broken)),
			Details: broken,
		}
	}
	return types.DoctorCheck{
		Name:    name,
		Status:  types.DoctorOK,
		Message: fmt.Sprintf("%d spell(s) satisfy the required mgs_ contract", len(spells)),
		Details: details,
	}
}

// implementedHooks names the optional contract a spell actually carries, so the
// coverage is legible without a reader cross-referencing spell sources.
func implementedHooks(s *spells.Spell) []string {
	var got []string
	if len(s.Sources()) > 0 {
		got = append(got, "needs")
	}
	if len(s.Outputs()) > 0 {
		got = append(got, "provides")
	}
	if len(s.IgnoreDirs()) > 0 {
		got = append(got, "ignore-dirs")
	}
	if s.HasVersionProbe() {
		got = append(got, "version-probe")
	}
	if s.Language() != "" {
		got = append(got, "language")
	}
	if s.Opaque() {
		got = append(got, "opaque")
	}
	if len(got) == 0 {
		return []string{"targets only (no optional hooks)"}
	}
	return got
}
