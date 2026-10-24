package image

import (
	"strings"
	"testing"
	"time"

	digest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func imageTestBase() *ocispec.Image {
	created := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	return &ocispec.Image{
		Created:  &created,
		Platform: ocispec.Platform{OS: "linux", Architecture: "amd64"},
		Config: ocispec.ImageConfig{
			User:       "1000",
			Env:        []string{"PATH=/bin", "A=1", "B=2"},
			Entrypoint: []string{"/entry"},
			Cmd:        []string{"serve"},
			WorkingDir: "/srv",
			Labels:     map[string]string{"keep": "x", "over": "old"},
		},
		RootFS:  ocispec.RootFS{Type: "layers", DiffIDs: []digest.Digest{digest.Digest("sha256:" + strings.Repeat("a", 64))}},
		History: []ocispec.History{{CreatedBy: "base step"}},
	}
}

var imageTestDiffID = digest.Digest("sha256:" + strings.Repeat("b", 64))

func TestImageConfigEnvMerge(t *testing.T) {
	tests := []struct {
		name  string
		base  []string
		set   map[string]string
		unset []string
		want  []string
	}{
		{"replace in place", []string{"A=1", "B=2"}, map[string]string{"A": "9"}, nil, []string{"A=9", "B=2"}},
		{"append sorted", []string{"A=1"}, map[string]string{"Z": "z", "M": "m"}, nil, []string{"A=1", "M=m", "Z=z"}},
		{"unset after set", []string{"A=1"}, map[string]string{"B": "2"}, []string{"B", "A"}, nil},
		{"unset absent key", []string{"A=1"}, nil, []string{"NOPE"}, []string{"A=1"}},
		{"prefix is not a match", []string{"AB=1"}, map[string]string{"A": "2"}, []string{"A"}, []string{"AB=1"}},
		{"empty value kept", nil, map[string]string{"E": ""}, nil, []string{"E="}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := mergeEnv(tt.base, tt.set, tt.unset)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestImageConfigEnvKeyErrors(t *testing.T) {
	for _, key := range []string{"", "A=B"} {
		_, err := mergeEnv(nil, map[string]string{key: "v"}, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "env key")
		_, err = mergeEnv(nil, nil, []string{key})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "env key")
	}
}

func TestImageConfigListReset(t *testing.T) {
	tests := []struct {
		name  string
		base  []string
		spec  []string
		reset bool
		want  []string
	}{
		{"empty inherits", []string{"a"}, nil, false, []string{"a"}},
		{"non-empty replaces", []string{"a"}, []string{"b", "c"}, false, []string{"b", "c"}},
		{"reset clears", []string{"a"}, nil, true, nil},
		{"reset then list", []string{"a"}, []string{"b"}, true, []string{"b"}},
		{"scratch inherits nothing", nil, nil, false, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, resolveList(tt.base, tt.spec, tt.reset))
		})
	}
}

func TestImageConfigEmptySpecKeepsBase(t *testing.T) {
	base := imageTestBase()
	got, err := buildConfig(base, Spec{}, "")
	require.NoError(t, err)

	assert.Equal(t, base.Created, got.Created)
	assert.Equal(t, base.Platform, got.Platform)
	assert.Equal(t, base.Config, got.Config)
	assert.Equal(t, base.RootFS, got.RootFS)
	assert.Equal(t, base.History, got.History[:1])
}

func TestImageConfigInheritanceWithFilesOnly(t *testing.T) {
	base := imageTestBase()
	spec := Spec{Files: []File{{Destination: "/x", Mode: 0o644}}}
	got, err := buildConfig(base, spec, imageTestDiffID)
	require.NoError(t, err)
	assert.Equal(t, base.Config, got.Config)
}

func TestImageConfigAppliesSpec(t *testing.T) {
	spec := Spec{
		Env:        map[string]string{"A": "9", "N": "new"},
		UnsetEnv:   []string{"B"},
		User:       "65532:65532",
		WorkingDir: "/app",
		Entrypoint: []string{"/magus"},
		ResetCmd:   true,
		Labels:     map[string]string{"over": "new", "add": "y"},
	}
	got, err := buildConfig(imageTestBase(), spec, imageTestDiffID)
	require.NoError(t, err)

	assert.Equal(t, ocispec.ImageConfig{
		User:       "65532:65532",
		Env:        []string{"PATH=/bin", "A=9", "N=new"},
		Entrypoint: []string{"/magus"},
		WorkingDir: "/app",
		Labels:     map[string]string{"keep": "x", "over": "new", "add": "y"},
	}, got.Config)
}

func TestImageConfigDoesNotAliasBase(t *testing.T) {
	base := imageTestBase()
	_, err := buildConfig(base, Spec{Env: map[string]string{"A": "9"}, Labels: map[string]string{"keep": "z"}}, imageTestDiffID)
	require.NoError(t, err)
	assert.Equal(t, imageTestBase(), base)
}

func TestImageConfigLayerAndHistory(t *testing.T) {
	base := imageTestBase()

	withLayer, err := buildConfig(base, Spec{}, imageTestDiffID)
	require.NoError(t, err)
	assert.Equal(t, []digest.Digest{base.RootFS.DiffIDs[0], imageTestDiffID}, withLayer.RootFS.DiffIDs)
	require.Len(t, withLayer.History, 2)
	assert.Equal(t, ocispec.History{CreatedBy: "magus image write"}, withLayer.History[1])

	noLayer, err := buildConfig(base, Spec{}, "")
	require.NoError(t, err)
	assert.Equal(t, base.RootFS.DiffIDs, noLayer.RootFS.DiffIDs)
	assert.Equal(t, ocispec.History{CreatedBy: "magus image write", EmptyLayer: true}, noLayer.History[1])
}

func TestImageConfigCreated(t *testing.T) {
	base := imageTestBase()

	kept, err := buildConfig(base, Spec{}, "")
	require.NoError(t, err)
	assert.Equal(t, base.Created, kept.Created)

	set, err := buildConfig(base, Spec{Created: "2026-10-23T12:00:00Z"}, "")
	require.NoError(t, err)
	assert.Equal(t, time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC), *set.Created)

	scratch, err := buildConfig(nil, Spec{}, "")
	require.NoError(t, err)
	assert.Nil(t, scratch.Created)

	_, err = buildConfig(base, Spec{Created: "yesterday"}, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "created")
}

func TestImageConfigHistoryCarriesNoCreated(t *testing.T) {
	got, err := buildConfig(imageTestBase(), Spec{Created: "2026-10-23T12:00:00Z"}, imageTestDiffID)
	require.NoError(t, err)
	assert.Nil(t, got.History[len(got.History)-1].Created)
}

func TestImageConfigScratch(t *testing.T) {
	spec := Spec{Platform: Platform{OS: "linux", Architecture: "arm64", Variant: "v8"}, Cmd: []string{"ls"}}
	got, err := buildConfig(nil, spec, imageTestDiffID)
	require.NoError(t, err)

	assert.Equal(t, ocispec.Image{
		Platform: ocispec.Platform{OS: "linux", Architecture: "arm64", Variant: "v8"},
		RootFS:   ocispec.RootFS{Type: "layers", DiffIDs: []digest.Digest{imageTestDiffID}},
		Config:   ocispec.ImageConfig{Cmd: []string{"ls"}},
		History:  []ocispec.History{{CreatedBy: "magus image write"}},
	}, got)
}

func TestImageConfigBlobBytes(t *testing.T) {
	cfg, err := buildConfig(nil, Spec{Platform: Platform{OS: "linux", Architecture: "amd64"}}, imageTestDiffID)
	require.NoError(t, err)
	data, desc, err := configBlob(cfg)
	require.NoError(t, err)

	assert.Equal(t, `{"architecture":"amd64","os":"linux","config":{"ArgsEscaped":false},"rootfs":{"type":"layers","diff_ids":["`+string(imageTestDiffID)+`"]},"history":[{"created_by":"magus image write","empty_layer":false}]}`, string(data))
	assert.Equal(t, ocispec.Descriptor{
		MediaType: "application/vnd.oci.image.config.v1+json",
		Digest:    digest.FromBytes(data),
		Size:      int64(len(data)),
	}, desc)
}
