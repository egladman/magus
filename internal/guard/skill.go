package guard

import (
	"strings"

	"github.com/egladman/magus/internal/agent"
	"github.com/egladman/magus/internal/hint"
)

// Rules that require a skill to have been READ before an act magus cannot correct
// afterwards. The mechanism is shared; what each rule chooses is which act and which skill.
//
// The case for the shape, measured rather than assumed: across 2,147 session transcripts
// the only skills that ever loaded were the ones a hook demanded. A skill reachable by
// description alone sat at zero, however well the description was written. So a convention
// worth keeping gets an enforcement point, which is the conclusion internal/guard/dir.go
// reached and the one this workspace's own agent instructions already record.
//
// The cost is real and bounds how many of these there should be: each one spends a
// session's first action on reading, and a deny common enough to be routine is one people
// learn to route around. Two exist. A third needs evidence of the same kind.

// skillMarker keys a session marker on a skill NAME, so the guard can ask "has this session
// loaded that" without reading the host's transcript.
//
// A marker rather than a lookup in the host's session store: the store is per host, and the
// guard is forbidden from branching on host vocabulary. A marker is magus's own file,
// written by the same gate every advisory uses, and the host's only contribution is telling
// magus that a skill was loaded at all.
func skillMarker(reported string) hint.MarkerKind {
	name := skillNameFromHost(reported)
	if name == "" {
		return ""
	}
	return hint.MarkerKind("skill-" + name)
}

// isSkillName reports whether name is shaped like a skill magus could ship.
//
// A MarkerKind is a FILENAME COMPONENT (hint.MarkerKind says so, and hint.MarkerPath
// interpolates it into filepath.Join without hashing it), and every other kind in the
// tree is a compile-time constant. This one is not: it arrives as a skill name in the
// host's own envelope, so it is the one place a caller outside magus chooses part of a
// path magus then creates directories under, exclusively creates a file at, and sweeps
// with os.Remove. `../` in that name reaches all three outside the advisories directory.
//
// Rejecting beats sanitizing. A name outside this charset cannot name a skill that
// exists: magus's own are lowercase words joined by dashes, and agent.MustSkill resolves
// against the shipped catalog at init. So refusing to record one loses nothing real, and
// the gate it feeds stays denied, which is the correct answer for a skill that did not
// load. Mapping bad characters onto good ones would instead mint a marker that CLEARS a
// gate on input nobody can account for.
func isSkillName(name string) bool {
	if name == "" || len(name) > 128 {
		return false
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
		default:
			return false
		}
	}
	return true
}

// skillNameFromHost reduces what a host reported to the bare skill name, or "" when
// nothing in it can be one.
//
// Hosts namespace. A plugin-installed skill arrives as `some-plugin:magus-buzz-write`,
// and a strict charset alone would reject it, record nothing, and leave the gate denied
// with the reader doing exactly what the message asked. So the last segment is what gets
// matched: it is the skill's own name in every namespaced form, and it still has to pass
// isSkillName, which is what keeps a traversing segment out of the path.
//
// Splitting on ":" and not on "/": a slash is the separator this is defending against,
// and accepting it here would hand back the traversal by the front door.
func skillNameFromHost(reported string) string {
	name := reported
	if i := strings.LastIndex(name, ":"); i >= 0 {
		name = name[i+1:]
	}
	if !isSkillName(name) {
		return ""
	}
	return name
}

// recordSkillLoad marks a skill as loaded for this session, and reports whether the session
// now carries that mark.
//
// NOT MarkFired's own return, which answers "was it already there": a second load of the
// same skill is an ordinary thing for a session to do, and reporting false for it would
// read as a failure to record.
func recordSkillLoad(markers hint.Gate, name string) bool {
	if name == "" || markers.Session() == "" {
		return false
	}
	markers.MarkFired(skillMarker(name))
	return markers.AlreadyFired(skillMarker(name))
}

// skillLoaded reports whether this session has read the named skill, under either the
// primary name or its full twin.
//
// Either copy counts as evidence. The twins carry the same rules and differ only in how
// much of the worked example they keep, so a session that loaded the fuller one has read
// the brief; holding out for the exact name would deny a reader who did more work, not
// less.
func skillLoaded(markers hint.Gate, skill agent.SkillRef) bool {
	return markers.AlreadyFired(skillMarker(skill.String())) ||
		markers.AlreadyFired(skillMarker(agent.FullTwinName(skill.String())))
}

// denyUntilSkillLoaded is the shared verdict body: act is what the caller was about to do,
// carries says what the skill holds that the act needs, and skill is what to load.
//
// observesSkillLoads is the difference between a rule and a trap, and it is the caller's to
// pass rather than something this can detect. Only a host whose wiring reports skill loads
// can ever satisfy one of these rules, and the four harnesses magus ships do not agree: one
// matches a skill tool, one wires four other matchers, one uses its own event names, and
// one wires no hook config at all. On the three that report nothing, a rule that denied
// anyway would deny for the life of the session with no action the reader could take.
//
// Three lines, like every other deny here: what happened, why it matters, what to do.
func denyUntilSkillLoaded(markers hint.Gate, observesSkillLoads bool, skill agent.SkillRef, act, carries string) string {
	if !observesSkillLoads {
		return ""
	}
	// No session pointer means no marker can be keyed, so every call in every session would
	// share one bucket: the first denies and the rest pass, which is worse than not asking.
	if markers.Session() == "" {
		return ""
	}
	// No cache dir means no marker can be WRITTEN. hint.Gate refuses to write one and
	// reports every kind unfired, so the load a reader performs records nothing and the
	// next attempt is denied in the same three lines. hookLocationAt returns an empty
	// location whenever the envelope's cwd sits outside a workspace or the cache dir will
	// not resolve, so this is reachable rather than theoretical.
	//
	// It is the same trap observesSkillLoads exists to avoid, arriving by another road: a
	// deny the reader cannot clear by doing what it asks. Standing down is the only honest
	// answer in both.
	if markers.CacheDir() == "" {
		return ""
	}
	if skillLoaded(markers, skill) {
		return ""
	}
	// The SHORT name, always. The full twin is not installed everywhere (one shipped harness
	// asks for the short form alone), and naming a skill the reader cannot load is the
	// failure agent.MustSkill exists to prevent.
	return act + " before the " + skill.String() + " skill loaded.\n" +
		carries + "\n" +
		"Load Skill(" + skill.String() + "), then retry."
}
