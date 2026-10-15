package bindings

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/interp"
	"github.com/egladman/magus/internal/interp/bindings/ffi"
	bindinggen "github.com/egladman/magus/internal/interp/bindings/gen"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/parsecache"
	"github.com/egladman/magus/internal/workspace"
	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/gopherbuzz/vm"
	"github.com/egladman/magus/types"
)

const (
	modelTiersMember = `magus\job.modelTiers`
	modelTierMember  = `magus\job.modelTier`

	tierEnumGlobal   = "__magusModelTierEnum"
	skillVariantEnum = "SkillVariant"
	harnessKeyObject = "HarnessKey"
)

// defaultModelTiersSource makes the call a magusfile makes, so the shipped declaration
// and a copy of it are checked alike.
const defaultModelTiersSource = `import "magus/modeltiers" as modeltiers;
declareDefault(modeltiers\ModelTier, modeltiers\defaultTier, modeltiers\bounds, modeltiers\skills);
`

// registerModelTiers binds magus\job.modelTiers onto jobMap:
//
//	final cc = magus\harness.provider(claudecode);
//	magus\job.modelTiers(ModelTier, default: ModelTier.standard,
//	    hosts: {cc: claudeModel}, bounds: bounds, skills: skills);
//
// The functions run here, once per case, and are not kept. Every misdeclaration is
// MGS1052, so a declaration that failed to register cannot leave a workspace that reads
// as tiered. magus\job.modelTiers(null) turns tiers off; with no call the workspace gets
// the shipped magus/modeltiers declaration, built when a fork first asks for it.
// Nothing declared here selects a model at run time.
func registerModelTiers(ctx context.Context, sess *buzz.Session, obs buzz.DirectObserver, jobMap vm.Value) {
	if reg := workspace.WorkspaceRegistryFromContext(ctx); reg != nil {
		// The context outlives the load that built it: a fork asks long after.
		defaultCtx := context.WithoutCancel(ctx)
		reg.SetDefaultModelTiers(func() (*types.ModelTierSet, error) { return defaultModelTierSet(defaultCtx, reg.Harnesses()) })
	}
	// Per session, not per registry, as registerFunctionRule counts.
	registered := false
	jobMap.MapSet("modelTiers", directVal(obs, "magus.job.modelTiers", func(callCtx context.Context, args []vm.Value) (vm.Value, error) {
		reg, err := modelTierCallSite(ctx, modelTiersMember, "the tiers the workspace's jobs run at")
		if err != nil {
			return vm.Null, err
		}
		if registered {
			return vm.Null, modelTierUnusable(modelTiersMember,
				"already declared in this magusfile, and a workspace has one declaration; fold the second one into the first")
		}
		registered = true
		arg := func(i int) vm.Value {
			if i < len(args) {
				return args[i]
			}
			return vm.Null
		}
		if arg(0).IsNull() {
			for i := 1; i < 5; i++ {
				if !arg(i).IsNull() {
					return vm.Null, modelTierUnusable(modelTiersMember,
						"null turns model tiers off and takes no other argument, got %s as argument %d", arg(i).Kind(), i+1)
				}
			}
			reg.SetModelTiers(nil)
			return vm.Null, nil
		}
		decl, err := declareModelTiers(callCtx, sess, arg(0), arg(1), arg(2), arg(3), arg(4))
		if err != nil {
			return vm.Null, err
		}
		decl.Site = modelTierSite(ctx)
		if err := checkWiredHarnesses(decl, reg.Harnesses()); err != nil {
			return vm.Null, err
		}
		set, err := types.NewModelTierSet(decl)
		if err != nil {
			return vm.Null, modelTierUnusable(modelTiersMember, "%v", err)
		}
		reg.SetModelTiers(set)
		return vm.Null, nil
	}))
}

// registerModelTierMatrix binds magus\job.modelTier onto jobMap:
//
//	magus\job.modelTier(fun(req: ModelTierRequest) > Choice? { ... });
//
// A fork calls the function once when the row names neither a tier nor a model; null
// leaves the declared default to decide. The function is kept, not called, here.
func registerModelTierMatrix(ctx context.Context, sess *buzz.Session, obs buzz.DirectObserver, jobMap vm.Value) {
	registered := false
	jobMap.MapSet("modelTier", directVal(obs, "magus.job.modelTier", func(_ context.Context, args []vm.Value) (vm.Value, error) {
		reg, err := modelTierCallSite(ctx, modelTierMember, "the tier a job is assigned")
		if err != nil {
			return vm.Null, err
		}
		if len(args) != 1 || !args[0].IsFun() {
			return vm.Null, modelTierUnusable(modelTierMember,
				"expected one function, fun(req: ModelTierRequest) > any")
		}
		if registered {
			return vm.Null, modelTierUnusable(modelTierMember,
				"already registered in this magusfile, and a workspace has one matrix; fold the second one into the first")
		}
		registered = true
		reg.SetModelTierMatrix(buzzModelTierMatrix(sess, reg, args[0], modelTierSite(ctx)))
		// The fork calls it after this load has closed.
		reg.Keep(sess.Keep())
		return vm.Null, nil
	}))
}

func modelTierCallSite(ctx context.Context, member, declares string) (*workspace.WorkspaceRegistry, error) {
	if path, ok := interp.ProjectPathFromContext(ctx); ok && path != "" && path != "." {
		return nil, modelTierUnusable(member,
			"called from the magusfile of %s; declare it in the root magusfile, where %s is decided for the whole workspace", path, declares)
	}
	reg := workspace.WorkspaceRegistryFromContext(ctx)
	if reg == nil {
		return nil, types.DiagnosticErrorf(types.MagusfileOnlyMember,
			"%s: only callable from a magusfile, not from a spell or a `magus buzz` script - it declares into the workspace magus is loading, and neither has one", member)
	}
	return reg, nil
}

func modelTierUnusable(member, format string, args ...any) error {
	return types.DiagnosticErrorf(types.ModelTierDeclarationUnusable, "%s: %s", member, fmt.Sprintf(format, args...))
}

// modelTierSite is the magusfile alone: a host call is not told its line.
func modelTierSite(ctx context.Context) string {
	if src := interp.SourceFromContext(ctx); src != nil && len(src.Files) > 0 {
		return filepath.Base(src.Files[0])
	}
	return "magusfile"
}

// checkWiredHarnesses reads the wiring as it stands, so the declaration must follow the
// magus\harness.provider calls it maps.
func checkWiredHarnesses(decl types.ModelTierDeclaration, wired []string) error {
	var missing []string
	for key := range decl.Hosts {
		if !slices.Contains(wired, key.ID) {
			missing = append(missing, key.ID)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	slices.Sort(missing)
	wiredText := "none"
	if len(wired) > 0 {
		wiredText = strings.Join(wired, ", ")
	}
	return modelTierUnusable(modelTiersMember,
		"hosts maps %s, which the magusfile has not wired with magus\\harness.provider (wired so far: %s); wire it above this call",
		strings.Join(missing, ", "), wiredText)
}

// declareModelTiers calls hosts, bounds and skills once per case and checks identity,
// never a name: tiers is an enum definition, default one of its cases, each hosts key a
// magus\HarnessKey. Every problem is MGS1052. The caller sets Site.
func declareModelTiers(ctx context.Context, sess *buzz.Session, tiers, def, hosts, bounds, skills vm.Value) (types.ModelTierDeclaration, error) {
	enum, cases, ok := tiers.EnumDef()
	if !ok {
		return types.ModelTierDeclaration{}, modelTierUnusable(modelTiersMember,
			"tiers must be an enum, such as enum ModelTier { economy, standard, principal }, cheapest first, got %s", tiers.Kind())
	}
	if len(cases) == 0 {
		return types.ModelTierDeclaration{}, modelTierUnusable(modelTiersMember, "enum %s declares no cases", enum)
	}
	defEnum, defCase, ok := def.EnumCase()
	if !ok || defEnum != enum || !slices.Contains(cases, defCase) {
		return types.ModelTierDeclaration{}, modelTierUnusable(modelTiersMember,
			"default must be a case of %s (%s), got %s", enum, strings.Join(cases, ", "), describeValue(def))
	}
	if !bounds.IsFun() {
		return types.ModelTierDeclaration{}, modelTierUnusable(modelTiersMember,
			"bounds must be a function from a %s to magus\\ModelTierBounds, got %s", enum, bounds.Kind())
	}
	if !skills.IsFun() {
		return types.ModelTierDeclaration{}, modelTierUnusable(modelTiersMember,
			"skills must be a function from a %s to magus\\SkillVariant, got %s", enum, skills.Kind())
	}
	caseValues, err := enumCaseValues(ctx, sess, tiers, cases)
	if err != nil {
		return types.ModelTierDeclaration{}, modelTierUnusable(modelTiersMember, "reading the cases of %s: %v", enum, err)
	}

	decl := types.ModelTierDeclaration{
		Enum:    enum,
		Cases:   cases,
		Default: slices.Index(cases, defCase),
		Bounds:  make([]types.ModelTierBounds, len(cases)),
		Skills:  make([]types.SkillVariant, len(cases)),
	}
	for i, tier := range caseValues {
		rv, err := sess.CallValue(ctx, bounds, []vm.Value{tier})
		if err != nil {
			return types.ModelTierDeclaration{}, modelTierUnusable(modelTiersMember, "bounds raised for %s.%s: %v", enum, cases[i], err)
		}
		if decl.Bounds[i], err = decodeTierBounds(enum, cases[i], rv); err != nil {
			return types.ModelTierDeclaration{}, err
		}
		rv, err = sess.CallValue(ctx, skills, []vm.Value{tier})
		if err != nil {
			return types.ModelTierDeclaration{}, modelTierUnusable(modelTiersMember, "skills raised for %s.%s: %v", enum, cases[i], err)
		}
		if decl.Skills[i], err = decodeSkillVariant(rv); err != nil {
			return types.ModelTierDeclaration{}, modelTierUnusable(modelTiersMember, "skills for %s.%s: %v", enum, cases[i], err)
		}
	}
	if !hosts.IsNull() {
		if decl.Hosts, err = declareTierHosts(ctx, sess, enum, cases, caseValues, hosts); err != nil {
			return types.ModelTierDeclaration{}, err
		}
	}
	return decl, nil
}

// declareTierHosts reads a declared hosts map; the caller skips a declaration with none.
func declareTierHosts(ctx context.Context, sess *buzz.Session, enum string, cases []string, caseValues []vm.Value, hosts vm.Value) (map[types.HarnessKey][]string, error) {
	if !hosts.IsMap() {
		return nil, modelTierUnusable(modelTiersMember,
			"hosts must be a map from a magus\\HarnessKey to a function from a %s to a model alias, got %s", enum, hosts.Kind())
	}
	keys, fns := hosts.MapKeyValues(), hosts.MapValues()
	out := make(map[types.HarnessKey][]string, len(keys))
	for i, keyValue := range keys {
		key, err := decodeHarnessKey(keyValue)
		if err != nil {
			return nil, err
		}
		if _, dup := out[key]; dup {
			return nil, modelTierUnusable(modelTiersMember, "hosts maps harness %s twice", key.ID)
		}
		if !fns[i].IsFun() {
			return nil, modelTierUnusable(modelTiersMember,
				"hosts for %s must be a function from a %s to a model alias, got %s", key.ID, enum, fns[i].Kind())
		}
		aliases := make([]string, len(cases))
		for j, tier := range caseValues {
			rv, err := sess.CallValue(ctx, fns[i], []vm.Value{tier})
			if err != nil {
				return nil, modelTierUnusable(modelTiersMember, "hosts for %s raised for %s.%s: %v", key.ID, enum, cases[j], err)
			}
			if !rv.IsStr() {
				return nil, modelTierUnusable(modelTiersMember,
					"hosts for %s answered %s for %s.%s, and a model alias is a str", key.ID, rv.Kind(), enum, cases[j])
			}
			aliases[j] = rv.AsString()
		}
		out[key] = aliases
	}
	return out, nil
}

// decodeHarnessKey reads a HarnessKey as magus\harness.provider returns it (a map with an
// id) or as a magusfile writes it (an instance).
func decodeHarnessKey(v vm.Value) (types.HarnessKey, error) {
	if v.ObjectTypeName() != harnessKeyObject && !v.IsMap() {
		return types.HarnessKey{}, modelTierUnusable(modelTiersMember,
			"a hosts key must be the magus\\HarnessKey magus\\harness.provider returns, got %s", describeValue(v))
	}
	fields, _ := v.MapView()
	id, _ := fields.MapGet("id")
	if !id.IsStr() || strings.TrimSpace(id.AsString()) == "" {
		return types.HarnessKey{}, modelTierUnusable(modelTiersMember, "a magus\\HarnessKey in hosts names no harness: its id is empty")
	}
	return types.HarnessKey{ID: id.AsString()}, nil
}

func decodeTierBounds(enum, tier string, v vm.Value) (types.ModelTierBounds, error) {
	fields, ok := v.MapView()
	if !ok {
		return types.ModelTierBounds{}, modelTierUnusable(modelTiersMember,
			"bounds answered %s for %s.%s, and it must answer a magus\\ModelTierBounds", v.Kind(), enum, tier)
	}
	var out types.ModelTierBounds
	for _, field := range []struct {
		name string
		into func(int64)
	}{
		{"maxLive", func(n int64) { out.MaxLive = int(n) }},
		{"maxContext", func(n int64) { out.MaxContext = n }},
	} {
		fv, present := fields.MapGet(field.name)
		if !present || fv.IsNull() {
			continue
		}
		if !fv.IsInt() {
			return types.ModelTierBounds{}, modelTierUnusable(modelTiersMember,
				"bounds for %s.%s has a %s %s, and it is an int", enum, tier, fv.Kind(), field.name)
		}
		field.into(fv.AsInt())
	}
	return out, nil
}

func decodeSkillVariant(v vm.Value) (types.SkillVariant, error) {
	enum, name, ok := v.EnumCase()
	if !ok || enum != skillVariantEnum {
		return "", fmt.Errorf("%s is not a magus\\SkillVariant, short or full", describeValue(v))
	}
	variant := types.SkillVariant(name)
	if !variant.Valid() {
		return "", fmt.Errorf("%q is not a skill variant, and the variants are %s", name, strings.Join(variant.Values(), ", "))
	}
	return variant, nil
}

func describeValue(v vm.Value) string {
	if enum, name, ok := v.EnumCase(); ok {
		return enum + "." + name
	}
	return v.Kind()
}

// enumCaseValues reads the cases in sess, the session that holds def: a case is interned
// on its definition, so one made by another session would not outlive that session.
func enumCaseValues(ctx context.Context, sess *buzz.Session, def vm.Value, cases []string) ([]vm.Value, error) {
	var src strings.Builder
	src.WriteString("return [")
	for i, c := range cases {
		if i > 0 {
			src.WriteString(", ")
		}
		src.WriteString(tierEnumGlobal + "." + c)
	}
	src.WriteString("];")
	sess.SetGlobal(tierEnumGlobal, def)
	list, err := sess.Eval(ctx, src.String())
	sess.SetGlobal(tierEnumGlobal, vm.Null)
	if err != nil {
		return nil, err
	}
	if !list.IsList() || len(list.ListItems()) != len(cases) {
		return nil, fmt.Errorf("expected %d cases, got %s", len(cases), list.Kind())
	}
	return slices.Clone(list.ListItems()), nil
}

// defaultModelTierSet maps each harness in wired whose model rows declare tiers to those
// rows' exact model ids (harnessTierHosts), and no other harness.
func defaultModelTierSet(ctx context.Context, wired []string) (*types.ModelTierSet, error) {
	// No registry on this session: it declares nothing into the workspace.
	ctx = workspace.ContextWithRegistry(ctx, nil)
	sess := buzz.NewSession(ctx, buzz.WithEmbedded(), buzz.WithParseCache(parsecache.Shared()))
	defer sess.Close()
	RegisterMagusNamespace(ctx, sess)
	registerMagusModules(ctx, sess)

	var (
		decl     types.ModelTierDeclaration
		declared bool
	)
	sess.SetGlobal("declareDefault", vm.DirectValue("declareDefault", func(callCtx context.Context, args []vm.Value) (vm.Value, error) {
		if len(args) != 4 {
			return vm.Null, errors.New("declareDefault: expected the enum, its default, bounds and skills")
		}
		var err error
		decl, err = declareModelTiers(callCtx, sess, args[0], args[1], vm.Null, args[2], args[3])
		declared = err == nil
		return vm.Null, err
	}))
	if err := sess.Exec(ctx, defaultModelTiersSource); err != nil {
		return nil, fmt.Errorf("magus/modeltiers: %w", err)
	}
	if !declared {
		return nil, errors.New("magus/modeltiers: the module declared nothing")
	}
	decl.Site = "magus/modeltiers"
	hosts, err := harnessTierHosts(ctx, decl.Cases, wired)
	if err != nil {
		return nil, fmt.Errorf("magus/modeltiers: %w", err)
	}
	decl.Hosts = hosts
	return types.NewModelTierSet(decl)
}

// buzzModelTierMatrix checks the answer against the declaration in force when it is
// called, since the modelTiers and modelTier calls may come in either order.
func buzzModelTierMatrix(sess *buzz.Session, reg *workspace.WorkspaceRegistry, matrix vm.Value, site string) types.ModelTierMatrix {
	return func(ctx context.Context, req types.ModelTierRequest) (choice *types.JobModelTier, err error) {
		set := reg.ModelTiers(ctx)
		if set == nil {
			return nil, fmt.Errorf("%s: the workspace has no model tiers to choose among: magus\\job.modelTiers(null) turned them off", modelTierMember)
		}
		// The fork runs the matrix inside the store's own write, so a read of the store
		// would wait on itself: every magus\job member raises under a failed snapshot.
		ctx = job.WithSnapshot(ctx, job.Snapshot{Err: errors.New(
			`magus\job: the job store is read-only inside magus\job.modelTier: decide from the request and its history`)})
		defer func() {
			if r := recover(); r != nil {
				choice, err = nil, fmt.Errorf("%s: the matrix faulted the interpreter: %v", modelTierMember, r)
			}
		}()
		reqValue := bindinggen.ObjectModelTierRequest(req, ffi.SessionEnums(ctx, sess, "magus"))
		rv, err := sess.CallValue(ctx, matrix, []vm.Value{reqValue})
		if err != nil {
			return nil, fmt.Errorf("%s: the matrix raised: %w", modelTierMember, err)
		}
		return decodeModelTierChoice(set, rv, site)
	}
}

func decodeModelTierChoice(set *types.ModelTierSet, v vm.Value, site string) (*types.JobModelTier, error) {
	if v.IsNull() {
		return nil, nil //nolint:nilnil // documented: nil leaves the declared default to decide
	}
	fields, ok := v.MapView()
	if !ok {
		return nil, fmt.Errorf("%s: the matrix returned %s, not null or an object with tier, skills and reason", modelTierMember, v.Kind())
	}
	tierValue, present := fields.MapGet("tier")
	if !present || tierValue.IsNull() {
		return nil, fmt.Errorf("%s: the matrix's answer has no tier, a case of the model tier enum", modelTierMember)
	}
	_, caseName, ok := tierValue.EnumCase()
	if !ok {
		return nil, fmt.Errorf("%s: the matrix's tier is %s, not a case of the model tier enum", modelTierMember, tierValue.Kind())
	}
	tier, err := set.Parse(caseName)
	if err != nil {
		return nil, fmt.Errorf("%s: the matrix's tier: %w", modelTierMember, err)
	}
	skillsValue, present := fields.MapGet("skills")
	if !present || skillsValue.IsNull() {
		return nil, fmt.Errorf("%s: the matrix's answer has no skills, a magus\\SkillVariant", modelTierMember)
	}
	skills, err := decodeSkillVariant(skillsValue)
	if err != nil {
		return nil, fmt.Errorf("%s: the matrix's skills: %w", modelTierMember, err)
	}
	reasonValue, present := fields.MapGet("reason")
	if !present || !reasonValue.IsStr() || strings.TrimSpace(reasonValue.AsString()) == "" {
		return nil, fmt.Errorf("%s: the matrix's answer has no reason, and every assignment records why", modelTierMember)
	}
	return &types.JobModelTier{
		Name:   tier.Name(),
		Rank:   tier.Rank(),
		Skills: skills,
		Source: types.ModelTierSourceMatrix,
		Reason: strings.TrimSpace(reasonValue.AsString()),
		Site:   site,
	}, nil
}
