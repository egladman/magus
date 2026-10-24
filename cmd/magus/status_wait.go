package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/egladman/magus"
	"github.com/egladman/magus/broker"
	flaggen "github.com/egladman/magus/cmd/magus/gen"
	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/log/attr"
	"github.com/egladman/magus/internal/pipe"
	"github.com/egladman/magus/internal/workspace"
	"github.com/egladman/magus/types"
)

// awaitBudget is `status --wait`. The gate goes up before anything slow, since the run
// reading the pipe looks for it once, on its way to its locks. A magus reading stdout
// gets the seat reserved for it; anything else is only told the budget has room.
func awaitBudget(ctx context.Context, rootOverride string) error {
	if root, err := workspace.FindRoot(rootOverride); err == nil {
		pipe.Gate(root, globalCfg)
	}
	start := time.Now()
	waited := false
	waiting := func(msg string) {
		waited = true
		slog.InfoContext(ctx, msg, attr.Notice(""), attr.Component("magus"), attr.Elapsed(time.Since(start)))
	}
	reader, argv, _ := pipe.ReaderArgv(ctx, os.Stdout)
	claims := pipedRunClaims(ctx, rootOverride, argv)
	weight := jobWeightAt(ctx, rootOverride)
	for i := range claims {
		claims[i].Weight = weight
	}
	var err error
	if reader == 0 {
		err = cache.AwaitMachine(ctx, queryCapacity, claims, waiting)
	} else {
		err = reserveSeat(ctx, reader, argv, claims, waiting)
	}
	if err == nil && waited {
		slog.InfoContext(ctx, fmt.Sprintf("the build budget can seat it now, after %s", time.Since(start).Round(time.Second)), attr.Notice(""), attr.Component("magus"))
	}
	return err
}

// reserveSeat waits until the broker can seat claims and has it hold the seat for the
// magus reader, whose command argv is. With no broker, or one shutting down, nothing
// holds a seat and the run admits itself; that is said rather than assumed.
func reserveSeat(ctx context.Context, reader int, argv []string, claims []types.MachineClaim, waiting func(string)) error {
	client := broker.NewClient(broker.DefaultAddr())
	defer func() { _ = client.Close() }()
	command := strings.Join(argv, " ")
	reserve := func(ctx context.Context, c types.MachineClaim, reader int) (types.MachineVerdict, error) {
		ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		c.Command = command
		return client.Reserve(ctx, c, reader)
	}
	err := cache.ReserveMachine(ctx, reserve, claims, reader, waiting)
	var refused *broker.Error
	switch {
	case errors.Is(err, broker.ErrUnavailable), errors.As(err, &refused) && refused.Code == broker.CodeDraining:
		slog.InfoContext(ctx, fmt.Sprintf("no seat is reserved for pid %d; it asks the budget itself when it starts", reader),
			attr.Error(err), attr.Notice(""), attr.Component("magus"))
		return nil
	case errors.As(err, &refused) && refused.Code == broker.CodeUnknownType:
		return fmt.Errorf("%w: the running broker predates seat reservations, so `%s` stops it and the next run starts a current one", err, hint.BinaryName()+" broker stop")
	}
	return err
}

// jobWeightAt is the weight of the job this caller acts under in the workspace at
// rootOverride, 0 when it acts under none.
func jobWeightAt(ctx context.Context, rootOverride string) float64 {
	root, err := workspace.FindRoot(rootOverride)
	if err != nil {
		return 0
	}
	cacheDir, err := magus.ResolveCacheDir(root, workspace.WithLoadedConfig(globalCfg))
	if err != nil {
		return 0
	}
	return cache.LeaseWeight(ctx, cacheDir)
}

func queryCapacity(ctx context.Context) (types.MachineSnapshot, error) {
	st, err := queryBroker(ctx)
	return st.Capacity, err
}

// pipedRunClaims is what the magus run or affected run whose argv is argv will claim,
// read the way that command reads its own. Any other command reading stdout waits for one
// free slot. A run this cannot size also waits for one slot, and says so; it reports its
// own error once it starts.
func pipedRunClaims(ctx context.Context, rootOverride string, argv []string) []types.MachineClaim {
	claims, fellBack := pipedClaims(ctx, rootOverride, argv)
	if fellBack != "" {
		slog.InfoContext(ctx, "waiting for one free slot, since the piped command's claim could not be sized: "+fellBack,
			attr.Notice(""), attr.Component("magus"))
	}
	return claims
}

// pipedClaims is pipedRunClaims with the reason it fell back to one slot, empty when it
// sized the command or the command claims nothing of its own.
func pipedClaims(ctx context.Context, rootOverride string, argv []string) (claims []types.MachineClaim, fellBack string) {
	slot := []types.MachineClaim{{Slots: 1}}
	fallBack := func(format string, args ...any) ([]types.MachineClaim, string) {
		return slot, fmt.Sprintf(format, args...)
	}
	if len(argv) == 0 {
		return slot, ""
	}
	sub, subArgs := peekSub(argv[1:])
	var prescan func(*flag.FlagSet)
	switch sub {
	case "run":
		if runReadsPlan(subArgs) {
			return fallBack("run reads its plan from stdin")
		}
		prescan = func(fs *flag.FlagSet) { bindRunFlags(fs, nil) }
	case "affected":
	default:
		return slot, ""
	}
	raw, rest, ok := splitTargetFromArgs(subArgs, prescan)
	if !ok {
		return fallBack("%s names no target", sub)
	}
	_, name := parseTarget(raw)
	t, err := types.ParseTarget(name)
	if err != nil {
		return fallBack("%s: %v", sub, err)
	}
	t.Name = canonicalTarget(t.Name)
	flagArgs, extraArgs := splitOnDashDash(rest)

	m, err := loadMagus(ctx, rootOverride)
	if err != nil {
		return fallBack("the workspace did not load: %v", err)
	}
	var targets []types.Target
	if sub == "affected" {
		targets, err = affectedPipedTargets(ctx, m, t.Name, flagArgs)
	} else {
		targets, err = runPipedTargets(ctx, m, t, prescan, flagArgs)
	}
	if err != nil {
		return fallBack("%v", err)
	}
	charms := withDefaultCharms(t.Charms, globalCfg.DefaultCharms, hasModeFlag(flagArgs, "no-default-charms"))
	if t.Name == "ci" {
		charms = magus.CharmsForCI(charms)
	}
	return m.MachineClaims(ctx, targets, charms, extraArgs), ""
}

func runPipedTargets(ctx context.Context, m *magus.Magus, t types.Target, prescan func(*flag.FlagSet), flagArgs []string) ([]types.Target, error) {
	fs := flag.NewFlagSet("prescan", flag.ContinueOnError)
	scratch := globalCfg
	flaggen.BindFlags(fs, &scratch)
	bindDisplayFlags(fs)
	prescan(fs)
	_, projects := partitionFlags(fs, flagArgs)
	targets, _, err := resolveTargets(ctx, m, t, runSelection{projects: projects, cwd: clientCwd(ctx)})
	if err == nil && len(targets) > 0 {
		targets, err = filterServedTargets(ctx, m, targets, t.Name)
	}
	if err != nil {
		return nil, fmt.Errorf("the targets did not resolve: %w", err)
	}
	return targets, nil
}

// affectedPipedTargets expands the VCS diff the way `magus affected` does. A set streamed
// in on --stdin is known only to the stream, so it has no claim to read.
func affectedPipedTargets(ctx context.Context, m *magus.Magus, target string, flagArgs []string) ([]types.Target, error) {
	fs := flag.NewFlagSet("prescan", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	scratch := globalCfg
	flaggen.BindFlags(fs, &scratch)
	bindDisplayFlags(fs)
	af := flaggen.BindAffected(fs)
	if err := fs.Parse(flagArgs); err != nil {
		return nil, fmt.Errorf("the affected flags did not parse: %w", err)
	}
	if af.Stdin {
		return nil, errors.New("affected --stdin takes its projects from the stream")
	}
	targets, _, _, err := m.ExpandAffected(ctx, target, af.Base)
	if err != nil {
		return nil, fmt.Errorf("the affected set did not resolve: %w", err)
	}
	return targets, nil
}
