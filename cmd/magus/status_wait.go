package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
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

// pipedRunClaims is what the magus run whose argv is argv will claim, read the way run
// reads its own. Anything else, or nothing, reading stdout waits for one free slot, as
// does a run this cannot follow: that run reports its own error once it starts.
func pipedRunClaims(ctx context.Context, rootOverride string, argv []string) []types.MachineClaim {
	slot := []types.MachineClaim{{Slots: 1}}
	if len(argv) == 0 {
		return slot
	}
	sub, subArgs := peekSub(argv[1:])
	if sub != "run" || runReadsPlan(subArgs) {
		return slot
	}
	prescan := func(fs *flag.FlagSet) { bindRunFlags(fs, nil) }
	raw, rest, ok := splitTargetFromArgs(subArgs, prescan)
	if !ok {
		return slot
	}
	_, name := parseTarget(raw)
	t, err := types.ParseTarget(name)
	if err != nil {
		return slot
	}
	t.Name = canonicalTarget(t.Name)
	flagArgs, extraArgs := splitOnDashDash(rest)
	fs := flag.NewFlagSet("prescan", flag.ContinueOnError)
	flaggen.BindFlags(fs, &globalCfg)
	bindDisplayFlags(fs)
	prescan(fs)
	_, projects := partitionFlags(fs, flagArgs)

	m, err := loadMagus(ctx, rootOverride)
	if err != nil {
		return slot
	}
	targets, _, err := resolveTargets(ctx, m, t, runSelection{projects: projects, cwd: clientCwd(ctx)})
	if err == nil && len(targets) > 0 {
		targets, err = filterServedTargets(ctx, m, targets, t.Name)
	}
	if err != nil {
		return slot
	}
	charms := withDefaultCharms(t.Charms, globalCfg.DefaultCharms, hasModeFlag(flagArgs, "no-default-charms"))
	if t.Name == "ci" {
		charms = magus.CharmsForCI(charms)
	}
	return m.MachineClaims(ctx, targets, charms, extraArgs)
}
