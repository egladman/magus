package ward

import (
	"errors"
	"fmt"
	"strings"

	"github.com/Masterminds/semver/v3"

	"github.com/egladman/magus/libs/diagnostics"

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

// Codes gopherbuzz reports when a name magus itself provides cannot be resolved.
// Referenced as literals rather than by importing gopherbuzz: this package is in the
// magus module's dependency floor, and the codes are the stable published identifier.
const (
	buzzUndefinedType    = "BZZ1002"
	buzzUnresolvedImport = "BZZ2001"
)

// bootstrapCmd builds a first binary in a checkout no magus on hand can load; the guard
// serves the same command.
const bootstrapCmd = "GOEXPERIMENT=jsonv2 go run -trimpath ./cmd/magus run go-build --no-cache ."

// StaleBuild is what magus knows about a binary that may be older than its workspace:
// the facts every out-of-date-binary report states, and the only ones it decides from.
//
// Two paths reach that conclusion from different evidence and at different moments: a
// magusfile naming something this build never heard of ([ExplainStaleBinary]) and
// magus.yaml carrying a key it cannot decode, which fails before there is a Config to
// read. Both render [StaleBuild.Advice], so they cannot disagree about the remedy.
type StaleBuild struct {
	// Running is the binary's version, [DevVersion] for an unstamped build, and "" when
	// the caller does not know it. "" is reported as unknown, never guessed at.
	Running string `json:"running,omitempty"`
	// Floor is the workspace's required_version, "" when it declares none.
	Floor string `json:"required_version,omitempty"`
	// SourceTree is whether cmd/magus exists at the workspace root: the workspace is a
	// magus checkout, so a binary can be built from it.
	SourceTree bool `json:"source_tree"`
}

// Advice states the version gap once and lists the fixes as runnable commands, one per
// line, without a trailing newline.
//
// The go-build line appears only for a [StaleBuild.SourceTree] workspace, and the
// [bootstrapCmd] only when the running build is also a dev build: that is the
// binary that may be unable to load the tree it would rebuild itself from. An unknown
// running version is not a dev build, so it never earns the bootstrap line.
func (b StaleBuild) Advice() string {
	lines := []string{b.gap(), "Fix it:", "  - released binary: magus self update"}
	dev := b.Running != "" && types.IsDevMagusVersion(b.Running)
	switch {
	case b.SourceTree:
		lines = append(lines, "  - built from this checkout: ./magus run go-build .")
		if dev {
			lines = append(lines, "  - if that cannot load the tree either: mv magus magus.old, then "+bootstrapCmd)
		}
	case dev:
		lines = append(lines, "  - built from source, in the checkout it came from: "+bootstrapCmd)
	}
	return strings.Join(lines, "\n")
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

// ExplainStaleBinary annotates a workspace-load failure that looks like an
// out-of-date binary, and returns err untouched otherwise.
//
// CheckRequiredVersion above is the ward that PREVENTS this, and it only fires when
// the workspace author remembered to raise the floor. When they did not (the common
// case, because the floor is a separate manual edit from the change that needed it),
// the failure lands here instead, as a name the magusfile or a spell reached for and
// this binary has never heard of. That is indistinguishable from a typo unless
// something says otherwise, and what makes it worse than a typo is that EVERY magus
// command then fails the same way, including the one that would build a newer binary.
// That is the deadlock: the tool cannot tell you to update the tool.
//
// This cannot say which release introduced the missing name: an old binary has never
// heard of that release. It says the two things an old binary does know: what it is,
// and what the workspace asked for. That is enough to act on.
//
// Deliberately NOT restricted to released builds. CheckRequiredVersion exempts dev
// builds on the reasoning that a source build is compiled from the workspace it runs,
// so it cannot lack a feature that workspace uses. That holds only until the checkout
// moves: a binary built before a pull is a dev build that is genuinely too old, and it
// is the single most likely way to reach this in day-to-day work.
//
// A joined load error keeps its branches: each stale-shaped one becomes MGS1021 with a
// one-line note, the others are returned untouched, and [StaleBuild.Advice] is stated
// once, on the last stale branch, however many files failed.
func ExplainStaleBinary(err error, running, constraint string) error {
	if err == nil {
		return nil
	}
	branches, isJoin := joined(err)
	if !isJoin {
		branches = []error{err}
	}
	last := -1
	for i, e := range branches {
		if staleShaped(e) {
			last = i
		}
	}
	if last < 0 {
		return err
	}
	out := make([]error, len(branches))
	for i, e := range branches {
		switch {
		case i == last:
			// MGS1021, the same code CheckRequiredVersion raises: this is the same
			// condition caught later and by a different signal, so it should be the same
			// thing to look up. WrapDiagnostic keeps the original diagnostic in the
			// chain, so a caller that branches on the BZZ code still can.
			out[i] = types.WrapDiagnostic(types.WorkspaceNeedsNewerMagus, e, "%s\n%s\n\n%s",
				e, staleNameNote, StaleBuild{Running: running, Floor: constraint}.Advice())
		case staleShaped(e):
			out[i] = types.WrapDiagnostic(types.WorkspaceNeedsNewerMagus, e, "%s\n%s", e, staleNameNote)
		default:
			out[i] = e
		}
	}
	if !isJoin {
		return out[0]
	}
	return errors.Join(out...)
}

// staleNameNote follows each failure that names something this build may not provide.
const staleNameNote = "This build does not provide that name; unless it is misspelled, this magus is out of date."

// staleShaped reports whether err carries one of the codes an out-of-date binary produces.
// BZZ1009, a namespace the file never imported, is not one: no newer binary supplies a
// missing import line.
func staleShaped(err error) bool {
	var d *diagnostics.Error
	if !errors.As(err, &d) {
		return false
	}
	code := string(d.Code)
	return code == buzzUndefinedType || code == buzzUnresolvedImport
}

// joined returns err's branches when rejoining them reproduces its message, as it
// does for errors.Join. A multi-%w wrapper such as "%w: %w" fails that test, so
// splitting it would drop its own text.
func joined(err error) ([]error, bool) {
	multi, ok := err.(interface{ Unwrap() []error })
	if !ok {
		return nil, false
	}
	errs := multi.Unwrap()
	msgs := make([]string, len(errs))
	for i, e := range errs {
		msgs[i] = e.Error()
	}
	return errs, strings.Join(msgs, "\n") == err.Error()
}
