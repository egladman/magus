package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/libs/cmdline/exit"
)

const harnessLinksRequestLimit = 1 << 20

type harnessLinksRequest struct {
	Links map[string]string `json:"links"`
}

type harnessLinksOutput struct {
	Files []string `json:"files"`
}

type harnessLinkPlan struct {
	destination string
	actual      string
	parent      string
	target      string
	sourceDir   string
	current     bool
}

func agentHarnessLinksCommand(ctx context.Context, rootOverride string, args []string) error {
	fset := flag.NewFlagSet("agent harness link", flag.ContinueOnError)
	stdin := fset.Bool("stdin", false, "read destination-to-source links as JSON from stdin")
	bindDisplayFlags(fset)
	fset.Usage = func() { agentHarnessUsage(fset.Output()) }
	if err := fset.Parse(reorderFlagsFirst(fset, args)); err != nil {
		return err
	}
	if len(fset.Args()) != 0 {
		return exit.Usagef("magus agent harness link: positional arguments are not accepted")
	}
	if !*stdin {
		return exit.Usagef("magus agent harness link: --stdin is required")
	}
	root := resolveRootOrEmpty(rootOverride)
	if root == "" {
		return fmt.Errorf("magus agent harness link: no workspace here: run it from inside one or pass --root <path>")
	}
	lease, _, err := checkoutLease(ctx, root)
	if err != nil {
		return fmt.Errorf("magus agent harness link: %w", err)
	}
	if lease != "" {
		return fmt.Errorf("magus agent harness link: bound job %q cannot link workspace sources", lease)
	}
	data, err := io.ReadAll(io.LimitReader(os.Stdin, harnessLinksRequestLimit+1))
	if err != nil {
		return fmt.Errorf("magus agent harness link: read stdin: %w", err)
	}
	if len(data) > harnessLinksRequestLimit {
		return fmt.Errorf("magus agent harness link: request exceeds %d bytes", harnessLinksRequestLimit)
	}
	var request harnessLinksRequest
	if err := json.UnmarshalStrict(data, &request); err != nil {
		return fmt.Errorf("magus agent harness link: decode request: %w", err)
	}
	if request.Links == nil {
		return fmt.Errorf("magus agent harness link: request must contain a links object")
	}
	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}
	files, err := installHarnessSourceLinks(ctx, root, request.Links, globalCfg.DryRun)
	if err != nil {
		return fmt.Errorf("magus agent harness link: %w", err)
	}
	if opts.Format == outputText {
		verb := "linked"
		if globalCfg.DryRun {
			verb = "would link"
		}
		for _, path := range files {
			fmt.Fprintf(os.Stdout, "%s %s\n", verb, path)
		}
		return nil
	}
	return emitRecord(opts, harnessLinksOutput{Files: files}, files)
}

func installHarnessSourceLinks(ctx context.Context, root string, links map[string]string, dryRun bool) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, fmt.Errorf("resolve workspace root: %w", err)
	}
	canonicalRoot, err = filepath.Abs(canonicalRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve workspace root: %w", err)
	}
	destinations := make([]string, 0, len(links))
	for destination := range links {
		destinations = append(destinations, destination)
	}
	slices.Sort(destinations)
	plans := make([]harnessLinkPlan, 0, len(destinations))
	for _, destination := range destinations {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		source := links[destination]
		if err := validHarnessLinkPath(destination); err != nil {
			return nil, fmt.Errorf("destination %q: %w", destination, err)
		}
		if err := validHarnessLinkPath(source); err != nil {
			return nil, fmt.Errorf("source for %q: %w", destination, err)
		}
		sourcePath := filepath.Join(canonicalRoot, source)
		sourceInfo, err := os.Stat(sourcePath)
		if err != nil {
			return nil, fmt.Errorf("source %q: %w", source, err)
		}
		if !sourceInfo.Mode().IsRegular() && !sourceInfo.IsDir() {
			return nil, fmt.Errorf("source %q must be a regular file or directory", source)
		}
		canonicalSource, err := filepath.EvalSymlinks(sourcePath)
		if err != nil {
			return nil, fmt.Errorf("resolve source %q: %w", source, err)
		}
		canonicalSource, err = filepath.Abs(canonicalSource)
		if err != nil || !pathWithin(canonicalRoot, canonicalSource) {
			return nil, fmt.Errorf("source %q resolves outside the workspace", source)
		}
		if hasGitPathComponent(canonicalRoot, canonicalSource) {
			return nil, fmt.Errorf("source %q resolves through a .git path component", source)
		}
		parentPath := filepath.Join(canonicalRoot, filepath.Dir(destination))
		canonicalParent, err := resolveHarnessLinkParent(canonicalRoot, parentPath)
		if err != nil {
			return nil, fmt.Errorf("destination %q: %w", destination, err)
		}
		resolvedDestination := filepath.Join(canonicalParent, filepath.Base(destination))
		if !pathWithin(canonicalRoot, resolvedDestination) {
			return nil, fmt.Errorf("destination %q resolves outside the workspace", destination)
		}
		if sourceInfo.IsDir() && (pathWithin(canonicalSource, resolvedDestination) || pathWithin(resolvedDestination, canonicalSource)) {
			if filepath.Clean(canonicalSource) != filepath.Clean(resolvedDestination) {
				return nil, fmt.Errorf("destination %q overlaps source directory %q", destination, source)
			}
		}
		plan := harnessLinkPlan{destination: destination, actual: resolvedDestination, parent: canonicalParent}
		if sourceInfo.IsDir() {
			plan.sourceDir = canonicalSource
		}
		plan.target, err = filepath.Rel(canonicalParent, canonicalSource)
		if err != nil {
			return nil, fmt.Errorf("relative link for %q: %w", destination, err)
		}
		current, err := harnessLinkDestinationCurrent(filepath.Join(canonicalRoot, destination), canonicalSource, sourceInfo)
		if err != nil {
			return nil, fmt.Errorf("destination %q: %w", destination, err)
		}
		plan.current = current
		plans = append(plans, plan)
	}
	for i, plan := range plans {
		for _, other := range plans[i+1:] {
			left := plan.actual
			right := other.actual
			if pathWithin(left, right) || pathWithin(right, left) {
				return nil, fmt.Errorf("destinations %q and %q overlap", plan.destination, other.destination)
			}
		}
	}
	for _, plan := range plans {
		if plan.current {
			continue
		}
		for _, source := range plans {
			if source.sourceDir != "" && pathWithin(source.sourceDir, plan.actual) {
				return nil, fmt.Errorf("destination %q would write inside source directory %q", plan.destination, links[source.destination])
			}
		}
	}
	files := make([]string, 0, len(plans))
	for _, plan := range plans {
		if !plan.current {
			files = append(files, plan.destination)
		}
	}
	if dryRun || len(files) == 0 {
		return files, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	for _, plan := range plans {
		if plan.current {
			continue
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := os.MkdirAll(plan.parent, 0o755); err != nil {
			return nil, fmt.Errorf("create parent for %q: %w", plan.destination, err)
		}
		if err := os.Symlink(plan.target, filepath.Join(canonicalRoot, plan.destination)); err != nil {
			return nil, fmt.Errorf("link %q: %w", plan.destination, err)
		}
	}
	return files, nil
}

func validHarnessLinkPath(path string) error {
	if path == "" || filepath.IsAbs(path) || filepath.VolumeName(path) != "" || filepath.Clean(path) != path || path == "." {
		return errors.New("must be a clean, nonempty workspace-relative path")
	}
	for _, component := range strings.Split(path, string(filepath.Separator)) {
		if component == ".." || component == "." || component == ".git" {
			return fmt.Errorf("contains forbidden path component %q", component)
		}
	}
	return nil
}

func resolveHarnessLinkParent(root, parent string) (string, error) {
	missing := []string{}
	for candidate := parent; ; candidate = filepath.Dir(candidate) {
		_, err := os.Lstat(candidate)
		if err == nil {
			resolved, err := filepath.EvalSymlinks(candidate)
			if err != nil {
				return "", fmt.Errorf("resolve parent %q: %w", candidate, err)
			}
			resolved, err = filepath.Abs(resolved)
			if err != nil || !pathWithin(root, resolved) {
				return "", fmt.Errorf("parent resolves outside the workspace")
			}
			if hasGitPathComponent(root, resolved) {
				return "", fmt.Errorf("parent resolves through a .git path component")
			}
			resolvedInfo, err := os.Stat(resolved)
			if err != nil || !resolvedInfo.IsDir() {
				return "", fmt.Errorf("parent component %q is not a directory", candidate)
			}
			for i := len(missing) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, missing[i])
			}
			return resolved, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("inspect parent %q: %w", candidate, err)
		}
		if candidate == root {
			return "", fmt.Errorf("workspace root is missing")
		}
		missing = append(missing, filepath.Base(candidate))
	}
}

func harnessLinkDestinationCurrent(destination, source string, sourceInfo os.FileInfo) (bool, error) {
	info, err := os.Lstat(destination)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		resolved, err := filepath.EvalSymlinks(destination)
		if err != nil {
			return false, fmt.Errorf("refuse dangling or unreadable symlink: %w", err)
		}
		resolved, err = filepath.Abs(resolved)
		if err != nil {
			return false, err
		}
		if filepath.Clean(resolved) == filepath.Clean(source) {
			return true, nil
		}
		return false, errors.New("refuse conflicting symlink")
	}
	if info.IsDir() && sourceInfo.IsDir() {
		resolved, err := filepath.EvalSymlinks(destination)
		if err == nil {
			resolved, _ = filepath.Abs(resolved)
			if filepath.Clean(resolved) == filepath.Clean(source) {
				return true, nil
			}
		}
		return false, errors.New("refuse existing directory")
	}
	if info.Mode().IsRegular() && sourceInfo.Mode().IsRegular() {
		destBytes, err := os.ReadFile(destination)
		if err != nil {
			return false, err
		}
		sourceBytes, err := os.ReadFile(source)
		if err != nil {
			return false, err
		}
		if bytes.Equal(destBytes, sourceBytes) {
			return true, nil
		}
		return false, errors.New("refuse conflicting file")
	}
	return false, errors.New("refuse existing destination")
}

func pathWithin(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	return err == nil && (rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))))
}

func hasGitPathComponent(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." {
		return false
	}
	for _, component := range strings.Split(rel, string(filepath.Separator)) {
		if component == ".git" {
			return true
		}
	}
	return false
}
