package image

import (
	"fmt"
	"maps"
	"reflect"
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
		cfg.Platform = *spec.Platform.oci()
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

// keepUnmodelled copies into fields and config each member of base that the image-spec
// types have no field for. A modelled member is never copied: buildConfig decided it,
// and an omitted one was cleared on purpose.
func keepUnmodelled(fields, config map[string]json.RawMessage, base []byte) error {
	var baseFields map[string]json.RawMessage
	if err := json.Unmarshal(base, &baseFields); err != nil {
		return err
	}
	for k, v := range baseFields {
		if !imageMembers[k] {
			fields[k] = v
		}
	}
	raw, ok := baseFields["config"]
	if !ok {
		return nil
	}
	var baseConfig map[string]json.RawMessage
	if err := json.Unmarshal(raw, &baseConfig); err != nil {
		return err
	}
	for k, v := range baseConfig {
		if !imageConfigMembers[k] {
			config[k] = v
		}
	}
	return nil
}

var (
	imageMembers       = jsonMembers(reflect.TypeFor[ocispec.Image]())
	imageConfigMembers = jsonMembers(reflect.TypeFor[ocispec.ImageConfig]())
)

// jsonMembers names the JSON members t encodes, an embedded struct's included, so the
// set follows the image-spec module rather than a list kept by hand.
func jsonMembers(t reflect.Type) map[string]bool {
	out := map[string]bool{}
	for i := range t.NumField() {
		f := t.Field(i)
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		switch {
		case f.Anonymous && name == "":
			maps.Copy(out, jsonMembers(f.Type))
		case name == "-":
		case name == "":
			out[f.Name] = true
		default:
			out[name] = true
		}
	}
	return out
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
// codec, keeping every other encoded member intact. Every member of base, the base
// config as written, that ocispec.Image does not model is carried over at the top level
// and in config: config.md says extra fields MUST NOT be an error, and a Healthcheck or
// Shell a base set must survive a rebuild.
func configJSON(cfg ocispec.Image, base []byte) ([]byte, error) {
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
	}
	if len(base) > 0 {
		if err := keepUnmodelled(fields, config, base); err != nil {
			return nil, fmt.Errorf("base config: %w", err)
		}
	}
	fields["config"], err = json.Marshal(config)
	if err != nil {
		return nil, err
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
