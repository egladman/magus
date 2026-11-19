package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/egladman/magus"
	flaggen "github.com/egladman/magus/cmd/magus/gen"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/interactive"
	"github.com/egladman/magus/internal/interp"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/log/attr"
	"github.com/egladman/magus/internal/service/console"
	"github.com/egladman/magus/internal/settle"
	"github.com/egladman/magus/libs/clikit/exit"
	jobtypes "github.com/egladman/magus/libs/safetyscissors/job"
	"github.com/egladman/magus/types"
	typesvcs "github.com/egladman/magus/types/vcs"
	"github.com/egladman/magus/vcs"
)

// vcsCommand implements `magus vcs <subcommand>`.
//
// Three of the verbs rest on one fact git does not have: which files are generated, and
// which target rebuilds them. `ls-files` classifies dirty paths for staging, `resolve` settles a
// conflicted merge, `merge-driver` is the per-file callback git invokes. `checkpoint` is
// the odd one out and rests on nothing: it reads the working state's identity back.
//
// `ls-files` lists and never stages: the person stages what it lists. Not a general git
// proxy.
func vcsCommand(ctx context.Context, root string, rc runConfig, args []string) error {
	if len(args) == 0 {
		vcsUsage(os.Stderr)
		return exit.Usagef("magus vcs: a subcommand is required (try: ls-files, resolve, checkpoint, checkout)")
	}
	verb, rest := splitVCSVerb(args)
	switch verb {
	case "ls-files":
		return vcsLsFilesCommand(ctx, root, rest)
	case "resolve":
		return vcsResolveCommand(ctx, root, rc, rest)
	case "checkpoint":
		return vcsCheckpointCommand(ctx, root, rest)
	case "checkout":
		return vcsCheckoutCommand(ctx, root, rest)
	case "merge-driver":
		return mergeDriverCommand(ctx, root, rest)
	case "-h", "--help", "help":
		vcsUsage(os.Stderr)
		return nil
	default:
		return exit.Usagef("magus vcs: unknown subcommand %q (want ls-files, resolve, checkpoint, checkout, or merge-driver)", verb)
	}
}

// splitVCSVerb returns the subcommand and the remaining args with it removed.
//
// The verb is the first non-flag token, not args[0]: global flags are allowed on either
// side of a subcommand elsewhere in this CLI, and a wrapper can prefix the merge driver's
// registration string. A help flag is reported as the verb.
func splitVCSVerb(args []string) (verb string, rest []string) {
	for i, a := range args {
		if a == "-h" || a == "--help" || !strings.HasPrefix(a, "-") {
			return a, slices.Concat(args[:i], args[i+1:])
		}
	}
	return "", args
}

func vcsUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage: magus vcs <subcommand> [args]")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Subcommands:")
	fmt.Fprintln(w, "  ls-files       list the dirty paths a commit of this change wants; stages nothing")
	fmt.Fprintln(w, "  resolve        settle an in-progress merge/rebase's conflicted generated files, then regenerate once")
	fmt.Fprintln(w, "  checkpoint     print the identity of the working state right now; writes nothing")
	fmt.Fprintln(w, "  checkout       check out one revision in an isolated checkout for review")
	fmt.Fprintln(w, "  merge-driver   the per-file merge driver git and hg invoke; you do not run this by hand")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Run `magus vcs <subcommand> -h` for its own flags.")
}

// ----------------------------------------------------------- vcs merge-driver

// mergeDriverCommand dispatches `magus vcs merge-driver %O %A %B %L %P`, which settles one
// conflicted file (settle.File). Args: ancestor result other markerSize path (git and
// hg), plus output when the VCS reads the result from a file of its own (jj). A non-zero
// exit leaves the file conflicted, with markers. Per-clone wiring is installed by `magus
// init`, not here.
func mergeDriverCommand(ctx context.Context, root string, args []string) error {
	if len(args) == 0 {
		_ = mergeDriverUsage()
		// git only ever calls this with all five placeholders, so a bare invocation is
		// a human typing it. Exiting 0 said "merge resolved" for a run that did nothing,
		// and disagreed with the 1-argument case, which already errored.
		return exit.Usagef("magus vcs merge-driver: expected 5 arguments (ancestor result other markerSize path), got 0")
	}
	if args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		return mergeDriverUsage()
	}
	if len(args) < 5 {
		return exit.Usagef("magus vcs merge-driver: expected 5 arguments (ancestor result other markerSize path), got %d", len(args))
	}
	if len(args) > 6 {
		return exit.Usagef("magus vcs merge-driver: expected at most 6 arguments (ancestor result other markerSize path output), got %d", len(args))
	}
	f := settle.Files{Base: args[0], Ours: args[1], Theirs: args[2], Output: args[1], MarkerSize: args[3]}
	if len(args) == 6 {
		f.Output = args[5]
	}

	relPath, err := settle.DriverRelPath(root, args[4])
	if err != nil {
		return err
	}
	m, err := loadMagus(ctx, root)
	if err != nil {
		return fmt.Errorf("merge-driver: load workspace: %w", err)
	}
	return settle.File(ctx, m, f, relPath)
}

// mergeDriverUsage prints usage for the merge-driver subcommand.
func mergeDriverUsage() error {
	fmt.Fprintln(os.Stderr, "Usage: magus vcs merge-driver %O %A %B %L %P")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "The VCS merge driver for declared output files and the files magus.yaml's")
	fmt.Fprintln(os.Stderr, "vcs.auto_resolve opts in. git, hg and Sapling invoke it during a merge, and")
	fmt.Fprintf(os.Stderr, "`%s` runs it through `jj resolve` on jj. An opted-in file\n", hint.VCSResolve.String())
	fmt.Fprintln(os.Stderr, "is merged when every region both sides changed is low risk, and otherwise")
	fmt.Fprintln(os.Stderr, "left with conflict markers. On git a declared output keeps the current version,")
	fmt.Fprintln(os.Stderr, "warns that the other side's change is gone, and records the regeneration it owes;")
	fmt.Fprintf(os.Stderr, "`%s` runs it once the merge, rebase, cherry-pick or revert\n", hint.VCSResolve.String())
	fmt.Fprintln(os.Stderr, "has the whole tree. With no record, a declared output is left conflicted too.")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintf(os.Stderr, "You do not run this by hand. Wire it once per clone with `%s`.\n", hint.Init.String())
	fmt.Fprintln(os.Stderr, "git calls it as:  magus vcs merge-driver %O %A %B %L %P")
	fmt.Fprintln(os.Stderr, "hg calls it as:   magus vcs merge-driver $base $local $other 0 $local")
	fmt.Fprintln(os.Stderr, "jj calls it as:   magus vcs merge-driver $base $left $right $marker_length $path $output")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintf(os.Stderr, "To settle a conflicted merge yourself, run `%s`: it decides\n", hint.VCSResolve.String())
	fmt.Fprintln(os.Stderr, "every conflicted path at once, regenerates once, and stages the result -")
	fmt.Fprintln(os.Stderr, "including the files one side deleted, which no VCS calls a driver for.")
	return nil
}

// ---------------------------------------------------------------- vcs resolve

func vcsResolveUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage: magus vcs resolve [flags]")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Settle the conflicted GENERATED files of an in-progress merge, rebase, or")
	fmt.Fprintln(w, "cherry-pick, then regenerate them once and stage the result. Conflicts in")
	fmt.Fprintln(w, "files magus does not generate are reported and left for you.")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "This is the bulk counterpart to the merge driver. git invokes a driver once")
	fmt.Fprintln(w, "per conflicted path, inside its own index manipulation, so the cost scales")
	fmt.Fprintln(w, "with the conflict count and no regeneration can run there at all. Deciding")
	fmt.Fprintln(w, "every path first and regenerating once is both faster and the only way to")
	fmt.Fprintln(w, "settle a file one side deleted, which no VCS calls a merge driver for.")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "With --against, a source file two jobs both wrote also settles when each side")
	fmt.Fprintln(w, "carries one job's work, every such job passed `magus job wait`, their claims on")
	fmt.Fprintln(w, "it (<file>#<declaration>) are disjoint, and a project's merge_low_risk covers it.")
	fmt.Fprintln(w, "Each edit must sit in its own job's claims; import lines merge as a set. A file")
	fmt.Fprintln(w, "a job claims gets a line saying it settled or which condition failed. Needs a")
	fmt.Fprintln(w, "VCS that reports a merge base: git does; Mercurial, Sapling and jj do not.")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "With nothing conflicted, it runs the regenerations a merge left owed. Git")
	fmt.Fprintln(w, "hooks only submit jobs and never regenerate, so run it after a merge,")
	fmt.Fprintln(w, "cherry-pick or rebase that touched generator inputs.")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "A settle another magus's project lock keeps from running waits up to 2m, then")
	fmt.Fprintln(w, "stages nothing, leaves the regeneration owed and prints this command.")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Flags:")
	fmt.Fprintln(w, "  --against <ref>  merge <ref> first, then settle what it conflicts with.")
	fmt.Fprintln(w, "                   Needs a clean tree. Leaves the merge in progress to")
	fmt.Fprintln(w, "                   commit; with --dry-run it is backed out again.")
	fmt.Fprintln(w, "  --dry-run        classify and report; touch nothing (global flag)")
}

// vcsResolveCommand classifies every conflicted path, settles the generated ones in bulk,
// regenerates once, and records the result.
func vcsResolveCommand(ctx context.Context, root string, rc runConfig, args []string) error {
	var rf *flaggen.VCSResolveFlags
	pos, err := commandParse("vcs resolve", args, func(fs *flag.FlagSet) {
		rf = flaggen.BindVCSResolve(fs)
		fs.Usage = func() { vcsResolveUsage(os.Stderr) }
	})
	if err != nil {
		return err
	}
	// resolve mutates every conflicted path in the workspace. Widening
	// `magus vcs resolve one/file.go` from one path to all of them would cost a tree.
	if len(pos) > 0 {
		return exit.Usagef("vcs resolve: takes no paths; it settles every conflicted path in the workspace (got %q)", pos[0])
	}

	m, err := loadMagus(ctx, root)
	// A magusfile carrying this merge's conflict markers is not a magusfile magus can parse,
	// and the declarations it holds are the whole basis for settling anything. The committed
	// side is a complete copy of those declarations, so retry against it rather than
	// stopping: the generated conflicts get settled now and the hand-written one waits for a
	// human, instead of both waiting.
	stale := false
	if err != nil {
		overlay := settle.CommittedMagusfiles(ctx, root)
		if len(overlay) == 0 {
			return err
		}
		m, err = loadMagus(interp.WithOverlay(ctx, overlay), root)
		if err != nil {
			return err
		}
		stale = true
		fmt.Printf("vcs resolve: %s still carries this merge's conflict markers, so the committed "+
			"declarations are being used to settle the rest.\n", strings.Join(slices.Sorted(maps.Keys(overlay)), ", "))
	}
	res, err := resolveVCS(ctx, root, m)
	if err != nil {
		return fmt.Errorf("vcs resolve: no VCS resolved for this workspace: %w", err)
	}
	if res.VCS == nil {
		return errors.New("vcs resolve: version control is disabled for this workspace, resolve this merge by hand")
	}

	// The revision the merge starts from, which settling by job claims reads as ours.
	var ours string
	if rf.Against != "" {
		meta, err := res.VCS.Metadata(ctx, m.Root())
		if err != nil {
			return fmt.Errorf("vcs resolve: %w", err)
		}
		ours = meta.ID
		undo, err := settle.StartMergeAgainst(ctx, m.Root(), res, rf.Against, globalCfg.DryRun)
		if err != nil {
			return err
		}
		defer undo()
	}

	conflicts, err := res.VCS.Conflicts(ctx, m.Root())
	if err != nil {
		return fmt.Errorf("vcs resolve: %w", err)
	}
	if len(conflicts) == 0 {
		if rf.Against != "" {
			fmt.Printf("vcs resolve: %s merged with no conflicts; conclude it with `git commit`\n", rf.Against)
			return nil
		}
		if globalCfg.DryRun {
			fmt.Println("vcs resolve: nothing to resolve; no conflicted paths")
			return nil
		}
		return settle.OwedByHand(ctx, os.Stdout, m, res.VCS, interactive.DefinesTarget(ctx, m), settleRun(root, rc))
	}

	if !globalCfg.DryRun {
		if conflicts, err = settle.AutoResolve(ctx, os.Stdout, m, res.VCS, conflicts); err != nil {
			return settle.StageFailed(err, "settling by job claims", "planning the rest")
		}
		if len(conflicts) > 0 && rf.Against != "" {
			if conflicts, err = settle.ByClaims(ctx, os.Stdout, m, res.VCS, jobRows(ctx, m.Root()), ours, rf.Against, conflicts); err != nil {
				return settle.StageFailed(err, "planning the rest")
			}
		}
		if len(conflicts) == 0 {
			return nil
		}
	}

	plan, err := settle.PlanConflicts(ctx, m, res.VCS, conflicts)
	if err != nil {
		return err
	}
	settle.Report(os.Stdout, plan, globalCfg.DryRun)
	if globalCfg.DryRun {
		return settle.UnresolvedError(plan)
	}
	return settle.Apply(ctx, os.Stdout, m, res.VCS, plan, func(args []string) error { return runTarget(ctx, root, rc, args) }, stale)
}

// settleLockWait bounds how long a settle waits for another magus to release a
// project lock, polling every settleLockPoll.
const (
	settleLockWait = 2 * time.Minute
	settleLockPoll = 2 * time.Second
)

// settleRun is the regeneration a settle runs: quiet, and waiting on a busy lock.
func settleRun(root string, rc runConfig) settle.RunFunc {
	run := settle.Quietly(func(ctx context.Context, inv []string) error { return runTarget(ctx, root, rc, inv) }, console.WithRunSink)
	return settle.Waiting(run, settleLockWait, settleLockPoll, func(ctx context.Context, line string) {
		slog.WarnContext(ctx, line, attr.Notice(""), attr.Component("magus"))
	})
}

// resolveVCS returns the active VCS resolution for the workspace.
func resolveVCS(ctx context.Context, root string, m *magus.Magus) (types.VCSResolution, error) {
	wsRoot := m.Root()
	if wsRoot == "" {
		wsRoot = root
	}
	return vcs.Resolve(ctx, wsRoot, "", m.VCSOptions())
}

// jobRows reads the job store of the workspace at root when asked, for settling by claims.
func jobRows(ctx context.Context, root string) func() ([]jobtypes.Job, error) {
	return func() ([]jobtypes.Job, error) {
		store, err := openJobs(ctx, root, jobOpen{})
		if err != nil {
			return nil, err
		}
		return store.List(ctx)
	}
}

// ------------------------------------------------------------- vcs checkpoint

func vcsCheckpointUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage: magus vcs checkpoint [flags]")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Print the identity of the working state right now: the head revision, the")
	fmt.Fprintln(w, "branch carrying it, whether the tree is dirty, and a digest of the")
	fmt.Fprintln(w, "uncommitted patch.")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "It RESOLVES AND RECORDS; it never MINTS. No tag, no stash, no ref, no file,")
	fmt.Fprintln(w, "nothing changed anywhere - so taking one costs the tree nothing and one you")
	fmt.Fprintln(w, "do not keep costs nothing either. Record it when you hand work out, so a")
	fmt.Fprintln(w, "later reader knows what that work was looking at.")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "--preserve is the exception, and the reason it is a flag. An identity tells")
	fmt.Fprintln(w, "you whether two trees match; it cannot rebuild either one. --preserve also")
	fmt.Fprintln(w, "captures the uncommitted work, tracked edits and untracked files alike, and")
	fmt.Fprintln(w, "prints a handle that restores it. The working copy is untouched either way.")
	fmt.Fprintln(w, "On git a capture is kept until every change in it reads the same in the base,")
	fmt.Fprintf(w, "however old; the server's `%s` drops it then. Without a server\n", hint.JobRun.With("prune-preserved"))
	fmt.Fprintln(w, "that job is a no-op; run `magus server prune-preserved` instead. Mercurial")
	fmt.Fprintln(w, "captures are never pruned, since Mercurial cannot prove one landed. On Sapling a")
	fmt.Fprintln(w, "capture is a hidden commit no Sapling command can drop, so it stays until you")
	fmt.Fprintln(w, "remove it. Jujutsu mints nothing, so nothing accumulates.")
	fmt.Fprintln(w, "")
	fmt.Fprintf(w, "Feed the revision to anything that takes one (%s).\n", hint.GraphDiff.With("--rev", "<rev>"))
	fmt.Fprintln(w, "Compare two digests to learn whether two workers saw the same uncommitted")
	fmt.Fprintln(w, "tree, which the revision alone cannot tell you: a dirty tree's revision is")
	fmt.Fprintln(w, "the same one everybody else has.")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Flags:")
	fmt.Fprintln(w, "  --preserve capture the uncommitted work too, and print a handle for it")
	fmt.Fprintln(w, "  -o name    the citable token: the revision, or <revision>+<digest> when dirty")
	fmt.Fprintln(w, "  -o json    the whole record (global flag; yaml, jsonl and template too)")
}

// vcsCheckpointCommand reads the working state's identity and prints it.
func vcsCheckpointCommand(ctx context.Context, root string, args []string) error {
	var flags *flaggen.VCSCheckpointFlags
	pos, err := commandParse("vcs checkpoint", args, func(fs *flag.FlagSet) {
		fs.Usage = func() { vcsCheckpointUsage(os.Stderr) }
		flags = flaggen.BindVCSCheckpoint(fs)
	})
	if err != nil {
		return err
	}
	if len(pos) > 0 {
		return exit.Usagef("vcs checkpoint: takes no arguments; it reports the whole workspace's working state (got %q)", pos[0])
	}

	ws, err := inspectWorkspace(ctx, root)
	if err != nil {
		return err
	}
	// The RESOLVED workspace root, not the --root override, for the reason vcsLsFilesCommand
	// spells out: the override is empty unless you passed --root, and an empty dir sends
	// every VCS call to the process cwd, which is a different repository the moment you
	// run this from anywhere but the root.
	wsRoot := ws.Root()
	res, err := vcs.Resolve(ctx, wsRoot, "", ws.VCSOptions())
	if err != nil {
		return fmt.Errorf("vcs checkpoint: %w", err)
	}
	cp, err := vcs.Checkpoint(ctx, wsRoot, res, flags.Preserve)
	if err != nil {
		// A failed --preserve can still have MINTED. Mercurial's shelf and Sapling's
		// snapshot commit both exist before the step that reports the failure, and the
		// handle in cp is the only thing that reaches the object, so it is rendered before
		// the failure is returned: the record on stdout, the reason on stderr, and an exit
		// status that still says the command failed. git returns no handle on any of its
		// failure paths, so it never arrives here.
		if cp.Preserved == "" {
			return err
		}
		if emitErr := emitCheckpoint(cp); emitErr != nil {
			return errors.Join(err, emitErr)
		}
		return err
	}
	return emitCheckpoint(cp)
}

// emitCheckpoint renders the checkpoint: the structured formats get the record, -o name
// the one citable token, and the terminal the one-line reading of the same value.
func emitCheckpoint(cp typesvcs.Checkpoint) error {
	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}
	switch opts.Format {
	case outputJSON, outputYAML, outputJSONL, outputTemplate:
		return emitFormatted(opts, cp)
	case outputName:
		return emitNames(os.Stdout, []string{cp.Token()})
	case outputText:
	}
	fmt.Println(settle.CheckpointLine(cp))
	return nil
}

// --------------------------------------------------------------- vcs ls-files

func vcsLsFilesUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage: magus vcs ls-files [<pattern>...] [flags]")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "List the dirty paths a commit of this change wants, and stage nothing.")
	fmt.Fprintln(w, "By default: tracked, modified or deleted declared sources that no other")
	fmt.Fprintln(w, "live job holds and that are not already staged. stderr names each group")
	fmt.Fprintln(w, "left out and the flag that lists it.")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Patterns are write-path patterns read from the current directory; they only narrow.")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Flags:")
	fmt.Fprintln(w, "  --include-untracked    untracked files (refused on jj)")
	fmt.Fprintln(w, "  --include-generated    declared outputs a dirty source explains")
	fmt.Fprintln(w, "  --include-unexplained  declared outputs nothing here explains")
	fmt.Fprintln(w, "  --include-unclaimed    paths no target declares, and the ones magus maintains")
	fmt.Fprintln(w, "  --include-held         paths another live job in this checkout writes")
	fmt.Fprintln(w, "  --include-staged       paths whose whole change is staged (git only)")
	fmt.Fprintln(w, "  --exclude <pattern>    leave out what the pattern covers; repeatable")
	fmt.Fprintln(w, "  --project <project>    only the files the project owns; repeatable")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Stage the listing: magus vcs ls-files -o name | git add --pathspec-from-file=-")
}

// vcsLsFilesCommand lists the dirty paths a commit of this change wants.
func vcsLsFilesCommand(ctx context.Context, root string, args []string) error {
	var lf *flaggen.VCSLsFilesFlags
	var exclude, projects job.ListFlag
	pos, err := commandParse("vcs ls-files", args, func(fs *flag.FlagSet) {
		lf = flaggen.BindVCSLsFiles(fs)
		fs.Var(&exclude, flaggen.FlagVCSLsFilesExclude, "Leave out paths this write-path `pattern` covers; repeatable or comma-separated")
		fs.Var(&projects, flaggen.FlagVCSLsFilesProject, "List only the files this `project` owns; repeatable or comma-separated")
		fs.Usage = func() { vcsLsFilesUsage(os.Stderr) }
	})
	if err != nil {
		return err
	}
	include := map[string]bool{
		settle.LsGroupUntracked:   lf.IncludeUntracked,
		settle.LsGroupGenerated:   lf.IncludeGenerated,
		settle.LsGroupUnexplained: lf.IncludeUnexplained,
		settle.LsGroupUnclaimed:   lf.IncludeUnclaimed,
		settle.LsGroupHeld:        lf.IncludeHeld,
		settle.LsGroupStaged:      lf.IncludeStaged,
	}

	ws, err := inspectWorkspace(ctx, root)
	if err != nil {
		return err
	}
	root = ws.Root()
	res, err := vcs.Resolve(ctx, root, "", ws.VCSOptions())
	if err != nil || res.VCS == nil {
		return fmt.Errorf("vcs ls-files: no VCS resolved for this workspace")
	}
	if err := settle.LsFilesRefusal(res.VCS, include); err != nil {
		return exit.Usagef("%s", err)
	}
	patterns, err := settle.WorkspaceRelPatterns(root, pos)
	if err != nil {
		return err
	}
	excluded, err := settle.WorkspaceRelPatterns(root, exclude)
	if err != nil {
		return err
	}

	list, err := settle.ListFiles(ctx, ws, res, root, settle.LsFilesFilter{Patterns: patterns, Exclude: excluded, Projects: projects, Include: include}, jobPlace, version)
	if err != nil {
		return err
	}

	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}
	if opts.Format == outputText {
		return emitNames(os.Stdout, list.Files)
	}
	return emitRecord(opts, list, list.Files)
}

// ------------------------------------------------------------- vcs checkout

// vcsCheckoutCommand implements `magus vcs checkout <rev> [--fetch <ref>]
// [--no-bootstrap]` and `magus vcs checkout --remove <dir>`.
func vcsCheckoutCommand(ctx context.Context, root string, args []string) error {
	var flags *flaggen.VCSCheckoutFlags
	pos, err := commandParse("vcs checkout", args, func(fs *flag.FlagSet) {
		flags = flaggen.BindVCSCheckout(fs)
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus vcs checkout <rev> [--fetch <ref>] [--no-bootstrap] | --remove <dir>")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Check out one revision under $XDG_CACHE_HOME/magus/tmp for review, then run")
			fmt.Fprintln(os.Stderr, "the workspace's declared install, harness install and checkout.bootstrap hook.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Flags:")
			printOwnDefaults(fs)
		}
	})
	if err != nil {
		return err
	}
	switch {
	case flags.Remove != "" && len(pos) > 0:
		return exit.Usagef("vcs checkout: --remove takes no revision (got %q)", pos[0])
	case flags.Remove == "" && len(pos) != 1:
		return exit.Usagef("vcs checkout: takes exactly one revision, got %d", len(pos))
	}
	ws, err := inspectWorkspace(ctx, root)
	if err != nil {
		return err
	}
	res, err := vcs.Resolve(ctx, ws.Root(), "", ws.VCSOptions())
	if err != nil {
		return fmt.Errorf("vcs checkout: %w", err)
	}
	if res.VCS == nil {
		return fmt.Errorf("vcs checkout: no version control answers in %s", ws.Root())
	}
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("vcs checkout: locate the running magus: %w", err)
	}
	review, err := settle.NewReviewCheckout(ws.Root(), exe, version, res.VCS)
	if err != nil {
		return err
	}
	if flags.Remove != "" {
		if err := settle.PreserveBeforeRemoval(ctx, flags.Remove, res); err != nil {
			return err
		}
		if err := review.Remove(ctx, flags.Remove); err != nil {
			return err
		}
		fmt.Printf("removed %s\n", flags.Remove)
		return nil
	}
	rec, err := review.Checkout(ctx, pos[0], flags.Fetch, !flags.Bootstrap)
	if rec.Path != "" {
		if emitErr := emitCheckout(rec); emitErr != nil {
			return errors.Join(err, emitErr)
		}
	}
	return err
}

// emitCheckout renders rec: the structured formats get the record, the
// terminal its text reading.
func emitCheckout(rec job.ReviewCheckoutRecord) error {
	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}
	switch opts.Format {
	case outputJSON, outputYAML, outputJSONL, outputTemplate:
		return emitFormatted(opts, rec)
	case outputName:
		return emitNames(os.Stdout, []string{rec.Path})
	case outputText:
	}
	settle.PrintCheckout(os.Stdout, rec)
	return nil
}
