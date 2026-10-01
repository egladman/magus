package bindings

import (
	"context"
	"fmt"

	"github.com/egladman/magus/internal/hostmodules"
	"github.com/egladman/magus/internal/interp/bindings/ffi"
	bindinggen "github.com/egladman/magus/internal/interp/bindings/gen"
	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/gopherbuzz/vm"
	"github.com/egladman/magus/std"
)

// buildDescribe assembles magus\describe: one typed read per `magus describe` noun.
//
// Hand-bound like buildJob, because a Namespace's methods are Extern by construction.
// Each closure calls the std function for its noun with the call-time ctx and encodes
// the result with the generated boundary encoder, so the record a script reads is the
// one the CLI's -o json prints. withheld is the top-level magus members the surface
// does not offer, which describe.module("magus") must not list either.
func buildDescribe(obs buzz.DirectObserver, withheld []string) vm.Value {
	d := vm.NewMap()
	bind := func(member string, fn func(ctx context.Context, args []vm.Value) (vm.Value, error)) {
		d.MapSet(member, directVal(obs, "magus.describe."+member, func(ctx context.Context, args []vm.Value) (vm.Value, error) {
			v, err := fn(ctx, args)
			if err != nil {
				return vm.Null, ffi.Error(err)
			}
			return v, nil
		}))
	}
	bind("file", func(ctx context.Context, args []vm.Value) (vm.Value, error) {
		report, err := std.MagusDescribeFile(ctx, ffi.StrSlice(args, 0), ffi.AnyMap(args, 1))
		return bindinggen.ObjectFileReport(report), err
	})
	// In-process rather than forked: the registry is this process's own, and
	// hostmodules.Describe already returns the collection `describe module` prints.
	bind("module", func(_ context.Context, args []vm.Value) (vm.Value, error) {
		name := ffi.Str(args, 0)
		out := dropMagusMethods(hostmodules.Describe(name), withheld)
		if name != "" && len(out) == 0 {
			return vm.Null, fmt.Errorf("magus.describe.module: unknown module %q", name)
		}
		return ffi.ObjectSlice(out, bindinggen.ObjectModuleEntry), nil
	})
	bind("spell", func(ctx context.Context, args []vm.Value) (vm.Value, error) {
		out, err := std.MagusDescribeSpell(ctx, ffi.Str(args, 0), ffi.AnyMap(args, 1))
		return ffi.ObjectSlice(out, bindinggen.ObjectSpell), err
	})
	bind("charm", func(ctx context.Context, args []vm.Value) (vm.Value, error) {
		out, err := std.MagusDescribeCharm(ctx, ffi.Str(args, 0), ffi.AnyMap(args, 1))
		return ffi.ObjectSlice(out, bindinggen.ObjectCharmEntry), err
	})
	bind("target", func(ctx context.Context, args []vm.Value) (vm.Value, error) {
		out, err := std.MagusDescribeTarget(ctx, ffi.AnyMap(args, 0))
		return ffi.ObjectSlice(out, bindinggen.ObjectTargetEntry), err
	})
	bind("evaluatedTarget", func(ctx context.Context, args []vm.Value) (vm.Value, error) {
		out, err := std.MagusDescribeEvaluatedTarget(ctx, ffi.Str(args, 0), ffi.AnyMap(args, 1))
		return ffi.ObjectSlice(out, bindinggen.ObjectEvaluatedTarget), err
	})
	bind("project", func(ctx context.Context, _ []vm.Value) (vm.Value, error) {
		out, err := std.MagusDescribeProject(ctx)
		return bindinggen.ObjectProjectsOutput(out), err
	})
	bind("evaluatedProject", func(ctx context.Context, args []vm.Value) (vm.Value, error) {
		out, err := std.MagusDescribeEvaluatedProject(ctx, ffi.Str(args, 0), ffi.AnyMap(args, 1))
		return ffi.ObjectSlice(out, bindinggen.ObjectEvaluatedProject), err
	})
	bind("graph", func(ctx context.Context, args []vm.Value) (vm.Value, error) {
		out, err := std.MagusDescribeGraph(ctx, ffi.AnyMap(args, 0))
		return bindinggen.ObjectTargetGraphOutput(out), err
	})
	bind("graphMarkdown", func(ctx context.Context, args []vm.Value) (vm.Value, error) {
		out, err := std.MagusDescribeGraphMarkdown(ctx, ffi.StrSlice(args, 0), ffi.AnyMap(args, 1))
		return ffi.StrVal(out), err
	})
	bind("workspace", func(ctx context.Context, args []vm.Value) (vm.Value, error) {
		out, err := std.MagusDescribeWorkspace(ctx, ffi.AnyMap(args, 0))
		return ffi.ObjectSlice(out, bindinggen.ObjectWorkspaceEntry), err
	})
	bind("tool", func(ctx context.Context, _ []vm.Value) (vm.Value, error) {
		out, err := std.MagusDescribeTool(ctx)
		return bindinggen.ObjectToolReport(out), err
	})
	bind("rule", func(ctx context.Context, args []vm.Value) (vm.Value, error) {
		out, err := std.MagusDescribeRule(ctx, ffi.Str(args, 0), ffi.AnyMap(args, 1))
		return ffi.ObjectSlice(out, bindinggen.ObjectRuleDoc), err
	})
	bind("harness", func(ctx context.Context, args []vm.Value) (vm.Value, error) {
		out, err := std.MagusDescribeHarness(ctx, ffi.Str(args, 0), ffi.AnyMap(args, 1))
		return ffi.ObjectSlice(out, bindinggen.ObjectHarnessPlan), err
	})
	bind("mcpTool", func(ctx context.Context, args []vm.Value) (vm.Value, error) {
		out, err := std.MagusDescribeMCPTool(ctx, ffi.Str(args, 0), ffi.AnyMap(args, 1))
		return ffi.ObjectSlice(out, bindinggen.ObjectMCPTool), err
	})
	return d
}
