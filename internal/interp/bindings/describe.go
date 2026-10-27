package bindings

import (
	"context"
	"fmt"

	"github.com/egladman/magus/internal/hostmodules"
	"github.com/egladman/magus/internal/interp/bindings/ffi"
	bindgen "github.com/egladman/magus/internal/interp/bindings/gen"
	"github.com/egladman/magus/internal/interp/bindings/gen/decode"
	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/gopherbuzz/vm"
	"github.com/egladman/magus/std"
	"github.com/egladman/magus/types"
)

// buildDescribe assembles magus\describe: one typed read per `magus describe` noun.
//
// Hand-bound like buildJob, because a Namespace's methods are Extern by construction.
// Each closure calls the std function for its noun with the call-time ctx and encodes
// the result with the generated boundary encoder, so the record a script reads is the
// one the CLI's -o json prints. withheld is the top-level magus members the client
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
		opts, err := describeOptions("file", args, 1)
		if err != nil {
			return vm.Null, err
		}
		report, err := std.MagusDescribeFile(ctx, ffi.StringSlice(args, 0), opts)
		return bindgen.ObjectFileReport(report), err
	})
	// In-process rather than forked: the registry is this process's own, and
	// hostmodules.Describe already returns the collection `describe module` prints.
	bind("module", func(_ context.Context, args []vm.Value) (vm.Value, error) {
		name := ffi.String(args, 0)
		out := dropMagusMethods(hostmodules.Describe(name), withheld)
		if name != "" && len(out) == 0 {
			return vm.Null, fmt.Errorf("magus.describe.module: unknown module %q", name)
		}
		return ffi.ObjectSlice(out, bindgen.ObjectModuleEntry), nil
	})
	bind("spell", func(ctx context.Context, args []vm.Value) (vm.Value, error) {
		opts, err := describeOptions("spell", args, 1)
		if err != nil {
			return vm.Null, err
		}
		out, err := std.MagusDescribeSpell(ctx, ffi.String(args, 0), opts)
		return ffi.ObjectSlice(out, bindgen.ObjectSpell), err
	})
	bind("charm", func(ctx context.Context, args []vm.Value) (vm.Value, error) {
		opts, err := describeOptions("charm", args, 1)
		if err != nil {
			return vm.Null, err
		}
		out, err := std.MagusDescribeCharm(ctx, ffi.String(args, 0), opts)
		return ffi.ObjectSlice(out, bindgen.ObjectCharmEntry), err
	})
	bind("target", func(ctx context.Context, args []vm.Value) (vm.Value, error) {
		opts, err := describeOptions("target", args, 0)
		if err != nil {
			return vm.Null, err
		}
		out, err := std.MagusDescribeTarget(ctx, opts)
		return ffi.ObjectSlice(out, bindgen.ObjectTargetEntry), err
	})
	bind("evaluatedTarget", func(ctx context.Context, args []vm.Value) (vm.Value, error) {
		opts, err := describeOptions("evaluatedTarget", args, 1)
		if err != nil {
			return vm.Null, err
		}
		out, err := std.MagusDescribeEvaluatedTarget(ctx, ffi.String(args, 0), opts)
		return ffi.ObjectSlice(out, bindgen.ObjectEvaluatedTarget), err
	})
	bind("project", func(ctx context.Context, _ []vm.Value) (vm.Value, error) {
		out, err := std.MagusDescribeProject(ctx)
		return bindgen.ObjectProjectsOutput(out), err
	})
	bind("evaluatedProject", func(ctx context.Context, args []vm.Value) (vm.Value, error) {
		opts, err := describeOptions("evaluatedProject", args, 1)
		if err != nil {
			return vm.Null, err
		}
		out, err := std.MagusDescribeEvaluatedProject(ctx, ffi.String(args, 0), opts)
		return ffi.ObjectSlice(out, bindgen.ObjectEvaluatedProject), err
	})
	bind("graph", func(ctx context.Context, args []vm.Value) (vm.Value, error) {
		opts, err := describeOptions("graph", args, 0)
		if err != nil {
			return vm.Null, err
		}
		out, err := std.MagusDescribeGraph(ctx, opts)
		return bindgen.ObjectTargetGraphOutput(out), err
	})
	bind("graphMarkdown", func(ctx context.Context, args []vm.Value) (vm.Value, error) {
		opts, err := describeOptions("graphMarkdown", args, 1)
		if err != nil {
			return vm.Null, err
		}
		out, err := std.MagusDescribeGraphMarkdown(ctx, ffi.StringSlice(args, 0), opts)
		return ffi.StringVal(out), err
	})
	bind("workspace", func(ctx context.Context, args []vm.Value) (vm.Value, error) {
		opts, err := describeOptions("workspace", args, 0)
		if err != nil {
			return vm.Null, err
		}
		out, err := std.MagusDescribeWorkspace(ctx, opts)
		return ffi.ObjectSlice(out, bindgen.ObjectWorkspaceEntry), err
	})
	bind("tool", func(ctx context.Context, _ []vm.Value) (vm.Value, error) {
		out, err := std.MagusDescribeTool(ctx)
		return bindgen.ObjectToolReport(out), err
	})
	bind("rule", func(ctx context.Context, args []vm.Value) (vm.Value, error) {
		opts, err := describeOptions("rule", args, 1)
		if err != nil {
			return vm.Null, err
		}
		out, err := std.MagusDescribeRule(ctx, ffi.String(args, 0), opts)
		return ffi.ObjectSlice(out, bindgen.ObjectRuleDoc), err
	})
	bind("harness", func(ctx context.Context, args []vm.Value) (vm.Value, error) {
		out, err := std.MagusDescribeHarness(ctx, ffi.String(args, 0))
		return bindgen.ObjectHarnessPlan(out), err
	})
	bind("mcpTool", func(ctx context.Context, args []vm.Value) (vm.Value, error) {
		opts, err := describeOptions("mcpTool", args, 1)
		if err != nil {
			return vm.Null, err
		}
		out, err := std.MagusDescribeMCPTool(ctx, ffi.String(args, 0), opts)
		return ffi.ObjectSlice(out, bindgen.ObjectMCPTool), err
	})
	return d
}

// describeOptions decodes the CommandOptions a forking describe member takes at arg n,
// strictly: an unknown or mistyped member raises. An omitted one is the zero record.
func describeOptions(member string, args []vm.Value, n int) (types.MagusCommandOptions, error) {
	if !ffi.Present(args, n) {
		return types.MagusCommandOptions{}, nil
	}
	return ffi.Object(`magus\describe.`+member, "opts", args, n, decode.DecodeMagusCommandOptions)
}
