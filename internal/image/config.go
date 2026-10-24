package image

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	digest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/egladman/magus/internal/json"
)

const historyCreatedBy = "magus image write"

// buildConfig applies spec over base (R16). A nil base is scratch: an empty
// config carrying only the spec's platform. diffID is the new layer's DiffID,
// empty when the write adds no layer (R9); the history entry then records an
// empty layer so history and diff_ids stay aligned.
func buildConfig(base *ocispec.Image, spec Spec, diffID digest.Digest) (ocispec.Image, error) {
	var cfg ocispec.Image
	if base == nil {
		cfg.OS = spec.Platform.OS
		cfg.Architecture = spec.Platform.Architecture
		cfg.Variant = spec.Platform.Variant
		cfg.RootFS = ocispec.RootFS{Type: "layers"}
	} else {
		cfg = cloneConfig(*base)
	}

	if spec.Created != "" {
		t, err := time.Parse(time.RFC3339, spec.Created)
		if err != nil {
			return ocispec.Image{}, fmt.Errorf("image.write: created %q: want RFC 3339: %w", spec.Created, err)
		}
		cfg.Created = &t
	}

	env, err := mergeEnv(cfg.Config.Env, spec.Env, spec.UnsetEnv)
	if err != nil {
		return ocispec.Image{}, err
	}
	cfg.Config.Env = env
	if spec.User != "" {
		cfg.Config.User = spec.User
	}
	if spec.WorkingDir != "" {
		cfg.Config.WorkingDir = spec.WorkingDir
	}
	cfg.Config.Entrypoint = resolveList(cfg.Config.Entrypoint, spec.Entrypoint, spec.ResetEntrypoint)
	cfg.Config.Cmd = resolveList(cfg.Config.Cmd, spec.Cmd, spec.ResetCmd)
	if len(spec.Labels) > 0 {
		labels := maps.Clone(cfg.Config.Labels)
		if labels == nil {
			labels = make(map[string]string, len(spec.Labels))
		}
		maps.Copy(labels, spec.Labels)
		cfg.Config.Labels = labels
	}

	entry := ocispec.History{CreatedBy: historyCreatedBy}
	if diffID == "" {
		entry.EmptyLayer = true
	} else {
		cfg.RootFS.DiffIDs = append(cfg.RootFS.DiffIDs, diffID)
	}
	cfg.History = append(cfg.History, entry)
	return cfg, nil
}

// cloneConfig copies the slices buildConfig appends to or edits so the
// caller's base is never aliased.
func cloneConfig(c ocispec.Image) ocispec.Image {
	c.Config.Env = slices.Clone(c.Config.Env)
	c.Config.Entrypoint = slices.Clone(c.Config.Entrypoint)
	c.Config.Cmd = slices.Clone(c.Config.Cmd)
	c.RootFS.DiffIDs = slices.Clone(c.RootFS.DiffIDs)
	c.History = slices.Clone(c.History)
	return c
}

// mergeEnv replaces or appends set's keys (appends in key order), then drops
// every unset key. Base entries that are not K=V pass through untouched.
func mergeEnv(base []string, set map[string]string, unset []string) ([]string, error) {
	for k := range set {
		if err := validateEnvKey(k); err != nil {
			return nil, err
		}
	}
	for _, k := range unset {
		if err := validateEnvKey(k); err != nil {
			return nil, err
		}
	}
	out := slices.Clone(base)
	for _, k := range slices.Sorted(maps.Keys(set)) {
		entry := k + "=" + set[k]
		i := slices.IndexFunc(out, func(e string) bool { return strings.HasPrefix(e, k+"=") })
		if i >= 0 {
			out[i] = entry
		} else {
			out = append(out, entry)
		}
	}
	for _, k := range unset {
		out = slices.DeleteFunc(out, func(e string) bool { return strings.HasPrefix(e, k+"=") })
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// resolveList applies R18: reset clears the base, then a non-empty list replaces.
func resolveList(base, spec []string, reset bool) []string {
	if reset {
		base = nil
	}
	if len(spec) > 0 {
		return slices.Clone(spec)
	}
	return base
}

// configBlob marshals cfg (R19) and describes the bytes.
func configBlob(cfg ocispec.Image) ([]byte, ocispec.Descriptor, error) {
	data, err := json.Marshal(cfg)
	if err != nil {
		return nil, ocispec.Descriptor{}, fmt.Errorf("image.write: config: %w", err)
	}
	return data, blobDescriptor(ocispec.MediaTypeImageConfig, data), nil
}
