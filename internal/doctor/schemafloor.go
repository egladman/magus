package doctor

import (
	"fmt"
	"slices"
	"strings"

	semver "github.com/Masterminds/semver/v3"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/types"
)

// usedProjectOptions reports which magus.project keys this workspace actually depends
// on, detected from decoded project state rather than by re-scanning magusfile source.
//
// Only keys whose use is OBSERVABLE on a Project appear here, and only keys that carry
// a Since - the rest predate floors and need no coverage. Detection by decoded state
// means a key set through any spelling or any composition path still counts, where a
// source scan would miss `"tools": policy.all` and every other indirection.
func usedProjectOptions(projects []*types.Project) []string {
	var used []string
	add := func(key string, inUse bool) {
		if !inUse {
			return
		}
		if since, ok := types.ProjectOptionSince(key); ok && since != "" && !slices.Contains(used, key) {
			used = append(used, key)
		}
	}
	for _, p := range projects {
		add("no_language", p.NoLanguage != "")
		add("tools", len(p.ToolBounds) > 0)
	}
	slices.Sort(used)
	return used
}

// checkSchemaFloor fails when the workspace uses a magus.project key that its declared
// required_version does not cover.
//
// This is the check that would have caught the deadlock it exists to prevent. A
// magusfile key an older binary does not know aborts WORKSPACE LOAD, so every magus
// command fails at once - including `magus run go-build`, the one that would produce a
// binary new enough to read the file. There is no way out from inside the workspace;
// you have to hand-edit the offending key out, build, and put it back.
//
// required_version is what converts that into MGS1021 ("your magus is too old, here is
// what this workspace needs") before any magusfile is evaluated. It only works if
// somebody bumps it in the same commit that adds the key, which is exactly the kind of
// bookkeeping nobody remembers - `no_language` shipped without it. So this asserts it
// mechanically instead.
//
// It does NOT catch a binary older than required_version itself; a release that
// predates the floor key never gets far enough to evaluate one. That gap is covered
// separately, by the upgrade hint on an unrecognized magus.project key.
func (r *runner) checkSchemaFloor(projects []*types.Project) types.DoctorCheck {
	const name = "required version covers schema"
	used := usedProjectOptions(projects)
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
	for _, key := range used {
		since, _ := types.ProjectOptionSince(key)
		details = append(details, fmt.Sprintf("%s (needs >= %s)", key, since))
		if need == "" || compareVersions(since, need) > 0 {
			need = since
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
	if c.Check(needV) && !c.Check(predecessor(needV)) {
		return types.DoctorCheck{Name: name, Status: types.DoctorOK,
			Message: fmt.Sprintf("required_version %q covers %d version-gated key(s)", declared, len(used)),
			Details: details}
	}
	if c.Check(predecessor(needV)) {
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

// predecessor returns the version immediately below v for the purpose of "does this
// constraint admit anything older": the previous patch, or the previous minor's .0 when
// v is itself a .0. Approximate on purpose - it only has to produce a version that IS
// older than v, so a constraint that admits it is demonstrably too loose.
func predecessor(v *semver.Version) *semver.Version {
	switch {
	case v.Patch() > 0:
		return semver.New(v.Major(), v.Minor(), v.Patch()-1, "", "")
	case v.Minor() > 0:
		return semver.New(v.Major(), v.Minor()-1, 0, "", "")
	case v.Major() > 0:
		return semver.New(v.Major()-1, 0, 0, "", "")
	default:
		return semver.New(0, 0, 0, "", "")
	}
}

// compareVersions orders two version strings, treating an unparseable one as lowest so
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
