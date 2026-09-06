package doctor

import (
	"fmt"
	"slices"
	"strings"

	semver "github.com/Masterminds/semver/v3"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/types"
)

// gatedKey is one version-gated magusfile key this workspace uses: how a magusfile
// spells it, and the release that first understood it.
type gatedKey struct {
	label string
	since string
}

// usedSchemaKeys reports which version-gated magusfile keys this workspace actually
// depends on, detected from decoded project state rather than by re-scanning magusfile
// source.
//
// Only keys whose use is OBSERVABLE on a Project appear here, and only keys that carry
// a Since - the rest predate floors and need no coverage. Detection by decoded state
// means a key set through any spelling or any composition path still counts, where a
// source scan would miss `"tools": policy.all` and every other indirection.
//
// Per-target policy is walked alongside the top-level options because that is the
// vocabulary that has actually been growing, and `timeout` there is the key that
// deadlocked a workspace. Its labels are qualified (`targets[].timeout`) for two
// reasons: it is how a reader finds the declaration, and `exclusive` exists in both
// vocabularies, so a bare name would not say which floor is being reported.
func usedSchemaKeys(projects []*types.Project) []gatedKey {
	var used []gatedKey
	add := func(label, since string, ok, inUse bool) {
		if !inUse || !ok || since == "" {
			return
		}
		if slices.ContainsFunc(used, func(g gatedKey) bool { return g.label == label }) {
			return
		}
		used = append(used, gatedKey{label: label, since: since})
	}
	addProject := func(key string, inUse bool) {
		since, ok := types.ProjectOptionSince(key)
		add(key, since, ok, inUse)
	}
	addPolicy := func(key string, inUse bool) {
		since, ok := types.TargetPolicySince(key)
		add("targets[]."+key, since, ok, inUse)
	}
	// Every key carrying a Since needs a line here, or the floor it declares is
	// documentation. TestEveryGatedKeyIsDetectable is what holds that true.
	for _, p := range projects {
		addProject("no_language", p.NoLanguage != "")
		addProject("tools", len(p.ToolBounds) > 0)
		addProject("review_required", len(p.ReviewRequired) > 0)
		addProject("gate_low_risk", p.GateLowRiskDeclared)
		addProject("gate_inherit", p.GateInheritOff)
		for _, policy := range p.TargetPolicies {
			addPolicy("timeout", policy.Timeout != "")
			addPolicy("retry_on_volatile", policy.RetryOnVolatile)
		}
	}
	slices.SortFunc(used, func(a, b gatedKey) int { return strings.Compare(a.label, b.label) })
	return used
}

// checkSchemaFloor fails when the workspace uses a magus.project key that its declared
// required_version does not cover.
//
// Load no longer aborts on a key an older binary does not know, so this is no longer the
// last line against a deadlock. It is the last line against a SILENT one: the key is
// dropped and the policy it declared stops applying, and required_version is what turns
// that into MGS1021 ("your magus is too old, here is what this workspace needs") before
// any magusfile is evaluated.
//
// It only works if somebody bumps the floor in the same commit that adds the key, which
// is exactly the bookkeeping nobody remembers. `no_language` shipped without it. So this
// asserts it mechanically instead.
//
// Two gaps it does not close. A binary older than required_version itself never gets far
// enough to evaluate one. And ward.CheckRequiredVersion exempts dev builds, on the
// reasoning that a source build is compiled from the workspace it runs against; that
// holds for one checkout and not for a `./magus` carried between worktrees on different
// branches, which is how this deadlock was actually hit.
func (r *runner) checkSchemaFloor(projects []*types.Project) types.DoctorCheck {
	const name = "required-version-covers-schema"
	used := usedSchemaKeys(projects)
	if len(used) == 0 {
		return types.DoctorCheck{Name: name, Status: types.DoctorOK,
			Message: "no version-gated magusfile keys in use"}
	}

	cfg, err := config.LoadWithRoot("", r.root)
	if err != nil {
		return types.DoctorCheck{Name: name, Status: types.DoctorAdvice,
			Message: fmt.Sprintf("could not read config to check the floor: %v", err)}
	}
	declared := strings.TrimSpace(cfg.RequiredVersion)

	// Highest Since across the keys in use is the floor the workspace actually needs.
	var need string
	details := make([]string, 0, len(used))
	for _, k := range used {
		details = append(details, fmt.Sprintf("%s (needs >= %s)", k.label, k.since))
		if need == "" || compareVersions(k.since, need) > 0 {
			need = k.since
		}
	}

	if declared == "" {
		return types.DoctorCheck{
			Name:   name,
			Status: types.DoctorAdvice,
			Message: fmt.Sprintf("magus.yaml declares no required_version, but this workspace uses "+
				"magusfile keys an older magus cannot load; add `required_version: \">= %s\"`", need),
			Details: details,
		}
	}

	// Satisfied by the oldest version the constraint admits, not by the running binary:
	// the question is whether the DECLARED floor is high enough, and a floor of
	// ">= 0.1.0" is wrong however new the magus evaluating it happens to be.
	c, cerr := semver.NewConstraint(declared)
	needV, verr := semver.NewVersion(need)
	if cerr != nil || verr != nil {
		return types.DoctorCheck{Name: name, Status: types.DoctorAdvice,
			Message: fmt.Sprintf("required_version %q is not a constraint this check can evaluate", declared),
			Details: details}
	}
	if c.Check(needV) && !c.Check(highestBelow(needV)) {
		return types.DoctorCheck{Name: name, Status: types.DoctorOK,
			Message: fmt.Sprintf("required_version %q covers %d version-gated key(s)", declared, len(used)),
			Details: details}
	}
	if c.Check(highestBelow(needV)) {
		return types.DoctorCheck{
			Name:   name,
			Status: types.DoctorAdvice,
			Message: fmt.Sprintf("required_version %q admits a magus older than %s, which cannot load "+
				"this workspace's magusfiles; raise it to \">= %s\"", declared, need, need),
			Details: details,
		}
	}
	return types.DoctorCheck{Name: name, Status: types.DoctorOK,
		Message: fmt.Sprintf("required_version %q covers %d version-gated key(s)", declared, len(used)),
		Details: details}
}

// highestBelow returns the GREATEST version below v, so that "does this constraint admit
// anything older than v" can be answered with a single probe.
//
// Greatest, not merely "some older version", and that distinction is the whole check.
// Probing the previous minor's .0 - v0.4.0 -> v0.3.0 - answers a narrower question than
// the one being asked: `>= 0.3.5` rejects 0.3.0, so the check reported OK while the
// declared floor still admitted 0.3.5 through 0.3.9, none of which can load the
// workspace. Any floor set INSIDE the previous series was invisible.
//
// Patch is the finest granularity a release can occupy, so decrementing it is exact.
// Below a .0 there is no greatest patch, so the probe uses one no release will reach;
// a constraint admitting it admits everything in that series.
func highestBelow(v *semver.Version) *semver.Version {
	// Beyond any real release, and far below the uint64 the field holds.
	const unreachablePatch = uint64(1) << 40
	switch {
	case v.Patch() > 0:
		return semver.New(v.Major(), v.Minor(), v.Patch()-1, "", "")
	case v.Minor() > 0:
		return semver.New(v.Major(), v.Minor()-1, unreachablePatch, "", "")
	case v.Major() > 0:
		return semver.New(v.Major()-1, unreachablePatch, unreachablePatch, "", "")
	default:
		return semver.New(0, 0, 0, "", "")
	}
}

// compareVersions orders two version strings, treating an unparsable one as lowest so
// it can never win the "highest Since" race and silently raise the required floor.
func compareVersions(a, b string) int {
	av, aerr := semver.NewVersion(a)
	bv, berr := semver.NewVersion(b)
	switch {
	case aerr != nil && berr != nil:
		return 0
	case aerr != nil:
		return -1
	case berr != nil:
		return 1
	}
	return av.Compare(bv)
}
