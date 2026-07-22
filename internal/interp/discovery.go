package interp

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"

	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/gopherbuzz/ast"
	"github.com/egladman/magus/libs/gopherbuzz/vm"
	"github.com/egladman/magus/std"
	"github.com/egladman/magus/types"
)

// DiscoverCtxNodes builds the target graph nodes for the ctx-form targets in src
// by RUNNING each one in discovery mode (types.WithDiscovery): the injected
// magus.Context records the target's needs/inputs/outputs/charms instead of
// dispatching, and every side-effecting host op no-ops (discovery is a superset of
// dry-run tracing), so no real work runs. The recorded declarations become a
// types.TargetGraphNode with the same shape describe.Extract produces for the old
// global-magus form, so downstream (affected, cache footprint, MAGUS.md, cycle
// detection) is unchanged.
//
// It covers ONLY ctx-form targets; old-form targets in the same magusfile stay with
// describe.Extract. The two node sets are merged by the caller. Best-effort on
// missing/ill-formed sources (returns what it can); a body that errors under
// discovery is surfaced so a genuine authoring bug is not silently dropped.
// It also returns the per-target execution policy each ctx-form target declared via
// ctx.skip_cache/exclusive/slots, keyed by normalized target name, for the caller to
// fold into Project.TargetPolicies alongside the old global-magus form. A target that
// declared no policy gets no entry (matching the old form). The policy map is separate
// from the nodes so TargetGraphNode stays a pure graph type carrying no run policy.
func DiscoverCtxNodes(ctx context.Context, src *Source) ([]types.TargetGraphNode, map[string]types.Target, error) {
	if src == nil || src.Engine != "buzz" {
		return nil, nil, nil
	}
	// Resolve the body's relative reads against the project dir, mirroring the run
	// path (runBuzz), without an os.Chdir of the whole process.
	ctx = std.WithCwd(ctx, src.Dir)
	ctx = WithSource(ctx, src)

	// Which exported targets are ctx-form, and each one's doc comment (for the node
	// description). Read straight from the AST: the signature contract and the doc
	// both live there.
	ctxForm := map[string]bool{}
	docs := map[string]string{}
	norm := targetNameNormalizerFrom(ctx)
	for _, f := range src.Files {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		prog, perr := buzz.ParseEmbedded(string(data))
		if perr != nil || prog == nil {
			continue
		}
		for _, stmt := range prog.Stmts {
			fd, ok := stmt.(*ast.FunDecl)
			if !ok || !fd.IsExported {
				continue
			}
			if len(fd.ParamAnnots) > 0 && fd.ParamAnnots[0] == types.ContextParamAnnot {
				key := norm.NormalizeTargetName(fd.Name)
				ctxForm[key] = true
				docs[key] = fd.Doc
			}
		}
	}
	if len(ctxForm) == 0 {
		return nil, nil, nil
	}

	buzzSess, _, err := execBuzzSrc(ctx, src, false)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = buzzSess.Close() }()

	targetCtxVal := buzzSess.GetGlobal(TargetContextGlobal)
	exports := buzzSess.Exports()
	var nodes []types.TargetGraphNode
	policies := map[string]types.Target{}
	var errs []error
	// Iterate in a deterministic (sorted) order so the recorded footprint is stable
	// run to run even if a body reads session state a sibling body set.
	for _, name := range slices.Sorted(maps.Keys(exports)) {
		val := exports[name]
		key := norm.NormalizeTargetName(name)
		if !ctxForm[key] || !val.IsFun() {
			continue
		}
		rec := &types.DiscoveryRecord{}
		dctx := types.WithDiscovery(ctx, rec)
		// (ctx, args): the empty arg list stands in for `args: [str]`; a discovered
		// body reads its declarations, not its runtime args.
		args := []vm.Value{targetCtxVal, vm.ListValue(nil)}
		if _, cerr := buzzSess.CallValue(dctx, val, args); cerr != nil {
			// Best-effort PER TARGET: a body that errors drops only its own node, not
			// every sibling ctx-form target's declared footprint. The errors are joined
			// and returned so a genuine authoring bug still surfaces (as a warning at the
			// caller) rather than being swallowed.
			errs = append(errs, fmt.Errorf("discover target %q: %w", key, cerr))
			continue
		}
		nodes = append(nodes, nodeFromRecord(key, docs[key], rec))
		if pol, ok := policyFromRecord(rec); ok {
			policies[key] = pol
		}
	}
	slices.SortFunc(nodes, func(a, b types.TargetGraphNode) int {
		return strings.Compare(a.Name, b.Name)
	})
	return nodes, policies, errors.Join(errs...)
}

// policyFromRecord returns the per-target execution policy a discovery record
// declared via ctx.skip_cache/exclusive/slots, and whether any was set. A target
// that declared none gets no entry, mirroring the old form where only a target named
// in magus.project's targets map carries a policy. Only the author-facing policy
// fields are set; the CI-only FailOnDrift/RetryOnVolatile are not ctx-declarable.
func policyFromRecord(rec *types.DiscoveryRecord) (types.Target, bool) {
	if !rec.SkipCache && !rec.Exclusive && rec.Slots == 0 {
		return types.Target{}, false
	}
	return types.Target{SkipCache: rec.SkipCache, Exclusive: rec.Exclusive, Slots: rec.Slots}, true
}

// nodeFromRecord assembles a target graph node from a discovery record. Dependency
// and charm lists are already normalized and deduped by the recording methods; the
// order is call order (needs) then sorted (charms), matching the extractor.
func nodeFromRecord(name, doc string, rec *types.DiscoveryRecord) types.TargetGraphNode {
	node := types.TargetGraphNode{
		Name:              name,
		Doc:               firstDocSentence(doc),
		Dependencies:      rec.Needs,
		CrossDependencies: rec.CrossDeps,
		Outputs:           rec.Outputs,
	}
	// ctx.inputs("glob") records a same-project glob; carry it in the unified
	// InputRef shape (empty Project = this target's own project, filled at
	// resolution), matching the static extractor's node.Inputs representation.
	for _, g := range rec.Inputs {
		node.Inputs = append(node.Inputs, types.InputRef{Glob: g})
	}
	if len(rec.Charms) > 0 {
		charms := append([]string(nil), rec.Charms...)
		slices.Sort(charms)
		node.Charms = charms
	}
	return node
}

// firstDocSentence reduces a doc-comment block to its first sentence, dropping
// blank and divider lines. A trimmed-down mirror of describe.docSentence, inlined
// here to avoid an import cycle (describe imports nothing from interp, and interp
// must not depend on describe).
func firstDocSentence(doc string) string {
	if doc == "" {
		return ""
	}
	var prose []string
	for _, line := range strings.Split(doc, "\n") {
		s := strings.TrimSpace(line)
		if s == "" || strings.Contains(s, "─") {
			continue
		}
		prose = append(prose, s)
	}
	s := strings.TrimSpace(strings.Join(prose, " "))
	for i := 0; i < len(s); i++ {
		if s[i] == '.' && (i == len(s)-1 || s[i+1] == ' ') {
			return s[:i+1]
		}
	}
	return s
}
