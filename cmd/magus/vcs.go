package main

import (
	"cmp"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/egladman/magus"
	flaggen "github.com/egladman/magus/cmd/magus/gen"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/interp"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/log/attr"
	"github.com/egladman/magus/internal/risk"
	"github.com/egladman/magus/internal/service/console"
	"github.com/egladman/magus/internal/settle"
	"github.com/egladman/magus/internal/workspace"
	"github.com/egladman/magus/libs/cmdline/exit"
	"github.com/egladman/magus/types"
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
		return exit.Usagef("magus vcs: a subcommand is required (try: ls-files, resolve, checkpoint)")
	}
	verb, rest := splitVCSVerb(args)
	switch verb {
	case "ls-files":
		return vcsLsFilesCommand(ctx, root, rest)
	case "resolve":
		return vcsResolveCommand(ctx, root, rc, rest)
	case "checkpoint":
		return vcsCheckpointCommand(ctx, root, rest)
	case "merge-driver":
		return mergeDriverCommand(ctx, root, rest)
	case "-h", "--help", "help":
		vcsUsage(os.Stderr)
		return nil
	default:
		return exit.Usagef("magus vcs: unknown subcommand %q (want ls-files, resolve, checkpoint, or merge-driver)", verb)
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

	relPath, err := mergeDriverRelPath(root, args[4])
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
	fmt.Fprintln(os.Stderr, "left with conflict markers. A declared output keeps the current version")
	fmt.Fprintln(os.Stderr, "instead of writing conflict markers. On git it records the regeneration it owes,")
	fmt.Fprintf(os.Stderr, "and `%s` runs it once the merge, rebase, cherry-pick or\n", hint.VCSResolve.String())
	fmt.Fprintln(os.Stderr, "revert has the whole tree.")
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

// mergeDriverRelPath normalizes the driver's path argument to a workspace-relative slash
// path. git passes it repo-relative; hg passes an absolute workspace path.
func mergeDriverRelPath(root, pathArg string) (string, error) {
	if !filepath.IsAbs(pathArg) {
		return filepath.ToSlash(pathArg), nil
	}
	wsRoot, err := workspace.FindRoot(root)
	if err != nil {
		return "", fmt.Errorf("merge-driver: find workspace root: %w", err)
	}
	rel, err := filepath.Rel(wsRoot, pathArg)
	if err != nil {
		return "", fmt.Errorf("merge-driver: resolve path %q: %w", pathArg, err)
	}
	return filepath.ToSlash(rel), nil
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
		overlay := committedMagusfiles(ctx, root)
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
		undo, err := startMergeAgainst(ctx, m.Root(), res, rf.Against)
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
		return settleOwedByHand(ctx, root, rc, m, res.VCS)
	}

	if !globalCfg.DryRun {
		if conflicts, err = autoResolveConflicts(ctx, m, res.VCS, conflicts); err != nil {
			return err
		}
		if len(conflicts) > 0 && rf.Against != "" {
			if conflicts, err = settleByClaims(ctx, m, res.VCS, ours, rf.Against, conflicts); err != nil {
				return err
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
	reportResolution(plan, globalCfg.DryRun)
	if globalCfg.DryRun {
		return unresolvedError(plan)
	}
	return applyResolution(ctx, root, rc, m, res.VCS, plan, stale)
}

// autoResolveConflicts runs the merge driver over the content conflicts in source files,
// where the VCS did not already run it during the operation (jj), and returns the
// conflicts still standing. The driver decides each file (Magus.AutoResolve), so a
// comment-only edit settles here even though no glob routes it.
func autoResolveConflicts(ctx context.Context, m *magus.Magus, drv types.VCSDriver, conflicts []types.Conflict) ([]types.Conflict, error) {
	var eligible []string
	for _, c := range conflicts {
		if c.Kind == types.ConflictKindContent && m.FindOutputProducer(filepath.Join(m.Root(), filepath.FromSlash(c.Path))) == nil {
			eligible = append(eligible, c.Path)
		}
	}
	if len(eligible) == 0 {
		return conflicts, nil
	}
	if err := drv.RunMergeDriver(ctx, m.Root(), eligible); err != nil {
		return nil, fmt.Errorf("vcs resolve: %w", err)
	}
	left, err := drv.Conflicts(ctx, m.Root())
	if err != nil {
		return nil, fmt.Errorf("vcs resolve: %w", err)
	}
	for _, p := range eligible {
		if !slices.ContainsFunc(left, func(c types.Conflict) bool { return c.Path == p }) {
			fmt.Printf("vcs resolve: auto-resolved %s\n", p)
		}
	}
	if len(left) == 0 {
		fmt.Println("vcs resolve: every conflict is resolved")
	}
	return left, nil
}

// settleByClaims settles the content conflicts that jobs on the two sides of a merge of
// against into ours reached through disjoint declaration claims, and returns the conflicts
// still standing. A path settles only when every condition holds:
//   - a project's merge_low_risk covers it, and a diff driver places its declarations;
//   - the VCS reports one merge base for the two sides;
//   - each side carries the work of a job that writes the path, and every such job is
//     recorded pass by `magus job wait`;
//   - each of those jobs writes it by declaration claims alone, and the sides' claims are
//     disjoint;
//   - risk.MergeByClaims settles every region the two sides' edits meet in.
//
// A path some job claims by declaration gets one line saying it settled or which condition
// failed. A path no job claims is passed over in silence, since claims say nothing about it.
func settleByClaims(ctx context.Context, m *magus.Magus, drv types.VCSDriver, ours, against string, conflicts []types.Conflict) ([]types.Conflict, error) {
	root := m.Root()
	store, err := openJobs(ctx, root, jobOpen{})
	var rows []types.Job
	if err == nil {
		rows, err = store.List()
	}
	if err != nil {
		fmt.Printf("vcs resolve: no conflict settles by job claims: the job store: %v\n", err)
		return conflicts, nil
	}
	s := claimSettle{m: m, drv: drv, ours: ours, theirs: against}
	var left []types.Conflict
	for _, c := range conflicts {
		writers, claimed := jobsWriting(rows, c.Path)
		if c.Kind != types.ConflictKindContent || !claimed || m.FindOutputProducer(filepath.Join(root, filepath.FromSlash(c.Path))) != nil {
			left = append(left, c)
			continue
		}
		merged, how, err := s.settle(ctx, c.Path, writers)
		var why unsettledError
		if errors.As(err, &why) {
			fmt.Printf("vcs resolve: %s not settled by job claims: %s\n", c.Path, why)
			left = append(left, c)
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("vcs resolve: %s: %w", c.Path, err)
		}
		if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(c.Path)), merged, 0o644); err != nil {
			return nil, fmt.Errorf("vcs resolve: %w", err)
		}
		if err := drv.MarkResolved(ctx, root, []string{c.Path}); err != nil {
			return nil, fmt.Errorf("vcs resolve: %w", err)
		}
		fmt.Printf("vcs resolve: settled %s by job claims: %s\n", c.Path, how)
	}
	if len(left) == 0 {
		fmt.Println("vcs resolve: every conflict is resolved")
	}
	return left, nil
}

// jobsWriting returns the rows with a write path on p, and whether any of them claims p by
// declaration.
func jobsWriting(rows []types.Job, p string) (writers []types.Job, claimed bool) {
	for _, row := range rows {
		writes := false
		for _, e := range row.WritePaths {
			if types.PathsIntersect(e, p) {
				writes = true
				claimed = claimed || types.HasClaim(e)
			}
		}
		if writes {
			writers = append(writers, row)
		}
	}
	return writers, claimed
}

// claimSettle decides paths for settleByClaims. ours and theirs are the two revisions the
// merge joins; the merge base between them is looked up once.
type claimSettle struct {
	m            *magus.Magus
	drv          types.VCSDriver
	ours, theirs string

	looked  bool
	base    string
	baseErr error
}

// unsettledError is the first condition that kept a path from settling by job claims,
// as opposed to a failure to evaluate them.
type unsettledError string

func (e unsettledError) Error() string { return string(e) }

func unsettledf(format string, args ...any) error {
	return unsettledError(fmt.Sprintf(format, args...))
}

// mergeSide is one of the two revisions a merge joins; it indexes the per-side arrays of
// settle.
type mergeSide uint8

const (
	oursSide mergeSide = iota
	theirsSide
)

// settle is the merge of p, or an unsettledError naming the first condition that failed.
// how names the claims that settled it.
func (s *claimSettle) settle(ctx context.Context, p string, writers []types.Job) (merged []byte, how string, err error) {
	if _, _, ok := risk.MatchScopes(risk.MergeScopes(s.m.All()), p); !ok {
		return nil, "", unsettledf("no project's merge_low_risk covers it")
	}
	if _, ok := types.MatchDiffDriver(p); !ok {
		return nil, "", unsettledf("no diff driver places its declarations")
	}
	base, err := s.mergeBase(ctx)
	if err != nil {
		return nil, "", err
	}
	var jobs [2][]types.Job
	for _, row := range writers {
		if side, ok := s.resolveSide(ctx, row, base); ok {
			jobs[side] = append(jobs[side], row)
		}
	}
	names := [2]string{oursSide: "ours", theirsSide: s.theirs}
	var claims, cited [2][]string
	for side, rows := range jobs {
		if len(rows) == 0 {
			return nil, "", unsettledf("no job whose work %s carries writes it", names[side])
		}
		for _, row := range rows {
			if row.State != types.StatePass {
				return nil, "", unsettledf("job %s is %s, not recorded pass by `magus job wait`", row.ID, cmp.Or(row.State, types.StateDeclared))
			}
			for _, e := range row.WritePaths {
				if !types.PathsIntersect(e, p) {
					continue
				}
				_, decl := types.SplitClaim(e)
				if decl == "" {
					return nil, "", unsettledf("job %s writes it whole (%s), not by declaration", row.ID, e)
				}
				claims[side] = append(claims[side], decl)
				cited[side] = append(cited[side], fmt.Sprintf("%s (job %s)", e, row.ID))
			}
		}
	}
	if a, b, ok := risk.OverlappingClaims(claims[0], claims[1]); ok {
		return nil, "", unsettledf("the claims overlap: %s#%s on %s, %s#%s on %s", p, a, names[oursSide], p, b, names[theirsSide])
	}
	var content [3][]byte
	for i, rev := range []string{base, s.ours, s.theirs} {
		text, err := s.drv.ReadFileAt(ctx, s.m.Root(), rev, p)
		if err != nil {
			return nil, "", unsettledf("it cannot be read at %s: %v", rev, err)
		}
		content[i] = []byte(text)
	}
	res, err := risk.MergeByClaims(p, content[0],
		risk.ClaimSide{Content: content[1], Claims: claims[oursSide]},
		risk.ClaimSide{Content: content[2], Claims: claims[theirsSide]})
	if err != nil {
		return nil, "", unsettledError(err.Error())
	}
	return res.Content, fmt.Sprintf("%s against %s; every job passed", strings.Join(cited[0], ", "), strings.Join(cited[1], ", ")), nil
}

// mergeBase is the one merge base of the two sides, an unsettledError when there is not one.
func (s *claimSettle) mergeBase(ctx context.Context) (string, error) {
	if !s.looked {
		s.looked = true
		var ok bool
		s.base, ok, s.baseErr = s.drv.MergeBase(ctx, s.m.Root(), s.ours, s.theirs)
		switch {
		case errors.Is(s.baseErr, types.ErrVCSUnsupported):
			s.baseErr = unsettledf("%s reports no merge base, and each side's edits are measured from one", s.drv.Name())
		case s.baseErr == nil && !ok:
			s.baseErr = unsettledf("the sides share no one merge base")
		}
	}
	return s.base, s.baseErr
}

// resolveSide is the side whose history carries row's work, or false for neither: the work is
// already in the merge base, or no revision here names it. The work is the revision row's
// checkout has checked out, else a revision named row.ID: the branch-per-job convention
// hack/dev/merge-job-branches.buzz also reads.
func (s *claimSettle) resolveSide(ctx context.Context, row types.Job, base string) (mergeSide, bool) {
	tip := row.ID
	if info, err := os.Stat(row.CheckoutRoot); err == nil && info.IsDir() {
		if ref, err := s.drv.Ref(ctx, row.CheckoutRoot); err == nil && ref != "" {
			tip = ref
		}
	}
	root := s.m.Root()
	if in, err := s.drv.IsAncestor(ctx, root, tip, base); err != nil || in {
		return 0, false
	}
	for side, rev := range []string{s.ours, s.theirs} {
		if in, err := s.drv.IsAncestor(ctx, root, tip, rev); err == nil && in {
			return mergeSide(side), true
		}
	}
	return 0, false
}

// startMergeAgainst begins the merge that --against settles, and returns the function that
// backs it out again: a no-op unless this is a dry run, since otherwise the merge is the
// whole point and stays in progress for the caller to commit.
//
// A real merge rather than `git merge-tree`: merge-tree reports conflicted PATHS only,
// while settle.PlanConflicts decides on the conflict KIND, and a modify/delete is the shape no
// merge driver is ever invoked for. (The read-only PR advisor uses merge-tree because it
// needs only the names.)
//
// So a dry run merges for real and aborts, which is why a clean tree is required up
// front: `git merge --abort` does not guarantee uncommitted work survives.
func startMergeAgainst(ctx context.Context, root string, res types.VCSResolution, ref string) (undo func(), err error) {
	dirty, err := res.VCS.DirtyFiles(ctx, root, nil)
	if err != nil {
		return nil, fmt.Errorf("vcs resolve: read tree status: %w", err)
	}
	if len(dirty) > 0 {
		return nil, fmt.Errorf("vcs resolve: --against needs a clean tree, and %d path(s) are uncommitted, commit or stash them first so backing the merge out cannot lose them", len(dirty))
	}
	// The person resolving, as the box knows them: the merge is theirs to conclude.
	if err := res.VCS.StartMerge(ctx, root, ref, types.Person{}); err != nil {
		return nil, fmt.Errorf("vcs resolve: %w", err)
	}
	if !globalCfg.DryRun {
		// The merge is the point: leave it in progress for the caller to commit.
		return func() {}, nil
	}
	return func() {
		if err := res.VCS.AbortMerge(ctx, root); err != nil {
			// Reported, never swallowed: the tree is NOT as this dry run found it, and a
			// caller told "nothing was touched" would go on to do something else in it.
			slog.ErrorContext(ctx, "could not back out the merge --dry-run started; the tree still has it in progress (git merge --abort)",
				attr.Notice(""), attr.Component("vcs resolve"), attr.Error(err))
		}
	}, nil
}

// applyResolution performs the plan: clear markers, record deletions, regenerate once,
// then record everything the regeneration touched.
//
// Every step past the first has already mutated the tree, so failures report how far it
// got. A half-applied resolve otherwise looks like a fresh conflict with the markers
// missing, which `git status` alone cannot explain.
// staleDecls says the declarations came from the committed magusfile because the working
// copy's is mid-merge. It suppresses the regeneration step and nothing else: clearing
// markers and recording paths are decisions ABOUT the conflicts, which either side's
// declarations answer the same way, while regenerating PRODUCES bytes, and a merge that
// touched a generator would have this produce output matching neither side.
func applyResolution(ctx context.Context, root string, rc runConfig, m *magus.Magus, driver types.VCSDriver, plan settle.Plan, staleDecls bool) error {
	if err := driver.KeepIncoming(ctx, m.Root(), slices.Concat(plan.Keep, plan.Rederive)); err != nil {
		return fmt.Errorf("vcs resolve: %w", err)
	}
	if err := driver.RemoveConflicts(ctx, m.Root(), plan.Gone); err != nil {
		return fmt.Errorf("vcs resolve, %s: %w", resolveTreeState(plan, "the conflict markers were already cleared"), err)
	}
	if staleDecls {
		fmt.Printf("vcs resolve: not regenerating - the magusfile is still mid-merge, and a generator it changes would produce bytes matching neither side. Resolve the magusfile, then `%s` to finish.\n", hint.Run.With("generate:rw"))
	} else if err := runRebuildTargets(ctx, root, rc, plan.Rebuild); err != nil {
		return fmt.Errorf("vcs resolve, %s: regenerate: %w", resolveTreeState(plan,
			"the conflict markers were cleared and the deletions recorded, but nothing was marked resolved"), err)
	}
	// The registration is derived from the declared outputs, so a conflict in the file
	// holding it is settled by re-deriving. First point the file has no markers.
	settle.EnsureDriver(ctx, m)

	settled, err := plan.Paths(ctx, m, driver)
	if err != nil {
		return fmt.Errorf("vcs resolve: %w", err)
	}
	// stagePaths, not MarkResolved directly: one pathspec matching nothing aborts the whole
	// call before staging anything, so a single conflict involving a RENAME took the other
	// forty paths down with it: regeneration complete, index untouched, and an error naming
	// a file the rename had legitimately removed. filterStageable splits those out first.
	staged, dropped, err := stagePaths(ctx, m.Root(), driver, settled)
	if err != nil {
		return fmt.Errorf("vcs resolve, %s: %w", resolveTreeState(plan, "regeneration completed"), err)
	}
	fmt.Printf("\nrecorded %d path(s); review before continuing: %s\n", len(staged), driver.ReviewCommand())
	// Named, never silent: a path magus settled and could not record is one the caller has
	// to look at, and the count above would otherwise be the only sign it existed.
	if len(dropped) > 0 {
		fmt.Printf("not recorded (gone from disk and untracked, so there is nothing to stage): %s\n",
			strings.Join(dropped, ", "))
	}
	return unresolvedError(plan)
}

// committedMagusfiles returns the COMMITTED content of every conflicted .buzz file, keyed
// by absolute path, for interp.WithOverlay.
//
// The committed side, not either merge stage: it is the one version guaranteed to parse,
// and the declarations it carries are the ones the tree had before this merge started.
//
// No error return, deliberately: every way this can fail (no VCS, a backend that cannot
// report conflicts or read a revision, a file the VCS will not hand back) means the same
// thing to the only caller, which is "carry on with the load failure you already have". An
// error here could only be discarded, and returning one alongside a nil map is the
// ambiguity `nilnil` exists to catch. Nothing to overlay is an empty map.
func committedMagusfiles(ctx context.Context, root string) map[string]string {
	res, err := vcs.Resolve(ctx, root, "", types.VCSOptions{})
	if err != nil || res.VCS == nil {
		return nil
	}
	conflicts, err := res.VCS.Conflicts(ctx, root)
	if err != nil {
		return nil
	}
	overlay := map[string]string{}
	for _, c := range conflicts {
		if !strings.HasSuffix(c.Path, ".buzz") {
			continue
		}
		// "" is the committed revision in whichever backend this is; naming HEAD here
		// would be correct for git alone.
		content, rerr := res.VCS.ReadFileAt(ctx, root, "", c.Path)
		if rerr != nil {
			return nil
		}
		overlay[filepath.Join(root, filepath.FromSlash(c.Path))] = content
	}
	return overlay
}

// resolveTreeState describes how far the resolve got, for an error message.
func resolveTreeState(plan settle.Plan, reached string) string {
	return fmt.Sprintf("the working tree has been modified (%s), "+
		"to start over abort the merge (`git rebase --abort` or `git merge --abort`), "+
		"to inspect it `git status` now shows %d kept and %d removed path(s)",
		reached, len(plan.Keep)+len(plan.Rederive), len(plan.Gone))
}

// runRebuildTargets runs each target ONCE over every project that needs it. Grouping by
// target keeps this a single build: fifty conflicted files still cost one `magus run
// generate` per distinct target.
func runRebuildTargets(ctx context.Context, root string, rc runConfig, rebuild map[string][]string) error {
	for _, target := range slices.Sorted(maps.Keys(rebuild)) {
		projects := rebuild[target]
		slices.Sort(projects)
		fmt.Printf("regenerating: %s\n", hint.Run.With(target, strings.Join(projects, " ")))
		if err := runTarget(ctx, root, rc, append([]string{target}, projects...)); err != nil {
			return err
		}
	}
	return nil
}

func reportResolution(plan settle.Plan, dryRun bool) {
	verb, goneVerb, rederiveVerb := "resolved", "recorded the deletion of", "re-derived"
	if dryRun {
		verb, goneVerb, rederiveVerb = "would resolve", "would record the deletion of", "would re-derive"
	}
	if len(plan.Keep) > 0 {
		fmt.Printf("%s %d generated file(s), then regenerating them:\n", verb, len(plan.Keep))
		printPaths(plan.Keep)
	}
	if len(plan.Gone) > 0 {
		fmt.Printf("%s %d generated file(s) this workspace no longer tracks:\n", goneVerb, len(plan.Gone))
		printPaths(plan.Gone)
	}
	if len(plan.Rederive) > 0 {
		// Reported because it is not a merge. The managed section is rebuilt from the
		// declared outputs; anything outside it comes from the incoming side, so a
		// hand-written rule only the other side has does not survive.
		fmt.Printf("%s %d file(s) magus maintains, from the workspace rather than by merging:\n",
			rederiveVerb, len(plan.Rederive))
		printPaths(plan.Rederive)
		fmt.Println("  any hand-written rules in these files are taken from the incoming side")
	}
	if len(plan.Manual) > 0 {
		fmt.Printf("left for you: %d conflict(s) magus cannot settle:\n", len(plan.Manual))
		printPaths(plan.Manual)
	}
}

func printPaths(paths []string) {
	for _, p := range paths {
		fmt.Printf("  %s\n", p)
	}
}

// unresolvedError exits non-zero when conflicts remain, so `magus vcs resolve && git
// rebase --continue` cannot skip past one you still have to read.
func unresolvedError(plan settle.Plan) error {
	if len(plan.Manual) == 0 {
		return nil
	}
	return fmt.Errorf("%d conflict(s) still need you, resolve them, stage them, and continue", len(plan.Manual))
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

// settleOwedByHand is `magus vcs resolve` with nothing conflicted. It runs, one run per
// target, the regenerations the merge driver left owed, which `magus doctor` reports,
// stages the result and clears the record. With nothing owed it says so.
func settleOwedByHand(ctx context.Context, root string, rc runConfig, m *magus.Magus, driver types.VCSDriver) error {
	out, err := settle.Hook(ctx, m, vcs.HookOperation{}, buildDefinesTarget(ctx, m), settleRun(root, rc))
	if err != nil {
		return fmt.Errorf("vcs resolve: %w", err)
	}
	if len(out.Ran) == 0 {
		fmt.Println("vcs resolve: nothing to resolve; no conflicted paths and no regeneration owed")
		return nil
	}
	fmt.Printf("vcs resolve: ran %s; staged %d regenerated file(s): %s\n", out.Command(), len(out.Staged), driver.ReviewCommand())
	return nil
}

// resolveVCS returns the active VCS resolution for the workspace.
func resolveVCS(ctx context.Context, root string, m *magus.Magus) (types.VCSResolution, error) {
	wsRoot := m.Root()
	if wsRoot == "" {
		wsRoot = root
	}
	return vcs.Resolve(ctx, wsRoot, "", m.VCSOptions())
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
	fmt.Fprintln(w, "On git and Mercurial a capture is dropped at 30 days, by the next preserve")
	fmt.Fprintf(w, "and by the server's `%s`. Without a server\n", hint.JobRun.With("prune-preserved"))
	fmt.Fprintln(w, "that job is a no-op; run `magus server prune-preserved` instead. On Sapling a")
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
func emitCheckpoint(cp types.VCSCheckpoint) error {
	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}
	switch opts.Format {
	case outputJSON, outputYAML, outputJSONL, outputTemplate:
		return emitFormatted(opts, cp)
	case outputName:
		return emitNames(os.Stdout, []string{cp.Token()})
	}
	fmt.Println(checkpointLine(cp))
	return nil
}

// checkpointLine is the human reading: "<rev> <branch> clean" or "<rev> <branch> dirty
// <digest>", with " preserved <handle>" appended when --preserve minted one. The field
// count is fixed through the dirty word, so a branchless revision (a detached head, jj's
// usual anonymous change) renders "-" rather than collapsing the column and silently
// shifting everything after it.
//
// The handle is on this line because --preserve promises to print one and this is the
// default rendering. Without it the two spellings of the command produced identical
// output while one of them left an object in the user's repository, and the only way back
// to that object was a format flag nobody was told to pass.
func checkpointLine(cp types.VCSCheckpoint) string {
	branch := cp.Branch
	if branch == "" {
		branch = "-"
	}
	if !cp.Dirty {
		return fmt.Sprintf("%s %s clean", cp.Revision, branch)
	}
	line := fmt.Sprintf("%s %s dirty %s", cp.Revision, branch, cp.PatchDigest)
	if cp.Preserved != "" {
		line += " preserved " + cp.Preserved
	}
	return line
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

// The groups a dirty path can be left out by, in the order lsFilesGroups reports a path
// that falls in several.
const (
	lsGroupHeld        = "held"
	lsGroupStaged      = "staged"
	lsGroupUntracked   = "untracked"
	lsGroupUnclaimed   = "unclaimed"
	lsGroupUnexplained = "unexplained"
	lsGroupGenerated   = "generated"
)

type lsFilesGroup struct {
	name string
	has  func(lsFilesPath) bool
}

var lsFilesGroups = []lsFilesGroup{
	{lsGroupHeld, func(p lsFilesPath) bool { return p.Held != "" }},
	{lsGroupStaged, func(p lsFilesPath) bool { return p.Staged }},
	{lsGroupUntracked, func(p lsFilesPath) bool { return p.Untracked }},
	{lsGroupUnclaimed, func(p lsFilesPath) bool { return p.Role != "source" && p.Role != "output" }},
	{lsGroupUnexplained, func(p lsFilesPath) bool { return p.Role == "output" && p.Unexplained }},
	{lsGroupGenerated, func(p lsFilesPath) bool { return p.Role == "output" && !p.Unexplained }},
}

// lsFilesPath is one dirty path with every fact the selection reads.
type lsFilesPath struct {
	Path                           string
	Role                           string // types.FileEntry.Role
	Project                        string
	Held                           string // the other live job that writes it
	Untracked, Staged, Unexplained bool
}

// lsFilesFilter is what the caller asked for. Patterns and Exclude are write-path
// patterns already made workspace-relative; Include is keyed by group.
type lsFilesFilter struct {
	Patterns, Exclude, Projects []string
	Include                     map[string]bool
}

// selectLsFiles narrows paths by the filter, then leaves out every group the filter does
// not include. unmatched names each pattern and project that covered no dirty path.
func selectLsFiles(paths []lsFilesPath, f lsFilesFilter) (list types.LsFiles, unmatched []string) {
	hit := map[string]bool{}
	var narrowed []lsFilesPath
	for _, p := range paths {
		if lsFilesKeeps(p, f, hit) {
			narrowed = append(narrowed, p)
		}
	}
	for _, want := range slices.Concat(f.Patterns, f.Projects) {
		if !hit[want] {
			unmatched = append(unmatched, want)
		}
	}

	list.Files = []string{}
	byGroup := map[string]*types.LsFilesExcluded{}
	for _, p := range narrowed {
		i := slices.IndexFunc(lsFilesGroups, func(g lsFilesGroup) bool { return g.has(p) && !f.Include[g.name] })
		if i < 0 {
			list.Files = append(list.Files, p.Path)
			continue
		}
		name := lsFilesGroups[i].name
		e := byGroup[name]
		if e == nil {
			e = &types.LsFilesExcluded{Group: name, Flag: "--include-" + name}
			byGroup[name] = e
		}
		e.Paths = append(e.Paths, p.Path)
		if name == lsGroupHeld {
			if e.Holders == nil {
				e.Holders = map[string]string{}
			}
			e.Holders[p.Path] = p.Held
		}
	}
	slices.Sort(list.Files)
	for _, g := range lsFilesGroups {
		e := byGroup[g.name]
		if e == nil {
			continue
		}
		slices.Sort(e.Paths)
		if g.name == lsGroupUnexplained {
			// No declared input moved, so this is ClassifyDrift's second fork: skew against a
			// differently-versioned magus, or a non-deterministic generator.
			code, msg := types.ClassifyDrift(false, version)
			e.Code, e.Message, e.URL = string(code), msg, types.CodeURL(code)
		}
		list.Excluded = append(list.Excluded, *e)
	}
	return list, unmatched
}

// lsFilesKeeps reports whether p passes the filter's patterns, exclusions and projects,
// marking in hit each pattern and project that covered it.
func lsFilesKeeps(p lsFilesPath, f lsFilesFilter, hit map[string]bool) bool {
	covered := len(f.Patterns) == 0
	for _, pat := range f.Patterns {
		if job.Covers(pat, p.Path) {
			hit[pat], covered = true, true
		}
	}
	if !covered || slices.ContainsFunc(f.Exclude, func(pat string) bool { return job.Covers(pat, p.Path) }) {
		return false
	}
	if len(f.Projects) == 0 {
		return true
	}
	owned := false
	for _, proj := range f.Projects {
		if path.Clean(proj) == path.Clean(cmp.Or(p.Project, ".")) {
			hit[proj], owned = true, true
		}
	}
	return owned
}

// lsFilesRefusal refuses an --include flag for a state the backend does not keep, rather
// than answering it with nothing.
func lsFilesRefusal(driver types.VCSDriver, include map[string]bool) error {
	if _, ok := driver.(types.StagedFileReporter); include[lsGroupStaged] && !ok {
		return fmt.Errorf("vcs ls-files: --include-staged: %s keeps no staging index, so no path is ever left out as staged", driver.Name())
	}
	if include[lsGroupUntracked] && !keepsUntracked(driver) {
		return fmt.Errorf("vcs ls-files: --include-untracked: %s tracks every file in the working copy, so no path is ever left out as untracked", driver.Name())
	}
	return nil
}

// keepsUntracked reports whether the backend has files present and not tracked. jj
// snapshots every file into the working-copy commit, and its TrackedFiles answers for
// the parent commit, which would call each new file untracked.
func keepsUntracked(driver types.VCSDriver) bool { return driver.Name() != "jj" }

// vcsLsFilesCommand lists the dirty paths a commit of this change wants.
func vcsLsFilesCommand(ctx context.Context, root string, args []string) error {
	var lf *flaggen.VCSLsFilesFlags
	var exclude, projects listFlag
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
		lsGroupUntracked:   lf.IncludeUntracked,
		lsGroupGenerated:   lf.IncludeGenerated,
		lsGroupUnexplained: lf.IncludeUnexplained,
		lsGroupUnclaimed:   lf.IncludeUnclaimed,
		lsGroupHeld:        lf.IncludeHeld,
		lsGroupStaged:      lf.IncludeStaged,
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
	if err := lsFilesRefusal(res.VCS, include); err != nil {
		return exit.Usagef("%s", err)
	}
	patterns, err := workspaceRelPatterns(root, pos)
	if err != nil {
		return err
	}
	excluded, err := workspaceRelPatterns(root, exclude)
	if err != nil {
		return err
	}

	dirty, err := res.VCS.DirtyFiles(ctx, root, nil)
	if err != nil {
		return fmt.Errorf("vcs ls-files: list dirty files: %w", err)
	}
	facts, err := lsFilesFacts(ctx, ws, res, root, dirty)
	if err != nil {
		return err
	}
	list, unmatched := selectLsFiles(facts, lsFilesFilter{Patterns: patterns, Exclude: excluded, Projects: projects, Include: include})
	for _, u := range unmatched {
		slog.WarnContext(ctx, u+" matches no dirty path", attr.Notice(""), attr.Component("magus"))
	}
	reportExcluded(ctx, list.Excluded)

	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}
	switch opts.Format {
	case outputJSON, outputYAML, outputJSONL, outputTemplate:
		return emitFormatted(opts, list)
	}
	return emitNames(os.Stdout, list.Files)
}

// lsFilesFacts gathers what the selection reads about each dirty path.
func lsFilesFacts(ctx context.Context, ws types.WorkspaceRepository, res types.VCSResolution, root string, dirty []string) ([]lsFilesPath, error) {
	if len(dirty) == 0 {
		return nil, nil
	}
	files, err := ws.ClassifyFiles(ctx, dirty)
	if err != nil {
		return nil, err
	}
	_, unexplained := types.SplitExplainedOutputs(files, types.SourcesChangedSinceBase(ctx, ws, res, root))
	untracked := map[string]bool{}
	if keepsUntracked(res.VCS) {
		tracked, err := res.VCS.TrackedFiles(ctx, root, dirty)
		if err != nil {
			return nil, fmt.Errorf("vcs ls-files: %w", err)
		}
		for _, p := range dirty {
			untracked[p] = !slices.Contains(tracked, p)
		}
	}
	var staged []string
	if sr, ok := res.VCS.(types.StagedFileReporter); ok {
		if staged, err = sr.StagedFiles(ctx, root); err != nil {
			return nil, fmt.Errorf("vcs ls-files: %w", err)
		}
	}
	held, err := heldForOtherLeases(ctx, root, dirty)
	if err != nil {
		return nil, err
	}
	out := make([]lsFilesPath, 0, len(files))
	for _, f := range files {
		out = append(out, lsFilesPath{
			Path:        f.Path,
			Role:        f.Role,
			Project:     f.Project,
			Held:        held[f.Path],
			Untracked:   untracked[f.Path],
			Staged:      slices.Contains(staged, f.Path),
			Unexplained: slices.Contains(unexplained, f.Path),
		})
	}
	return out, nil
}

// reportExcluded names on stderr, in every output format, each group left out and the
// flag that lists it.
func reportExcluded(ctx context.Context, groups []types.LsFilesExcluded) {
	for _, g := range groups {
		paths := slices.Clone(g.Paths)
		for i, p := range paths {
			if lease := g.Holders[p]; lease != "" {
				paths[i] = fmt.Sprintf("%s (live job %s writes it)", p, lease)
			}
		}
		msg := fmt.Sprintf("left out %d %s path(s); %s lists them: %s", len(paths), g.Group, g.Flag, strings.Join(paths, ", "))
		if g.Code != "" {
			msg += fmt.Sprintf("; %s: %s %s", g.Code, g.Message, g.URL)
		}
		slog.InfoContext(ctx, msg, attr.Notice(""), attr.Component("magus"))
	}
}

// workspaceRelPatterns is workspaceRelPaths for write-path patterns: the path half of
// each (types.SplitClaim) is made workspace-relative, and a literal `#` in it is escaped
// again so job.Covers reads it as part of the path.
func workspaceRelPatterns(root string, patterns []string) ([]string, error) {
	paths := make([]string, len(patterns))
	decls := make([]string, len(patterns))
	for i, p := range patterns {
		paths[i], decls[i] = types.SplitClaim(p)
	}
	rel, err := workspaceRelPaths(root, paths)
	if err != nil {
		return nil, err
	}
	for i, p := range patterns {
		rel[i] = strings.ReplaceAll(rel[i], "#", `\#`)
		if types.HasClaim(p) {
			rel[i] += "#" + decls[i]
		}
	}
	return rel, nil
}

// workspaceRelPaths turns the paths you typed into workspace-relative ones.
//
// ClassifyFiles, the declared-output globs, and the index write are all
// workspace-relative, but you run this from wherever you are. Passing the typed path
// through made `magus vcs ls-files foo.go` from a subdirectory miss, or address a different
// file, with no error: an unmatched path just reports as undeclared.
func workspaceRelPaths(root string, paths []string) ([]string, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	// An empty root is a CALLER bug, not a path the user typed wrong. Left to run, it
	// reports every path as "outside the workspace at " with nothing after "at", which
	// blames the argument for a mistake it did not make.
	if root == "" {
		return nil, fmt.Errorf("no workspace root resolved, cannot place %q", paths[0])
	}
	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("resolve working directory: %w", err)
	}
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		abs := p
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(cwd, p)
		}
		rel, err := filepath.Rel(root, abs)
		if err != nil || strings.HasPrefix(rel, "..") {
			return nil, fmt.Errorf("%q is outside the workspace at %s", p, root)
		}
		out = append(out, filepath.ToSlash(rel))
	}
	return out, nil
}

// stagePaths shells out to git for the index write itself.
//
// One pathspec that matches nothing aborts the WHOLE `git add` before staging anything,
// so a single stale declaration silently loses every other path in the call.
// filterStageable splits them first: on disk is staged; gone from disk but still tracked
// is ALSO staged, since that is how a deletion or rename is recorded. Only a path that is
// neither is dropped, and reported rather than discarded.
//
// Paths are passed after `--` so one beginning with a dash is unambiguously a path.
//
// It returns what it staged and dropped rather than printing either, so `-o json` gets
// the same answer the terminal does.
func stagePaths(ctx context.Context, root string, driver types.VCSDriver, paths []string) (staged, dropped []string, err error) {
	stageable, dropped, err := filterStageable(ctx, root, driver, paths)
	if err != nil {
		return nil, nil, err
	}
	if len(stageable) == 0 {
		return []string{}, dropped, nil
	}
	// MarkResolved batches the pathspecs, which matters here: `vcs resolve` over a whole
	// dirty tree is the largest path list these commands hand to the VCS.
	if err := driver.MarkResolved(ctx, root, stageable); err != nil {
		return nil, nil, fmt.Errorf("stage: %w", err)
	}
	return stageable, dropped, nil
}

// heldForOtherLeases is the subset of paths a whole-tree listing leaves out, each
// with the live lease it belongs to: the ones another lease's write paths cover in this
// checkout. Two holders sharing a checkout share its dirty tree, and staging the other's
// uncommitted sources would land them under this caller's commit.
func heldForOtherLeases(ctx context.Context, root string, paths []string) (map[string]string, error) {
	loc, err := jobPlace(root)
	if err != nil {
		return nil, fmt.Errorf("place the job store to read which leases share this checkout: %w", err)
	}
	rows, err := job.NewStore(loc).List()
	if err != nil {
		return nil, fmt.Errorf("read the job store for the paths other leases in this checkout write: %w", err)
	}
	lease, _ := job.RunLease(ctx, loc)
	return otherLeasesPaths(rows, root, lease, paths), nil
}

// otherLeasesPaths maps each of paths that a live row in the checkout at root, other than
// lease's own, declares among its write paths to that row's id. Two rows covering one path
// both name it, unless lease is one of them.
func otherLeasesPaths(rows []types.Job, root, lease string, paths []string) map[string]string {
	here := slices.DeleteFunc(slices.Clone(rows), func(r types.Job) bool {
		return r.CheckoutRoot == "" || !sameDir(r.CheckoutRoot, root)
	})
	held := map[string]string{}
	for _, p := range paths {
		owner, ok := job.AttributeWrite(here, p)
		switch {
		case ok && owner.Job != lease:
			held[p] = owner.Job
		case !ok && len(owner.Ambiguous) > 0 && !slices.Contains(owner.Ambiguous, lease):
			held[p] = strings.Join(owner.Ambiguous, " and ")
		}
	}
	return held
}

// filterStageable separates paths git add can actually act on from ones that
// would abort the whole `git add` call. See stagePaths for why a missing-but-
// tracked path (a deletion or the old half of a rename) must still be staged.
func filterStageable(ctx context.Context, root string, driver types.VCSDriver, paths []string) (stageable, dropped []string, err error) {
	var maybeGone []string
	for _, p := range paths {
		if _, statErr := os.Stat(filepath.Join(root, p)); statErr == nil {
			stageable = append(stageable, p)
			continue
		}
		maybeGone = append(maybeGone, p)
	}
	if len(maybeGone) == 0 {
		return stageable, nil, nil
	}
	// The abort-the-whole-invocation behavior this guards against is git's. Other
	// backends get the paths passed through rather than probed.
	if driver.Name() != "git" {
		return append(stageable, maybeGone...), nil, nil
	}

	// The tracked listing does not care whether a file is still on disk, so a
	// tracked-but-deleted path is kept and its deletion gets recorded.
	known, err := driver.TrackedFiles(ctx, root, maybeGone)
	if err != nil {
		return nil, nil, fmt.Errorf("stage: %w", err)
	}
	tracked := make(map[string]bool, len(known))
	for _, p := range known {
		tracked[p] = true
	}
	for _, p := range maybeGone {
		if tracked[p] {
			stageable = append(stageable, p)
		} else {
			dropped = append(dropped, p)
		}
	}
	return stageable, dropped, nil
}
