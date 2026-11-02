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

func planHarnessAssets(root string, d HarnessDescriptor, plan *types.HarnessInstallation) error {
	if len(d.Assets) == 0 {
		return nil
	}
	receipt := filepath.Join(".magus", "harnesses", d.ID, "assets.jsonl")
	receiptPath, err := assetPath(root, receipt)
	if err != nil {
		return err
	}
	known := map[string]string{}
	lines, err := file.ReadLines(receiptPath)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	for _, line := range lines.Lines {
		var entry harnessAssetReceipt
		if err := json.Unmarshal(line.Text, &entry); err != nil {
			return fmt.Errorf("read harness asset ownership: %w", err)
		}
		known[entry.Path] = entry.Digest
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
			return fmt.Errorf("harness asset %s has unrecognized local changes; preserve or move it before installing", a.Path)
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
	bodies := map[string][]byte{}
	for _, relative := range paths {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		path, err := assetPath(root, relative)
		if err != nil {
			return nil, err
		}
		change := plan.Files[relative]
		body := change.Bytes
		if body == nil {
			body = []byte(change.Content)
		}
		if change.Bytes == nil && change.Content == "" {
			doc := map[string]any{}
			existing, err := os.ReadFile(path)
			if err != nil && !errors.Is(err, fs.ErrNotExist) {
				return nil, err
			}
			if len(existing) > 0 {
				if err := decodeHarnessJSON(existing, &doc); err != nil {
					return nil, err
				}
			}
			mergeHarnessFragment(doc, change.Fragment)
			body, err = json.MarshalIndent(doc, "", "  ")
			if err != nil {
				return nil, err
			}
			body = append(body, '\n')
		}
		bodies[relative] = body
	}
	if dryRun {
		return paths, nil
	}
	// Ownership receipts are written last, so a failed install cannot claim bytes not written.
	slices.SortStableFunc(paths, func(a, b string) int {
		aReceipt := strings.HasSuffix(a, "assets.jsonl")
		bReceipt := strings.HasSuffix(b, "assets.jsonl")
		if aReceipt == bReceipt {
			return strings.Compare(a, b)
		}
		if aReceipt {
			return 1
		}
		return -1
	})
	for _, relative := range paths {
		path, err := assetPath(root, relative)
		if err != nil {
			return nil, err
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, err
		}
		if err := file.WriteFileAtomic(path, bodies[relative], 0o644); err != nil {
			return nil, err
		}
	}
	return paths, nil
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
