package agent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/file"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/types"
)

// HarnessRuntimeAsset is one resolved runtime file; its bytes are never decoded as text.
type HarnessRuntimeAsset struct {
	Path    string `json:"path"`
	Content []byte `json:"content"`
}

func validateHarnessRuntime(d HarnessDescriptor) error {
	if d.Guard != "" && d.Guard != "none" && d.Guard != "hooks" && d.Guard != "plugin" {
		return fmt.Errorf("guard must be hooks, plugin or none")
	}
	if len(d.Assets) > 0 && d.Guard == "" {
		return fmt.Errorf("a harness with runtime assets must declare its guard transport")
	}
	if d.Guard == "plugin" && (len(d.Assets) == 0 || len(d.Probe) == 0) {
		return fmt.Errorf("a plugin harness must declare runtime assets and an activation probe")
	}
	if d.Guard == "none" && len(d.Hooks) > 0 {
		return fmt.Errorf("a harness declaring hooks cannot declare guard none")
	}
	seen := map[string]bool{}
	for _, a := range d.Assets {
		if !filepath.IsLocal(a.Path) || a.Path == "." || a.Content == nil {
			return fmt.Errorf("runtime asset requires a workspace-relative path and content: %q", a.Path)
		}
		key := filepath.Clean(a.Path)
		if seen[key] {
			return fmt.Errorf("duplicate runtime asset %s", a.Path)
		}
		seen[key] = true
		if key == filepath.Clean(d.Config.Path) {
			return fmt.Errorf("runtime asset %s also names the config document", a.Path)
		}
	}
	return nil
}

type harnessAssetReceipt struct {
	Path   string `json:"path"`
	Digest string `json:"digest"`
}

func assetDigest(body []byte) string { return fmt.Sprintf("%x", sha256.Sum256(body)) }

func assetPath(root, relative string) (string, error) {
	if !filepath.IsLocal(relative) || relative == "." {
		return "", fmt.Errorf("invalid harness path %q", relative)
	}
	current := root
	for _, part := range strings.Split(filepath.Clean(relative), string(filepath.Separator)) {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("harness path %s contains a symlink", relative)
		}
	}
	return filepath.Join(root, relative), nil
}

// readAssetReceipts maps each asset path to the digest magus last wrote there.
func readAssetReceipts(path string) (map[string]string, error) {
	known := map[string]string{}
	lines, err := file.ReadLines(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	for _, line := range lines.Lines {
		var entry harnessAssetReceipt
		if err := json.Unmarshal(line.Text, &entry); err != nil {
			return nil, fmt.Errorf("read harness asset ownership: %w", err)
		}
		known[entry.Path] = entry.Digest
	}
	return known, nil
}

func planHarnessAssets(root string, d HarnessDescriptor, plan *types.HarnessInstallation) error {
	if len(d.Assets) == 0 {
		return nil
	}
	receipt := filepath.Join(".magus", "harnesses", d.ID, "assets.jsonl")
	receiptPath, err := assetPath(root, receipt)
	if err != nil {
		return err
	}
	known, err := readAssetReceipts(receiptPath)
	if err != nil {
		return err
	}
	var receipts strings.Builder
	for _, a := range d.Assets {
		path, err := assetPath(root, a.Path)
		if err != nil {
			return err
		}
		body, err := os.ReadFile(path)
		exists := err == nil
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		if exists && !bytes.Equal(body, a.Content) && known[a.Path] != assetDigest(body) {
			return fmt.Errorf("harness asset %s has unrecognized local changes, so preserve or move it before installing", a.Path)
		}
		if !exists || !bytes.Equal(body, a.Content) {
			if err := addPlanFile(plan, a.Path, types.HarnessFile{Exists: exists, Bytes: a.Content, Changes: []types.HarnessChange{{Op: types.HarnessWrite}}}); err != nil {
				return err
			}
		}
		encoded, err := json.Marshal(harnessAssetReceipt{Path: a.Path, Digest: assetDigest(a.Content)})
		if err != nil {
			return err
		}
		receipts.Write(encoded)
		receipts.WriteByte('\n')
	}
	state, err := planWholeFile(root, receipt, receipts.String())
	if err != nil {
		return err
	}
	return addPlanFile(plan, receipt, state)
}

// ApplyHarnessInstallation applies the same file changes describe and bootstrap use.
// Planning and serialization finish before the first write; unknown asset edits fail there.
func ApplyHarnessInstallation(ctx context.Context, root string, plan types.HarnessInstallation, dryRun bool) ([]string, error) {
	paths := slices.Sorted(maps.Keys(plan.Files))
	bodies, err := harnessFileBodies(ctx, root, plan, paths)
	if err != nil {
		return nil, err
	}
	if dryRun {
		return paths, nil
	}
	// Ownership receipts are written last, so a failed install cannot claim bytes not written.
	slices.SortStableFunc(paths, receiptsLast)
	if err := writeHarnessFiles(root, paths, bodies); err != nil {
		return nil, err
	}
	return paths, nil
}

func harnessFileBodies(ctx context.Context, root string, plan types.HarnessInstallation, paths []string) (map[string][]byte, error) {
	bodies := map[string][]byte{}
	for _, relative := range paths {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		path, err := assetPath(root, relative)
		if err != nil {
			return nil, err
		}
		if bodies[relative], err = harnessFileBody(path, plan.Files[relative]); err != nil {
			return nil, err
		}
	}
	return bodies, nil
}

// harnessFileBody is the whole file change writes to path: its bytes or
// content, else its fragment merged into the JSON document already there.
func harnessFileBody(path string, change types.HarnessFile) ([]byte, error) {
	if change.Bytes != nil {
		return change.Bytes, nil
	}
	if change.Content != "" {
		return []byte(change.Content), nil
	}
	doc, err := readHarnessDocument(path)
	if err != nil {
		return nil, err
	}
	mergeHarnessFragment(doc, change.Fragment)
	body, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(body, '\n'), nil
}

func readHarnessDocument(path string) (map[string]any, error) {
	doc := map[string]any{}
	existing, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	if len(existing) == 0 {
		return doc, nil
	}
	if err := decodeHarnessJSON(existing, &doc); err != nil {
		return nil, err
	}
	return doc, nil
}

func receiptsLast(a, b string) int {
	aReceipt := strings.HasSuffix(a, "assets.jsonl")
	bReceipt := strings.HasSuffix(b, "assets.jsonl")
	if aReceipt == bReceipt {
		return strings.Compare(a, b)
	}
	if aReceipt {
		return 1
	}
	return -1
}

func writeHarnessFiles(root string, paths []string, bodies map[string][]byte) error {
	for _, relative := range paths {
		path, err := assetPath(root, relative)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := file.WriteFileAtomic(path, bodies[relative], 0o644); err != nil {
			return err
		}
	}
	return nil
}

func mergeHarnessFragment(into, fragment map[string]any) {
	for key, value := range fragment {
		object, ok := value.(map[string]any)
		if !ok {
			into[key] = value
			continue
		}
		target, ok := into[key].(map[string]any)
		if !ok {
			target = map[string]any{}
			into[key] = target
		}
		mergeHarnessFragment(target, object)
	}
}

func verifyHarnessPlugin(ctx context.Context, root string, d HarnessDescriptor, result HarnessVerification) (HarnessVerification, error) {
	for _, a := range d.Assets {
		path, err := assetPath(root, a.Path)
		if err != nil {
			return result, err
		}
		body, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(body, a.Content) {
			result.Status = HarnessUncovered
			result.Reason = fmt.Sprintf("required runtime asset %s is missing or differs; run magus agent harness install --id %s", a.Path, d.ID)
			return result, nil
		}
	}
	if _, err := exec.LookPath(d.Probe[0]); err != nil {
		result.Status = HarnessUnprobed
		result.Reason = fmt.Sprintf("runtime assets are current, but the activation probe requires %s", d.Probe[0])
		return result, nil
	}
	probeCtx, cancel := context.WithTimeout(ctx, ProbeTimeout(ctx))
	defer cancel()
	command := exec.CommandContext(probeCtx, d.Probe[0], d.Probe[1:]...)
	command.Dir = root
	output, err := command.CombinedOutput()
	if err != nil {
		result.Status = HarnessUncovered
		result.Reason = fmt.Sprintf("plugin activation probe failed: %v: %s", err, output)
		return result, nil
	}
	result.Status = HarnessVerified
	result.Reason = "runtime assets match and the installed plugin passed its synthetic activation probe; restart the harness to load upgrades"
	verifyHarnessMCP(d, &result)
	return result, nil
}

// InstallHarnessSourceLinks links each workspace-relative destination in links
// to its source with a relative symlink. The whole batch is checked before the
// first write. A destination that already holds its source is left alone.
// It returns the destinations it linked, or would link when dryRun is set.
func InstallHarnessSourceLinks(ctx context.Context, root string, links map[string]string, dryRun bool) ([]string, error) {
	plans, err := planHarnessLinks(ctx, root, links)
	if err != nil {
		return nil, err
	}
	files := pendingHarnessLinks(plans)
	if dryRun || len(files) == 0 {
		return files, nil
	}
	if err := writeHarnessLinks(ctx, plans); err != nil {
		return nil, err
	}
	return files, nil
}

// harnessLink is one destination after preflight.
type harnessLink struct {
	destination string // as requested
	source      string // as requested
	link        string // the destination under the canonical root
	actual      string // the destination with its parent resolved
	parent      string
	target      string // the relative symlink text
	sourceDir   string // the canonical source, when it is a directory
	current     bool
}

// harnessLinkSource is a source resolved inside the workspace.
type harnessLinkSource struct {
	name string
	path string
	info os.FileInfo
}

func planHarnessLinks(ctx context.Context, root string, links map[string]string) ([]harnessLink, error) {
	canonicalRoot, err := canonicalPath(root)
	if err != nil {
		return nil, fmt.Errorf("resolve workspace root: %w", err)
	}
	plans := make([]harnessLink, 0, len(links))
	for _, destination := range slices.Sorted(maps.Keys(links)) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		plan, err := planHarnessLink(canonicalRoot, destination, links[destination])
		if err != nil {
			return nil, err
		}
		plans = append(plans, plan)
	}
	return plans, checkHarnessLinkBatch(plans)
}

func checkHarnessLinkBatch(plans []harnessLink) error {
	if err := checkDestinationOverlaps(plans); err != nil {
		return err
	}
	return checkWritesInsideSources(plans)
}

func planHarnessLink(root, destination, source string) (harnessLink, error) {
	if err := validHarnessLinkPath(destination); err != nil {
		return harnessLink{}, fmt.Errorf("destination %q: %w", destination, err)
	}
	if err := validHarnessLinkPath(source); err != nil {
		return harnessLink{}, fmt.Errorf("source for %q: %w", destination, err)
	}
	src, err := resolveHarnessLinkSource(root, source)
	if err != nil {
		return harnessLink{}, err
	}
	plan, err := placeHarnessLink(root, destination, src)
	if err != nil {
		return harnessLink{}, fmt.Errorf("destination %q: %w", destination, err)
	}
	return plan, nil
}

func validHarnessLinkPath(path string) error {
	if !filepath.IsLocal(path) || path == "." || filepath.Clean(path) != path {
		return errors.New("must be a clean, nonempty workspace-relative path")
	}
	if slices.Contains(strings.Split(path, string(filepath.Separator)), ".git") {
		return errors.New(`contains forbidden path component ".git"`)
	}
	return nil
}

func resolveHarnessLinkSource(root, source string) (harnessLinkSource, error) {
	info, err := os.Stat(filepath.Join(root, source))
	if err != nil {
		return harnessLinkSource{}, fmt.Errorf("source %q: %w", source, err)
	}
	if !linkableSource(info) {
		return harnessLinkSource{}, fmt.Errorf("source %q must be a regular file or directory", source)
	}
	resolved, err := canonicalPath(filepath.Join(root, source))
	if err != nil {
		return harnessLinkSource{}, fmt.Errorf("resolve source %q: %w", source, err)
	}
	if err := confineToWorkspace(root, resolved); err != nil {
		return harnessLinkSource{}, fmt.Errorf("source %q: %w", source, err)
	}
	return harnessLinkSource{name: source, path: resolved, info: info}, nil
}

func linkableSource(info os.FileInfo) bool { return info.Mode().IsRegular() || info.IsDir() }

func placeHarnessLink(root, destination string, src harnessLinkSource) (harnessLink, error) {
	parent, err := resolveHarnessLinkParent(root, filepath.Join(root, filepath.Dir(destination)))
	if err != nil {
		return harnessLink{}, err
	}
	actual := filepath.Join(parent, filepath.Base(destination))
	if !pathWithin(root, actual) {
		return harnessLink{}, errors.New("resolves outside the workspace")
	}
	if err := checkSourceOverlap(actual, src); err != nil {
		return harnessLink{}, err
	}
	plan := harnessLink{destination: destination, source: src.name, link: filepath.Join(root, destination), actual: actual, parent: parent}
	if src.info.IsDir() {
		plan.sourceDir = src.path
	}
	return finishHarnessLink(plan, src)
}

func finishHarnessLink(plan harnessLink, src harnessLinkSource) (harnessLink, error) {
	target, err := filepath.Rel(plan.parent, src.path)
	if err != nil {
		return harnessLink{}, fmt.Errorf("relative link: %w", err)
	}
	plan.target = target
	plan.current, err = harnessLinkCurrent(plan.link, src)
	return plan, err
}

// checkSourceOverlap refuses a destination nested in or around a directory
// source, unless both name the same directory.
func checkSourceOverlap(actual string, src harnessLinkSource) error {
	if !src.info.IsDir() || src.path == filepath.Clean(actual) {
		return nil
	}
	if pathsOverlap(src.path, actual) {
		return fmt.Errorf("overlaps source directory %q", src.name)
	}
	return nil
}

// resolveHarnessLinkParent resolves the nearest existing ancestor of parent
// and joins the components that do not exist yet back on.
func resolveHarnessLinkParent(root, parent string) (string, error) {
	existing, missing, err := nearestExisting(root, parent)
	if err != nil {
		return "", err
	}
	resolved, err := resolveParentDir(root, existing)
	if err != nil {
		return "", err
	}
	return filepath.Join(append([]string{resolved}, missing...)...), nil
}

// nearestExisting returns the first ancestor of path that exists and the
// components below it, outermost first.
func nearestExisting(root, path string) (string, []string, error) {
	var missing []string
	for candidate := path; ; candidate = filepath.Dir(candidate) {
		_, err := os.Lstat(candidate)
		if err == nil {
			slices.Reverse(missing)
			return candidate, missing, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", nil, fmt.Errorf("inspect parent %q: %w", candidate, err)
		}
		if candidate == root {
			return "", nil, errors.New("workspace root is missing")
		}
		missing = append(missing, filepath.Base(candidate))
	}
}

func resolveParentDir(root, candidate string) (string, error) {
	resolved, err := canonicalPath(candidate)
	if err != nil {
		return "", fmt.Errorf("resolve parent %q: %w", candidate, err)
	}
	if err := confineToWorkspace(root, resolved); err != nil {
		return "", fmt.Errorf("parent: %w", err)
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("parent component %q is not a directory", candidate)
	}
	return resolved, nil
}

// harnessLinkCurrent reports whether link already holds src, and refuses
// any other entry there.
func harnessLinkCurrent(link string, src harnessLinkSource) (bool, error) {
	info, err := os.Lstat(link)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	kind := linkEntryKind(info)
	if kind == "symlink" {
		return symlinkCurrent(link, src.path)
	}
	if kind != linkEntryKind(src.info) {
		return false, errors.New("refuse existing destination")
	}
	return currentByKind[kind](link, src.path)
}

func linkEntryKind(info os.FileInfo) string {
	switch {
	case info.Mode()&os.ModeSymlink != 0:
		return "symlink"
	case info.IsDir():
		return "directory"
	case info.Mode().IsRegular():
		return "file"
	}
	return "other"
}

// currentByKind covers every kind a source can be; see linkableSource.
var currentByKind = map[string]func(link, source string) (bool, error){
	"directory": directoryCurrent,
	"file":      fileCurrent,
}

func symlinkCurrent(link, source string) (bool, error) {
	resolved, err := canonicalPath(link)
	if err != nil {
		return false, fmt.Errorf("refuse dangling or unreadable symlink: %w", err)
	}
	if resolved == filepath.Clean(source) {
		return true, nil
	}
	return false, errors.New("refuse conflicting symlink")
}

func directoryCurrent(link, source string) (bool, error) {
	resolved, err := canonicalPath(link)
	if err == nil && resolved == filepath.Clean(source) {
		return true, nil
	}
	return false, errors.New("refuse existing directory")
}

func fileCurrent(link, source string) (bool, error) {
	linked, err := os.ReadFile(link)
	if err != nil {
		return false, err
	}
	want, err := os.ReadFile(source)
	if err != nil {
		return false, err
	}
	if bytes.Equal(linked, want) {
		return true, nil
	}
	return false, errors.New("refuse conflicting file")
}

func checkDestinationOverlaps(plans []harnessLink) error {
	for i, plan := range plans {
		for _, other := range plans[i+1:] {
			if pathsOverlap(plan.actual, other.actual) {
				return fmt.Errorf("destinations %q and %q overlap", plan.destination, other.destination)
			}
		}
	}
	return nil
}

func checkWritesInsideSources(plans []harnessLink) error {
	for _, plan := range plans {
		if source, ok := sourceHolding(plan, plans); ok && !plan.current {
			return fmt.Errorf("destination %q would write inside source directory %q", plan.destination, source.source)
		}
	}
	return nil
}

func sourceHolding(plan harnessLink, plans []harnessLink) (harnessLink, bool) {
	for _, source := range plans {
		if source.sourceDir != "" && pathWithin(source.sourceDir, plan.actual) {
			return source, true
		}
	}
	return harnessLink{}, false
}

func pendingHarnessLinks(plans []harnessLink) []string {
	files := make([]string, 0, len(plans))
	for _, plan := range plans {
		if !plan.current {
			files = append(files, plan.destination)
		}
	}
	return files
}

func writeHarnessLinks(ctx context.Context, plans []harnessLink) error {
	for _, plan := range plans {
		if plan.current {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := writeHarnessLink(plan); err != nil {
			return err
		}
	}
	return nil
}

func writeHarnessLink(plan harnessLink) error {
	if err := os.MkdirAll(plan.parent, 0o755); err != nil {
		return fmt.Errorf("create parent for %q: %w", plan.destination, err)
	}
	if err := os.Symlink(plan.target, plan.link); err != nil {
		return fmt.Errorf("link %q: %w", plan.destination, err)
	}
	return nil
}

func canonicalPath(path string) (string, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	return filepath.Abs(resolved)
}

func confineToWorkspace(root, resolved string) error {
	if !pathWithin(root, resolved) {
		return errors.New("resolves outside the workspace")
	}
	if hasGitPathComponent(root, resolved) {
		return errors.New("resolves through a .git path component")
	}
	return nil
}

func pathsOverlap(a, b string) bool { return pathWithin(a, b) || pathWithin(b, a) }

func pathWithin(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	return err == nil && filepath.IsLocal(rel)
}

func hasGitPathComponent(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && slices.Contains(strings.Split(rel, string(filepath.Separator)), ".git")
}
