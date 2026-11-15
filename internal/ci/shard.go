package ci

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/agent"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/libs/text/plural"
	"github.com/egladman/magus/types"
)

func joinDuties(duties []types.Duty) string {
	names := make([]string, len(duties))
	for i, d := range duties {
		names[i] = string(d)
	}
	return strings.Join(names, " ")
}

// shardName names a shard by what is in it. A CI job list is scanned, not
// studied, so this shows the first few paths and counts the rest.
func shardName(paths []string) string {
	const shown = 3
	switch {
	case len(paths) == 0:
		return "no projects"
	case len(paths) <= shown:
		return strings.Join(paths, " ")
	}
	return fmt.Sprintf("%s +%d", strings.Join(paths[:shown], " "), len(paths)-shown)
}

// shardCount is n shards, singular for one.
func shardCount(n int) string {
	return fmt.Sprintf("%d %s", n, plural.Word(n, "shard", "shards"))
}

// ShardDetail is what each shard actually DOES: the invocation, what it runs, and what it
// writes. The plan has always known which projects may run concurrently (the hard half)
// while saying nothing about their content, leaving any reader to infer it from paths.
//
// Every field is JOINED from declarations magus already holds; none of it is new analysis.
// It is plain plan metadata and reads that way for a person, which is why only the one
// genuinely agent-shaped part is nested under Agents rather than spread through it.
type ShardDetail struct {
	// Command is the invocation this shard is, spelled the way a person would run it.
	Command string `json:"command"`
	// Spells say what the shard will actually execute, so the environment it needs can
	// be checked before it starts rather than after it fails.
	Spells []string `json:"spells,omitempty"`
	// Writes is the write set shards collide on: the declared output globs of every project in
	// the shard. This is the field that earns the briefing. Two shards are safe to run
	// together exactly when these do not overlap, and magus is the only party that knows
	// them: whoever splits the work up otherwise hands out units and hopes.
	Writes []string `json:"writes,omitempty"`
	// Agents is the only agent-specific part, kept in its own object so the rest reads as
	// what it is: ordinary plan metadata a person wants too.
	Agents *ShardAgents `json:"agents,omitempty"`
}

// ShardAgents is the one part of a shard record that is specific to an agent reader, kept
// in its own object for that reason. Everything beside it is ordinary plan metadata: this
// plan exists to fan CI jobs across runners, and it long predates anything agentic. That an
// agent can use the same record is a consequence of the record being correct, not a
// feature added for one.
type ShardAgents struct {
	// Skills names the skills this shard's work routes to; Why states the derivation.
	// Derived rather than declared so it cannot drift from what the shard does, and
	// stated, because a routing decision an agent cannot audit is one it should not trust.
	Skills []string `json:"skills"`
	Why    []string `json:"why,omitempty"`
}

// ProjectSource lists the workspace's projects. *magus.Magus satisfies it; the
// interface keeps this package from importing the root package, which imports it.
type ProjectSource interface {
	ListProjects(ctx context.Context) (types.ProjectsOutput, error)
}

// ShardSource is a ProjectSource that also answers per-target writes and a project's
// declared outputs.
type ShardSource interface {
	ProjectSource
	TargetGraph(ctx context.Context) (types.TargetGraphOutput, error)
	Get(path string) *types.Project
}

// PlanDetail joins the shard partition against what magus already declares about each
// project, producing one detail record per shard.
//
// This is assembly, not analysis. The plan knew which projects may run concurrently; the
// project list knows each one's spell, declared outputs, and exclusivity; the skills are
// derived from those two. Nothing here inspects a file or runs a target.
func PlanDetail(ctx context.Context, m ShardSource, target string, shards []types.Shard) (map[string]ShardDetail, error) {
	projects, err := m.ListProjects(ctx)
	if err != nil {
		return nil, err
	}
	byPath := make(map[string]types.ProjectEntry, len(projects.Projects))
	for _, p := range projects.Projects {
		byPath[p.Path] = p
	}

	// Per-TARGET writes as well as project-wide ones. A project that declares its outputs
	// per target (ctx.writesFiles(...)) has an EMPTY project-level Outputs, so a
	// project-only join reported this workspace's root shard as writing nothing while it
	// rewrites MAGUS.md and gen/*.json. A write set that omits the busiest writer
	// is worse than none: it reads as a cleared shard.
	writesByProject := map[string][]string{}
	if graph, gerr := m.TargetGraph(ctx); gerr == nil {
		for _, proj := range graph.Projects {
			for _, node := range proj.Nodes {
				for _, ref := range node.WritesFiles {
					writesByProject[proj.Path] = AppendUnique(writesByProject[proj.Path], ref.Rooted(proj.Path).String())
				}
			}
		}
	}

	out := make(map[string]ShardDetail, len(shards))
	for _, s := range shards {
		b := ShardDetail{Command: hint.Run.With(target, strings.Join(s.ProjectPaths, " "))}
		for _, path := range s.ProjectPaths {
			p, ok := byPath[path]
			if !ok {
				continue // a shard naming a project the workspace no longer lists: nothing to say
			}
			b.Spells = AppendUnique(b.Spells, p.Spells...)
			if p.Spell != "" {
				b.Spells = AppendUnique(b.Spells, p.Spell)
			}
			// Project-relative as declared, rooted at the project, so two briefings can be
			// compared for overlap without the reader re-deriving where each one sits.
			if proj := m.Get(path); proj != nil {
				for _, g := range proj.Outputs {
					b.Writes = AppendUnique(b.Writes, g.Root(path).String())
				}
			}
			b.Writes = AppendUnique(b.Writes, writesByProject[path]...)
		}
		skills, why := shardSkills(b)
		b.Agents = &ShardAgents{Skills: skills, Why: why}
		out[s.ID] = b
	}
	return out, nil
}

// shardSkills derives the agent skills a shard's work routes to, and says why.
//
// Derived from what the shard DOES rather than declared in a table, so it cannot drift
// from the shard it describes: a second copy of the routing table would rot the first
// time a project changed spells. Each reason is returned alongside, because a routing
// decision an agent cannot audit is one it should not act on.
func shardSkills(b ShardDetail) (skills, why []string) {
	// The always-full twin, not the primary. The primary entry is the curated shorter
	// form, a bet that the reader who INSTALLED it can re-derive the steps it drops.
	// A record like this is read by someone who did not make that bet and would inherit it
	// with no say, so the twin is the name that survives being passed along.
	skills = append(skills, agent.FullTwinName("magus-run"))
	why = append(why, "magus-run: the shard is a target invocation, and the raw language tool would bypass the cache and the affected set")
	if len(b.Writes) > 0 {
		skills = append(skills, agent.FullTwinName("magus-vcs-hygiene"))
		why = append(why, "magus-vcs-hygiene: this shard declares outputs, so its run leaves generated files that must be classified before they are committed or reverted")
	}
	why = append(why, "variant: each skill is named as its always-full twin, because the reader of this record is not the session that chose the install")
	return skills, why
}

// AppendUnique appends each value not already present, preserving order.
func AppendUnique(dst []string, values ...string) []string {
	for _, v := range values {
		if v != "" && !slices.Contains(dst, v) {
			dst = append(dst, v)
		}
	}
	return dst
}

// FilterShards narrows a plan to the named projects, INTERSECTING them with the affected
// set rather than replacing it.
//
// The distinction is the whole point: `magus run ci docs` runs docs whether or not the diff
// touched it, while this answers "of the work this change actually implies, give me the
// docs part". Splitting a large affected set into units wants the second; the first would
// hand out work the change never justified.
//
// Shard IDs are preserved rather than renumbered, so a filtered plan can be read against
// the unfiltered one it came from; a shard left empty drops out.
//
// A name that matches no project in the WORKSPACE is an error, because it is a typo and
// silently planning nothing is how a typo turns into "the change affected nothing". A name
// that is a real project but outside the affected set is not an error: that is the honest
// empty answer, and it is the question being asked.
func FilterShards(ctx context.Context, m ProjectSource, shards []types.Shard, only []string) ([]types.Shard, error) {
	projects, err := m.ListProjects(ctx)
	if err != nil {
		return nil, err
	}
	known := make(map[string]bool, len(projects.Projects))
	for _, p := range projects.Projects {
		known[p.Path] = true
	}
	want := make(map[string]bool, len(only))
	for _, name := range only {
		clean := strings.TrimSuffix(filepath.ToSlash(strings.TrimSpace(name)), "/")
		if !known[clean] {
			return nil, fmt.Errorf("magus affected --plan: no project %q in this workspace, run `%s` to list them", name, hint.Ls)
		}
		want[clean] = true
	}

	return filterShardPaths(shards, want), nil
}

// filterShardPaths is the pure half of FilterShards: the intersection itself, with the
// workspace lookup and its typo check left to the caller.
func filterShardPaths(shards []types.Shard, want map[string]bool) []types.Shard {
	out := make([]types.Shard, 0, len(shards))
	for _, sh := range shards {
		kept := make([]string, 0, len(sh.ProjectPaths))
		for _, path := range sh.ProjectPaths {
			if want[path] {
				kept = append(kept, path)
			}
		}
		if len(kept) == 0 {
			continue
		}
		sh.ProjectPaths = kept
		sh.Units = slices.DeleteFunc(slices.Clone(sh.Units), func(u types.Unit) bool { return !want[u.Project] })
		sh.Digest = types.UnitsDigest(sh.Units)
		out = append(out, sh)
	}
	return out
}
