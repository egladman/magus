package magus

import (
	"context"
	"errors"
	"sort"
	"strings"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/types"
)

// projectTargets returns the target names project p can actually key: those declared
// directly in its magusfile plus every target its resolved spells provide. This is the
// candidate space IdentifyRef sweeps - not every workspace target name crossed with
// every project. buildStep will happily key a target name a project never declared,
// but no real run ever mints a ref under that combination, so sweeping it would only
// waste cycles and manufacture false positives.
func projectTargets(p *types.Project) []string {
	seen := map[string]struct{}{}
	var out []string
	add := func(name string) {
		if _, ok := seen[name]; !ok {
			seen[name] = struct{}{}
			out = append(out, name)
		}
	}
	for _, t := range p.MagusfileTargets {
		add(t)
	}
	for _, s := range p.ResolvedSpells {
		for _, t := range s.Targets() {
			add(t)
		}
	}
	return out
}

// IdentifyRef inverts a ref back to the workspace target(s) that could have minted it.
// A ref cannot be decoded - it is a truncated hash, not an encoding - so identification
// works by PREDICTION instead: key every candidate target exactly as a run would (via
// ComputeTargetKey) and compare its PortableRef against ref. This is the whole point of
// the method: it exists for the moment someone pastes a ref from a teammate's terminal
// or a CI log and asks "what is this", with no other metadata to go on.
//
// Each target is tried under two charm sets - the workspace's configured default charms
// and the empty set, deduped - because CI runs `--no-default-charms` and CI is the peer
// whose refs most often get pasted into a local terminal: a ref minted by the bare CI
// variant must still resolve even though this workspace's local runs always carry the
// configured defaults. The defaults are read from m.cfg.DefaultCharms rather than taken
// as a parameter, exactly as ListCharms reads them (see its doc comment): a caller that
// forgets to pass defaults must not silently degrade to a bare-charm-only sweep.
//
// ref may be a full portable ref or a unique prefix of one, mirroring the resolver in
// internal/cache/output.go: matches are ref-as-prefix-of-full-ref, not equality, so a
// shortened ref still finds its target. A ref that is not shaped like a ref, or that
// matches nothing, yields a nil slice and no error - "nothing matched" is a finding
// here, not a failure. A single target that fails to key (an unresolved project, a
// malformed step) is skipped rather than aborting the sweep, since this runs on a
// best-effort error path. The one exception is types.ErrNoCache, checked up front -
// mirroring computeTargetKey's own first line - rather than left to surface from deep
// in the sweep: a cache-free (Inspect) workspace can mint no keys at all, so the whole
// method is meaningless without a cache, and there is no point walking every project
// and probing every spell's tool version first only to discover that.
//
// Both nil-slice cases - ref not shaped like a ref, and a well-formed ref matching
// nothing - render identically to a caller, which would be misleading for a garbage
// string. That is deliberate here rather than a gap: cmd/magus/query.go's queryCmd
// and internal/handler/mcp's outputTool.Invoke both reject a non-ref-shaped ref with
// cache.LooksLikeRef up front, before either reaches a code path that calls
// IdentifyRef, so every real caller already knows ref is ref-shaped by the time the
// nil slice would need distinguishing.
//
// Matches are sorted by project, then target, then charms, so repeated calls and
// rendered output are stable.
func (m *Magus) IdentifyRef(ctx context.Context, ref string) ([]types.RefMatch, error) {
	if m.cache == nil {
		return nil, types.ErrNoCache
	}
	ref = strings.ToLower(strings.TrimSpace(ref))
	if !cache.LooksLikeRef(ref) {
		return nil, nil
	}

	defaultCharms := m.cfg.DefaultCharms
	variants := [][]string{defaultCharms}
	if len(defaultCharms) > 0 {
		variants = append(variants, nil)
	}

	// Sweep-scoped only: buildStep gives most targets in a project the SAME
	// Sources baseline (see run.go's buildStep doc), so without this memo every
	// target/charm combination re-walks and re-hashes an identical file set. Safe
	// here because IdentifyRef only predicts - it executes nothing between calls -
	// and the memo is discarded the moment this sweep returns. See
	// cache.SourceMemo's doc for the full safety argument.
	memo := cache.NewSourceMemo()

	// Resolved ONCE for the whole workspace, for the same reason: every probe spawns
	// a subprocess, and computeTargetKey's own memo lives only as long as one call.
	// Probing per target dominated the sweep - see computeTargetKey's doc.
	projects := m.All()
	reuse := &sweepReuse{memo: memo, toolVersions: m.toolVersionsByProject(ctx, projects)}

	var matches []types.RefMatch
	for _, p := range projects {
		for _, target := range projectTargets(p) {
			for _, charms := range variants {
				key, _, err := m.computeTargetKey(ctx, p.Path, target, charms, reuse)
				if err != nil {
					if errors.Is(err, types.ErrNoCache) {
						return nil, err
					}
					continue
				}
				if strings.HasPrefix(cache.PortableRef(key), ref) {
					matches = append(matches, types.RefMatch{
						Project: p.Path,
						Target:  target,
						Charms:  charms,
					})
				}
			}
		}
	}

	sort.Slice(matches, func(i, j int) bool {
		a, b := matches[i], matches[j]
		if a.Project != b.Project {
			return a.Project < b.Project
		}
		if a.Target != b.Target {
			return a.Target < b.Target
		}
		return strings.Join(a.Charms, ",") < strings.Join(b.Charms, ",")
	})
	return matches, nil
}

// RefMatchCommand renders a types.RefMatch (as returned by IdentifyRef) as the
// "magus run" invocation that would key it: the target name (with a
// :charm1,charm2 suffix when the match required explicit charms), the project
// (omitted for the workspace root "."), and --no-default-charms when the match
// required the bare CI variant while m.cfg.DefaultCharms is non-empty, since the
// workspace's configured defaults would otherwise apply and mint a different key.
//
// It is a method on *Magus, not a free function, so it reads m.cfg.DefaultCharms
// itself rather than taking it as a parameter a caller could pass stale or out of
// sync with the *Magus that produced the match in the first place.
//
// Shared by cmd/magus/query.go's ref-lookup suggestion and internal/handler/mcp's
// magus_output not-found fallback, so the CLI and the MCP surface render the exact
// same reproduce command instead of two copies that can drift. It renders the
// "magus run" prefix via hint.Run so the command path itself stays
// single-sourced with every other canonical command reference.
func (m *Magus) RefMatchCommand(mt types.RefMatch) string {
	target := mt.Target
	if len(mt.Charms) > 0 {
		target += ":" + strings.Join(mt.Charms, ",")
	}
	args := []string{target}
	if mt.Project != "." {
		args = append(args, mt.Project)
	}
	if len(mt.Charms) == 0 && len(m.cfg.DefaultCharms) > 0 {
		args = append(args, "--no-default-charms")
	}
	return hint.Run.With(args...)
}
