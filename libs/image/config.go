package image

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	digest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/egladman/magus/internal/json"
)

const historyCreatedBy = "magus image.assemble"

// buildConfig applies spec over base. A nil base is scratch: an empty config carrying
// only the spec's platform. diffID is the new layer's DiffID, empty when the assemble
// adds no layer; the history entry then records an empty layer so history and diff_ids
// stay aligned.
func buildConfig(base *ocispec.Image, spec Spec, diffID digest.Digest) (ocispec.Image, error) {
	var cfg ocispec.Image
	if base == nil {
		cfg.OS = spec.Platform.OS
		cfg.Architecture = spec.Platform.Architecture
		cfg.Variant = spec.Platform.Variant
		cfg.RootFS = ocispec.RootFS{Type: "layers", DiffIDs: []digest.Digest{}}
	} else {
		cfg = cloneConfig(*base)
	}
	created, ok, err := spec.created()
	if err != nil {
		return ocispec.Image{}, err
	}
	if ok {
		cfg.Created = &created
	}

	c := spec.Config
	cfg.Config.Env = mergeEnv(cfg.Config.Env, c.Env, c.UnsetEnv)
	setString(&cfg.Config.User, c.User)
	setString(&cfg.Config.WorkingDir, c.WorkingDir)
	setString(&cfg.Config.StopSignal, c.StopSignal)
	setList(&cfg.Config.Entrypoint, c.Entrypoint)
	setList(&cfg.Config.Cmd, c.Cmd)
	setSet(&cfg.Config.ExposedPorts, c.ExposedPorts)
	setSet(&cfg.Config.Volumes, c.Volumes)
	if len(c.Labels) > 0 {
		labels := maps.Clone(cfg.Config.Labels)
		if labels == nil {
			labels = make(map[string]string, len(c.Labels))
		}
		maps.Copy(labels, c.Labels)
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

func setString(dst *string, v *string) {
	if v != nil {
		*dst = *v
	}
}

// setList applies OCI's inherit-or-replace: nil keeps the base's list, empty clears it.
func setList(dst *[]string, v *[]string) {
	switch {
	case v == nil:
	case len(*v) == 0:
		*dst = nil
	default:
		*dst = slices.Clone(*v)
	}
}

func setSet(dst *map[string]struct{}, v *[]string) {
	switch {
	case v == nil:
	case len(*v) == 0:
		*dst = nil
	default:
		set := make(map[string]struct{}, len(*v))
		for _, k := range *v {
			set[k] = struct{}{}
		}
		*dst = set
	}
}

// cloneConfig copies what buildConfig appends to or edits so the base is never aliased.
func cloneConfig(c ocispec.Image) ocispec.Image {
	c.Config.Env = slices.Clone(c.Config.Env)
	c.Config.Entrypoint = slices.Clone(c.Config.Entrypoint)
	c.Config.Cmd = slices.Clone(c.Config.Cmd)
	c.Config.ExposedPorts = maps.Clone(c.Config.ExposedPorts)
	c.Config.Volumes = maps.Clone(c.Config.Volumes)
	c.RootFS.DiffIDs = slices.Clone(c.RootFS.DiffIDs)
	c.History = slices.Clone(c.History)
	return c
}

// mergeEnv replaces or appends set's keys (appends in key order), then drops every
// unset key. Base entries that are not K=V pass through untouched.
func mergeEnv(base []string, set map[string]string, unset []string) []string {
	out := slices.Clone(base)
	for _, k := range slices.Sorted(maps.Keys(set)) {
		entry := fmt.Sprintf("%s=%s", k, set[k])
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
		return nil
	}
	return out
}

// configJSON adapts the two image-spec bools that use v1 omitempty to the shared v2
// codec, keeping every other encoded member intact.
func configJSON(cfg ocispec.Image) ([]byte, error) {
	data, err := json.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, err
	}
	var config map[string]json.RawMessage
	if err := json.Unmarshal(fields["config"], &config); err != nil {
		return nil, err
	}
	var argsEscaped bool
	if err := json.Unmarshal(config["ArgsEscaped"], &argsEscaped); err != nil {
		return nil, err
	}
	if !argsEscaped {
		delete(config, "ArgsEscaped")
		fields["config"], err = json.Marshal(config)
		if err != nil {
			return nil, err
		}
	}
	if len(cfg.History) > 0 {
		var history []map[string]json.RawMessage
		if err := json.Unmarshal(fields["history"], &history); err != nil {
			return nil, err
		}
		for i, entry := range cfg.History {
			if !entry.EmptyLayer {
				delete(history[i], "empty_layer")
			}
		}
		fields["history"], err = json.Marshal(history)
		if err != nil {
			return nil, err
		}
	}
	return json.Marshal(fields)
}
