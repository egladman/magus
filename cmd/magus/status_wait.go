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
	err := awaitSeat(ctx, reader, argv, claims, waiting)
	if err == nil && waited {
		slog.InfoContext(ctx, fmt.Sprintf("the build budget can seat it now, after %s", time.Since(start).Round(time.Second)), attr.Notice(""), attr.Component("magus"))
	}
	return err
}

// awaitSeat is one request the broker holds until the budget can seat claims: for a magus
// reader, whose command argv is, a seat held for it; for any other reader (0), only the
// word that the budget has room. The deadline is ctx's. With no broker, or one shutting
// down, nothing is held and the run admits itself; that is said rather than assumed.
func awaitSeat(ctx context.Context, reader int, argv []string, claims []types.MachineClaim, waiting func(string)) error {
	client := broker.NewClient(broker.DefaultAddr())
	defer func() { _ = client.Close() }()
	wait := func(ctx context.Context, parked func(types.MachineClaim, types.MachineVerdict)) (types.MachineClaim, types.MachineVerdict, error) {
		return client.Await(ctx, claims, parked)
	}
	if reader != 0 {
		seat := cache.WidestSeat(claims)
		seat.Command = strings.Join(argv, " ")
		wait = func(ctx context.Context, parked func(types.MachineClaim, types.MachineVerdict)) (types.MachineClaim, types.MachineVerdict, error) {
			v, err := client.Reserve(ctx, seat, reader, func(v types.MachineVerdict) { parked(seat, v) })
			return seat, v, err
		}
	}
	err := cache.AwaitMachine(ctx, wait, waiting)
	var refused *broker.Error
	if errors.Is(err, broker.ErrUnavailable) || errors.As(err, &refused) && refused.Code == broker.CodeDraining {
		held := "nothing holds a seat for this run"
		if reader != 0 {
			held = fmt.Sprintf("no seat is reserved for pid %d", reader)
		}
		slog.InfoContext(ctx, fmt.Sprintf("%s; it asks the budget itself when it starts", held),
			attr.Error(err), attr.Notice(""), attr.Component("magus"))
		return nil
	}
	return err
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
