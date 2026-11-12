package ward

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/Masterminds/semver/v3"

	"github.com/egladman/magus/types"
)

// CheckRequiredVersion checks the running build against the workspace's declared floor
// (magus.yaml required_version) and returns MGS1021 when the build is too old.
//
// This is the only ward an OLD binary can enforce, and that asymmetry is the whole
// reason it exists. When a magusfile reaches for a host module or a project key
// added after the running binary was built, the binary that reports the failure is
// the old one: it cannot look up which release introduced the thing it does not
// have, because it has never heard of that release. The failure therefore surfaces
// wherever the magusfile happened to touch it first (`import "xml": module not
// found`), which reads like a typo rather than an out-of-date tool. A declared
// floor is a claim the old binary CAN evaluate, so it must be checked before the
// magusfile is evaluated at all.
//
// Both arguments tolerate being absent, in opposite directions and for different
// reasons:
//
//   - constraint == "" is a workspace that declares no floor. Most workspaces, and
//     every one written before this existed.
//   - running == "" is a caller that never supplied a version: a bare library
//     caller of magus.Open, or a test. It has no version to be too old, so there is
//     nothing to compare and the check passes. Same escape hatch the server
//     adoption gate uses.
//
// A DEV BUILD also passes, in either spelling: the unstamped sentinel ("unknown") and
// the git-describe form a source build carries ("v0.3.0-286-gabc123"). Both are newer
// than the release they name, and blocking one against a floor the working tree itself
// just raised is exactly backwards.
//
// The describe form used to be compared, and that made a floor for an UNRELEASED feature
// unarmable. `git describe` says "286 commits past v0.3.0", but semver parses that as the
// prerelease 0.3.0-286-gabc123, which orders BELOW 0.3.0, the opposite of what the
// string means. Stripping the prerelease recovers 0.3.0, so a floor naming the release
// that will first carry the feature rejected every build made before that release was
// tagged, including the source-path builds CI uses to exercise a magusfile change at the
// commit that introduces it. The floor could only be raised AFTER the release, which is
// precisely when it is no longer the thing anyone needed protecting from.
//
// Exempting it is not a hole. A source build is compiled FROM the workspace it then runs,
// so it cannot lack a feature that workspace's magusfile uses; they are the same commit.
// The binary a floor exists to catch is one built somewhere else and shipped: a release.
// types.IsDevMagusVersion is the same test the drift classifier uses, so "what counts as
// a dev build" has one answer.
//
// A malformed constraint is an error rather than a silent pass. A floor nobody can
// parse protects nobody, and failing loudly on it is what keeps a typo from
// reading as "no floor declared".
func CheckRequiredVersion(constraint, running string) *types.DiagnosticError {
	if constraint == "" || running == "" || types.IsDevMagusVersion(running) {
		return nil
	}
	c, err := semver.NewConstraint(constraint)
	if err != nil {
		return types.DiagnosticErrorf(types.WorkspaceNeedsNewerMagus,
			"magus.yaml required_version %q is not a valid semver constraint: %v. "+
				"Use a comparison like \">= 0.4.0\", or remove the key to declare no floor.",
			constraint, err)
	}
	v, err := semver.NewVersion(strings.TrimPrefix(running, "v"))
	if err != nil {
		// A running version this ward cannot parse is not the workspace's fault, and
		// refusing to load over it would strand the user with no way forward. Nothing
		// to compare, so nothing to report.
		//nolint:nilerr // an unparsable running version means no comparison to make, not a failure to report
		return nil
	}
	// Compare on the release alone. A prerelease of the version that satisfies the
	// floor is treated as satisfying it, because semver constraints otherwise
	// exclude every prerelease, so a v0.4.0-rc1 binary would be told to upgrade to
	// 0.4.0, which it effectively already is.
	base, err := v.SetPrerelease("")
	if err == nil {
		v = &base
	}
	if c.Check(v) {
		return nil
	}
	return types.DiagnosticErrorf(types.WorkspaceNeedsNewerMagus,
		"this workspace requires magus %s and this build is %s. "+
			"Upgrade the binary (`magus self update`), or if this is CI, raise the pinned "+
			"version in your magus setup step. The workspace declares the floor in "+
			"magus.yaml (required_version).",
		constraint, running)
}

// DevVersion is the placeholder an unstamped dev build carries. It mirrors the
// default of `version` in cmd/magus/version.go (and proc's devVersionSentinel);
// keep the three in sync.
const DevVersion = "unknown"

// BootstrapCommand builds a first binary in a checkout no magus on hand can load; the
// guard serves the same command.
const BootstrapCommand = "GOEXPERIMENT=jsonv2 go run -trimpath ./cmd/magus run go-build --no-cache ."

// relinkCommand links ./magus from the checkout's committed sources.
// It loads no magusfile, so it runs whatever the tree's contract says.
// The binary it links embeds the tree's contract.
const relinkCommand = "GOEXPERIMENT=jsonv2 go build -trimpath -o magus ./cmd/magus"

// RelinkAt is the relink of the checkout at root, aimed with -C so it runs
// from any directory, and bare for root "". A root a POSIX shell would
// split is single-quoted.
func RelinkAt(root string) string {
	if root == "" {
		return relinkCommand
	}
	return strings.Replace(relinkCommand, " go ", fmt.Sprintf(" go -C %s ", shellWord(root)), 1)
}

// shellWord quotes word for a POSIX shell, and leaves it bare when it can.
func shellWord(word string) string {
	const safe = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ" +
		"0123456789@%+=:,./-_"
	if word != "" && strings.Trim(word, safe) == "" {
		return word
	}
	return fmt.Sprintf("'%s'", strings.ReplaceAll(word, "'", `'\''`))
}

// StaleBuild is what magus knows about a binary that may be older than its workspace:
// the facts every out-of-date-binary report states, and the only ones it decides from.
// A magus.yaml key the build cannot decode renders [StaleBuild.Advice].
type StaleBuild struct {
	// Running is the binary's version, [DevVersion] for an unstamped build, and "" when
	// the caller does not know it. "" is reported as unknown, never guessed at.
	Running string `json:"running,omitempty"`
	// Floor is the workspace's required_version, "" when it declares none.
	Floor string `json:"required_version,omitempty"`
	// Scripts are the run lines of the scripts the workspace declares for MGS1021
	// (SERVES "code:MGS1021"), in index order. magus never guesses a workspace's own
	// rebuild from its layout; a workspace that has one says so.
	Scripts []string `json:"scripts,omitempty"`
}

// Advice states the version gap once and lists the fixes as runnable commands, one per
// line, without a trailing newline.
//
// Each declared script gets a line, and the [BootstrapCommand] appears only when the
// running build is a dev build: that is the binary that may be unable to load the tree
// it would rebuild itself from. An unknown running version is not a dev build, so it
// never earns the bootstrap line.
func (b StaleBuild) Advice() string {
	update := "  - released binary: magus self update"
	if pin := PinnedRelease(b.Floor); pin != "" {
		update += " --version " + pin
	}
	lines := []string{b.gap(), "Fix it:", update}
	for _, run := range b.Scripts {
		lines = append(lines, "  - this workspace's script for it: "+run)
	}
	if b.Running != "" && types.IsDevMagusVersion(b.Running) {
		lines = append(lines, "  - built from source, in the checkout it came from: "+BootstrapCommand)
	}
	return strings.Join(lines, "\n")
}

var constraintClause = regexp.MustCompile(`^(==?|>=|<=|<|>|~|\^)?\s*v?(\d+\.\d+\.\d+(?:-[0-9A-Za-z.]+)?)$`)

// PinnedRelease is the release, "vX.Y.Z", that `magus self update --version` installs for
// a workspace requiring constraint, and "" when the newest release is the one to take.
// self update reads no magus.yaml, so where constraint bounds releases from above the
// newest may be one the workspace refuses: an exact pin names itself, and a range with
// an upper bound names its inclusive lower bound. A range with no inclusive lower bound,
// a union, or a constraint that does not parse names none.
func PinnedRelease(constraint string) string {
	if strings.Contains(constraint, "||") {
		return ""
	}
	clauses := strings.Split(constraint, ",")
	if len(clauses) == 1 {
		if m := constraintClause.FindStringSubmatch(strings.TrimSpace(clauses[0])); len(m) == 3 && strings.Trim(m[1], "=") == "" {
			return "v" + m[2]
		}
	}
	var lower string
	bounded := false
	for _, clause := range clauses {
		m := constraintClause.FindStringSubmatch(strings.TrimSpace(clause))
		if m == nil {
			return ""
		}
		switch m[1] {
		case "", "=", "==", "~", "^":
			lower, bounded = m[2], true
		case ">=":
			lower = m[2]
		case "<", "<=":
			bounded = true
		}
	}
	if !bounded || lower == "" {
		return ""
	}
	c, err := semver.NewConstraint(constraint)
	if err != nil {
		return ""
	}
	if v, err := semver.NewVersion(lower); err != nil || !c.Check(v) {
		return ""
	}
	return "v" + lower
}

// gap is the one sentence comparing the build with the floor, naming whichever of the
// two is unknown.
func (b StaleBuild) gap() string {
	running, floor := strings.TrimSpace(b.Running), strings.TrimSpace(b.Floor)
	switch {
	case running == "" && floor == "":
		return "This magus's version is unknown here, and the workspace declares no required_version floor."
	case running == "":
		return fmt.Sprintf("This magus's version is unknown here; the workspace needs %s.", floor)
	case running == DevVersion && floor == "":
		return "This magus is an unstamped build, so its version is unknown, and the workspace declares no required_version floor."
	case running == DevVersion:
		return fmt.Sprintf("This magus is an unstamped build, so its version is unknown; the workspace needs %s.", floor)
	case floor == "":
		return fmt.Sprintf("This magus is %s; the workspace declares no required_version floor.", running)
	}
	below, ok := belowFloor(running, floor)
	switch {
	case !ok:
		return fmt.Sprintf("This magus is %s; the workspace needs %s, and the two cannot be compared.", running, floor)
	case below:
		return fmt.Sprintf("This magus (%s) is older than the workspace needs (%s).", running, floor)
	default:
		return fmt.Sprintf("This magus (%s) meets the workspace floor (%s) but was likely built before the workspace changed.", running, floor)
	}
}

// belowFloor reports whether running fails constraint, comparing the release alone as
// [CheckRequiredVersion] does; ok is false when either side does not parse.
//
// Unlike that ward it compares dev builds too: git-describe's v0.4.3-122-gabc is 122
// commits past v0.4.3, so it is below a ">= 0.4.4" floor and meets ">= 0.4.3".
func belowFloor(running, constraint string) (below, ok bool) {
	c, err := semver.NewConstraint(constraint)
	if err != nil {
		return false, false
	}
	v, err := semver.NewVersion(strings.TrimPrefix(running, "v"))
	if err != nil {
		return false, false
	}
	if base, err := v.SetPrerelease(""); err == nil {
		v = &base
	}
	return !c.Check(v), true
}
