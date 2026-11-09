package bindings

import (
	"context"
	"fmt"
	"io/fs"
	"maps"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/agent"
	"github.com/egladman/magus/internal/interp"
	"github.com/egladman/magus/internal/interp/bindings/ffi"
	bindgen "github.com/egladman/magus/internal/interp/bindings/gen"
	"github.com/egladman/magus/internal/interp/bindings/gen/decode"
	"github.com/egladman/magus/libs/gopherbuzz/vm"
	"github.com/egladman/magus/project"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
)

func init() { //nolint:gochecknoinits // internal/agent cannot link the Buzz VM; the loader registers from the layer that can
	agent.RegisterHarnessSpellLoader(loadHarnessFromSpell)
}

// harnessSchemaVersion is the descriptor version a spell-built descriptor carries, the one
// agent.LoadHarness validates against.
const harnessSchemaVersion = 2

// loadHarnessFromSpell builds a HarnessDescriptor from the harness contract's functions on
// the spell registered under id. Missing mgs_getHarness is a miss (ok=false).
// Missing mgs_renderPrompts is an error: it used to decode as the empty list a
// hook-prompting host returns. Any other omission is a capability the host lacks.
func loadHarnessFromSpell(ctx context.Context, id string) (agent.HarnessDescriptor, string, bool, error) {
	drv, ok := project.DefaultSpellRegistry().Lookup(id)
	if !ok {
		return agent.HarnessDescriptor{}, "", false, nil
	}
	fail := func(err error) (agent.HarnessDescriptor, string, bool, error) {
		return agent.HarnessDescriptor{}, "", false, fmt.Errorf("harness spell %q: %w", id, err)
	}
	definition, ok, err := callHarness[types.Harness](ctx, drv, "mgs_getHarness")
	if err != nil {
		return fail(err)
	}
	if !ok {
		return agent.HarnessDescriptor{}, "", false, nil
	}
	d := agent.HarnessDescriptor{
		SchemaVersion: harnessSchemaVersion,
		ID:            id,
		Display:       agent.HarnessDisplay{Name: displayName(id)},
		Config:        definition.Config,
		Guard:         definition.Guard,
		Probe:         slices.Clone(definition.Probe),
	}
	for _, asset := range definition.Assets {
		content, err := harnessAssetContent(drv, asset)
		if err != nil {
			return fail(err)
		}
		d.Assets = append(d.Assets, agent.HarnessRuntimeAsset{Path: asset.Path, Content: content})
	}
	if d.Skills, _, err = callHarness[types.HarnessSkills](ctx, drv, "mgs_getSkills"); err != nil {
		return fail(err)
	}
	if d.ManagedEntries, _, err = callHarness[[]types.ManagedEntries](ctx, drv, "mgs_listEntries"); err != nil {
		return fail(err)
	}
	if d.Hooks, _, err = callHarness[[]types.HookCommand](ctx, drv, "mgs_listHooks"); err != nil {
		return fail(err)
	}
	if d.UnsupportedHooks, _, err = callHarness[[]types.HookBinding](ctx, drv, "mgs_listUnsupportedHooks"); err != nil {
		return fail(err)
	}
	if d.MCP, err = callHarnessOptional[types.McpServer](ctx, drv, "mgs_getMcp"); err != nil {
		return fail(err)
	}
	if d.Tools, err = callHarnessOptional[types.HarnessTools](ctx, drv, "mgs_listTools"); err != nil {
		return fail(err)
	}
	if d.Limits, _, err = callHarness[types.Limits](ctx, drv, "mgs_getLimits"); err != nil {
		return fail(err)
	}
	// A harness with no hooks prints nothing anyone reads, so it needs no channels.
	if len(d.Hooks) > 0 || len(d.Limits.Channels) > 0 {
		if _, err := agent.ParseHarnessChannels(d.Limits.Channels); err != nil {
			return fail(fmt.Errorf("mgs_getLimits: %w", err))
		}
	}
	if d.Settings, _, err = callHarness[[]types.Setting](ctx, drv, "mgs_listSettings"); err != nil {
		return fail(err)
	}
	var exported bool
	if d.Prompts, exported, err = callHarness[[]types.Prompt](ctx, drv, "mgs_renderPrompts", ffi.StringMapVal(humanOnlyReasons(ctx))); err != nil {
		return fail(err)
	}
	if !exported {
		return fail(fmt.Errorf("does not export mgs_renderPrompts: return the native approval files, or an empty list when the hook itself prompts"))
	}
	// The model rows come first: the agents magus ships run on the one a row names for the
	// cheapest tier.
	rows, _, err := callHarness[[]types.ModelRow](ctx, drv, "mgs_listModels")
	if err != nil {
		return fail(err)
	}
	models, tierRows, err := validateModelRows(rows)
	if err != nil {
		return fail(fmt.Errorf("mgs_listModels: %w", err))
	}
	d.Models = models
	tierModels, err := declaredTierModels(ctx, id, tierRows)
	if err != nil {
		return fail(fmt.Errorf("mgs_listModels: %w", err))
	}
	model, err := cheapestTierModel(ctx, id, tierModels)
	if err != nil {
		return fail(err)
	}
	shipped, err := shippedAgents(model)
	if err != nil {
		return agent.HarnessDescriptor{}, "", false, err
	}
	if d.Agents, _, err = callHarness[[]types.AgentFile](ctx, drv, "mgs_renderAgents", shipped); err != nil {
		return fail(err)
	}
	return d, "spell:" + id, true, nil
}

// callHarness calls the harness contract function fn on drv's Buzz source with args and
// decodes what it returns strictly, through the generated decode.HarnessFuncs row. exported
// is false, err nil, when drv does not export fn: the capability its host lacks.
func callHarness[Resp any](ctx context.Context, drv spells.Driver, fn string, args ...vm.Value) (resp Resp, exported bool, err error) {
	row, ok := harnessFunc(fn)
	if !ok {
		return resp, false, fmt.Errorf("%s is not a harness contract function", fn)
	}
	fs, ok := drv.(spells.FuncSource)
	if !ok || fs.FuncSource() == "" {
		return resp, false, fmt.Errorf("%s: the spell has no Buzz source to call", fn)
	}
	sess, err := execSpellSource(ctx, fs.FuncSource(), fn)
	if err != nil {
		return resp, false, err
	}
	defer sess.Close()
	export, ok := sess.Exports()[fn]
	if !ok {
		return resp, false, nil
	}
	if !export.IsFun() {
		return resp, true, fmt.Errorf("%s is exported but is not a function", fn)
	}
	rv, err := interp.TimeCall(ctx, interp.ModeSpell, func() (vm.Value, error) {
		return sess.CallValue(ctx, export, args)
	})
	if err != nil {
		return resp, true, fmt.Errorf("%s: %w", fn, err)
	}
	decoded, err := row.Decode(rv)
	if err != nil {
		return resp, true, fmt.Errorf("%w: %s: %w", errMalformedAnswer, fn, err)
	}
	typed, ok := decoded.(Resp)
	if !ok {
		return resp, true, fmt.Errorf("%w: %s decoded as %T, not %T", errMalformedAnswer, fn, decoded, resp)
	}
	return typed, true, nil
}

// callHarnessOptional is callHarness for a function whose absence leaves a nil field rather
// than a zero one, so a reader tells "declares none" from "declares an empty one".
func callHarnessOptional[Resp any](ctx context.Context, drv spells.Driver, fn string) (*Resp, error) {
	resp, exported, err := callHarness[Resp](ctx, drv, fn)
	if err != nil || !exported {
		return nil, err
	}
	return &resp, nil
}

func harnessFunc(name string) (decode.ContractFunc, bool) {
	i := slices.IndexFunc(decode.HarnessFuncs, func(fn decode.ContractFunc) bool { return fn.Name == name })
	if i < 0 {
		return decode.ContractFunc{}, false
	}
	return decode.HarnessFuncs[i], true
}

// shippedAgents is every agent magus ships as mgs_renderAgents takes them, each running on
// model, the host's alias for the cheapest model tier, or "" for the host's default.
func shippedAgents(model string) (vm.Value, error) {
	agents, err := agent.ShippedAgents()
	if err != nil {
		return vm.Null, err
	}
	items := make([]vm.Value, len(agents))
	for i, a := range agents {
		items[i] = bindgen.ObjectShippedAgent(types.ShippedAgent{
			Name: a.Name, Description: a.Description, Instructions: a.Instructions, ReadOnly: a.ReadOnly, Model: model,
		})
	}
	return vm.ListValue(items), nil
}

// cheapestTierModel is the model the agents magus ships run on under harness id: the one
// the workspace's model tiers map for the cheapest tier, else the one id's own model rows
// name for that tier (tierModels, tier to exact model id). A model is never inferred from
// a name.
//
// Empty, and the spell renders no model line, where no model is meant: ctx carries no
// workspace, the workspace turned tiers off, or the harness declares no tier on any row,
// which is how a spell for a host that takes no per-agent model says so. A harness that
// declares tiers and has no row for the cheapest one is an error naming the row to add.
func cheapestTierModel(ctx context.Context, id string, tierModels map[string]string) (string, error) {
	ws, ok := types.WorkspaceFromContext(ctx).(interface {
		ModelTiers(ctx context.Context) *types.ModelTierSet
	})
	if !ok {
		return "", nil
	}
	set := ws.ModelTiers(ctx)
	if set == nil {
		return "", nil
	}
	cheapest, ok := set.At(0)
	if !ok {
		return "", nil
	}
	if alias, err := set.Alias(id, cheapest); err == nil {
		return alias, nil
	}
	if len(tierModels) == 0 {
		return "", nil
	}
	if model, ok := tierModels[cheapest.Name()]; ok {
		return model, nil
	}
	return "", fmt.Errorf("its mgs_listModels rows name a model for tiers %s and none for %s, the cheapest tier of the declaration at %s, so "+
		"add tier = %q to the row of the model its shipped agents should run on, or map %s in that declaration's hosts",
		strings.Join(slices.Sorted(maps.Keys(tierModels)), ", "), cheapest.Name(), set.Site(), cheapest.Name(), id)
}

// harnessTierHosts maps each harness in wired whose model rows declare tiers to its model
// for every case in cases, for the declaration a workspace gets when its magusfile declares
// none. A harness whose rows declare some tiers and not a case is an error naming the
// missing row.
func harnessTierHosts(ctx context.Context, cases, wired []string) (map[string][]string, error) {
	hosts := map[string][]string{}
	for _, id := range wired {
		drv, ok := project.DefaultSpellRegistry().Lookup(id)
		if !ok {
			continue
		}
		rows, _, err := callHarness[[]types.ModelRow](ctx, drv, "mgs_listModels")
		if err != nil {
			return nil, fmt.Errorf("harness spell %q: %w", id, err)
		}
		_, tierRows, err := validateModelRows(rows)
		if err != nil {
			return nil, fmt.Errorf("harness spell %q: mgs_listModels: %w", id, err)
		}
		// No declaration maps a tier yet, so a tier of several rows fails here.
		tiers, err := pickTierModels(tierRows, func(string) (string, bool) { return "", false })
		if err != nil {
			return nil, fmt.Errorf("harness spell %q: mgs_listModels: %w", id, err)
		}
		if len(tiers) == 0 {
			continue
		}
		models := make([]string, len(cases))
		for i, c := range cases {
			model, ok := tiers[c]
			if !ok {
				return nil, fmt.Errorf("harness spell %q: its mgs_listModels rows name no model for tier %s, so add tier = %q to the row of the model that tier runs on",
					id, c, c)
			}
			models[i] = model
		}
		hosts[id] = models
	}
	return hosts, nil
}

// declaredTierModels is each tier's default model under harness id.
// A tier of several rows takes the id the declaration maps for it.
// With no declaration in force, such a tier is left out.
func declaredTierModels(ctx context.Context, id string, tierRows map[string][]string) (map[string]string, error) {
	var set *types.ModelTierSet
	if ws, ok := types.WorkspaceFromContext(ctx).(interface {
		ModelTiers(ctx context.Context) *types.ModelTierSet
	}); ok {
		set = ws.ModelTiers(ctx)
	}
	if set == nil {
		var out map[string]string
		for tier, ids := range tierRows {
			if len(ids) != 1 {
				continue
			}
			if out == nil {
				out = map[string]string{}
			}
			out[tier] = ids[0]
		}
		return out, nil
	}
	return pickTierModels(tierRows, func(tier string) (string, bool) {
		t, err := set.Parse(tier)
		if err != nil {
			return "", false
		}
		alias, err := set.Alias(id, t)
		return alias, err == nil
	})
}

// pickTierModels is each tier's default model from its rows' ids.
// A tier of several rows runs the id mapped returns for it.
// When mapped names none of them it is an error, never a pick by order.
func pickTierModels(tierRows map[string][]string, mapped func(tier string) (string, bool)) (map[string]string, error) {
	if len(tierRows) == 0 {
		return nil, nil
	}
	out := map[string]string{}
	for _, tier := range slices.Sorted(maps.Keys(tierRows)) {
		ids := tierRows[tier]
		if len(ids) == 1 {
			out[tier] = ids[0]
			continue
		}
		if id, ok := mapped(tier); ok && slices.Contains(ids, id) {
			out[tier] = id
			continue
		}
		return nil, fmt.Errorf("rows %s all declare tier %s and the declaration maps none of them for it, so map tier %s to one of those ids in the declaration's hosts",
			strings.Join(ids, ", "), tier, tier)
	}
	return out, nil
}

// validateModelRows reads model rows into prices and each tier's row ids.
// A half-set price step, or an alias without a tier or on two rows, names
// no one model, so each is an error. Several rows may share a tier.
func validateModelRows(rows []types.ModelRow) ([]types.ModelPricing, map[string][]string, error) {
	out := make([]types.ModelPricing, len(rows))
	aliases := map[string]string{}
	var tiers map[string][]string
	for i, r := range rows {
		if strings.TrimSpace(r.ID) == "" {
			return nil, nil, fmt.Errorf("row %d has no id", i+1)
		}
		if r.Alias != "" {
			if r.Tier == "" {
				return nil, nil, fmt.Errorf("row %d (%s) sets alias %s without a tier, so the alias names no tier", i+1, r.ID, r.Alias)
			}
			if prior, dup := aliases[r.Alias]; dup {
				return nil, nil, fmt.Errorf("rows %s and %s both declare alias %s", prior, r.ID, r.Alias)
			}
			aliases[r.Alias] = r.ID
		}
		if r.Tier != "" {
			if tiers == nil {
				tiers = map[string][]string{}
			}
			tiers[r.Tier] = append(tiers[r.Tier], r.ID)
		}
		if (r.Step.Above > 0) != (r.Step.Multiplier > 0) {
			return nil, nil, fmt.Errorf("row %d (%s) sets one of the price step's above and multiplier without the other", i+1, r.ID)
		}
		out[i] = types.ModelPricing{
			ID: r.ID, Tier: r.Tier, Alias: r.Alias, Window: r.Window, Input: types.USDPerMTok(r.Price.Input),
			CacheWrite5m: types.USDPerMTok(r.Price.CacheWrite5m), CacheWrite1h: types.USDPerMTok(r.Price.CacheWrite1h),
			CacheRead: types.USDPerMTok(r.Price.CacheRead), Output: types.USDPerMTok(r.Price.Output),
		}
		if r.Step.Above > 0 {
			step := r.Step
			out[i].LongContext = &step
		}
	}
	return out, tiers, nil
}

// humanOnlyReasons maps each target the workspace on ctx declares human_only to its
// reason, joining the reasons of one name declared in several projects. A host's native
// prompt matches a run by target name alone, so the project is not kept. Empty when ctx
// carries no workspace.
func humanOnlyReasons(ctx context.Context) map[string]string {
	out := map[string]string{}
	ws := types.WorkspaceFromContext(ctx)
	if ws == nil {
		return out
	}
	for _, p := range ws.All() {
		for _, name := range slices.Sorted(maps.Keys(p.TargetPolicies)) {
			pol := p.TargetPolicies[name]
			if !pol.HumanOnly {
				continue
			}
			if prior, ok := out[name]; ok {
				out[name] = fmt.Sprintf("%s; %s", prior, pol.HumanOnlyReason)
				continue
			}
			out[name] = pol.HumanOnlyReason
		}
	}
	return out
}

func displayName(id string) string {
	if id == "" {
		return ""
	}
	parts := strings.Split(id, "-")
	for i, p := range parts {
		if p == "" {
			continue
		}
		parts[i] = strings.ToUpper(p[:1]) + p[1:]
	}
	return strings.Join(parts, " ")
}

func harnessAssetContent(drv spells.Driver, asset types.HarnessAsset) ([]byte, error) {
	if (asset.Source == "") == (asset.Content == "") {
		return nil, fmt.Errorf("asset %s requires exactly one of source and content", asset.Path)
	}
	if asset.Source == "" {
		return []byte(asset.Content), nil
	}
	name, embedded := strings.CutPrefix(asset.Source, "magus:")
	if !fs.ValidPath(name) {
		return nil, fmt.Errorf("asset %s has an invalid source %q", asset.Path, asset.Source)
	}
	var body []byte
	var err error
	if embedded {
		body, err = fs.ReadFile(spells.Shipped(), name)
	} else {
		reader, ok := drv.(interface{ ReadAsset(string) ([]byte, error) })
		if !ok {
			return nil, fmt.Errorf("spell %s cannot read runtime assets", drv.Name())
		}
		body, err = reader.ReadAsset(name)
	}
	if err != nil {
		return nil, fmt.Errorf("asset %s: %w", asset.Path, err)
	}
	return body, nil
}
