package main

import (
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/interp/bindings"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/oci"
	"github.com/egladman/magus/internal/secret"
	"github.com/egladman/magus/internal/spell"
	remotespell "github.com/egladman/magus/internal/spell/remote"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
)

// spellRegistry is enough of the distribution API for every spell verb: a token
// endpoint, the two-step blob upload, manifests by tag or digest, and tags/list.
type spellRegistry struct {
	srv       *httptest.Server
	mu        sync.Mutex
	blobs     map[string][]byte
	manifests map[string][]byte
	uploads   int
}

func newSpellRegistry(t *testing.T) *spellRegistry {
	t.Helper()
	r := &spellRegistry{blobs: map[string][]byte{}, manifests: map[string][]byte{}}
	r.srv = httptest.NewTLSServer(http.HandlerFunc(r.serve))
	t.Cleanup(r.srv.Close)
	prev := registryHTTP
	registryHTTP = r.srv.Client
	t.Cleanup(func() { registryHTTP = prev })
	return r
}

func (r *spellRegistry) host() string { return strings.TrimPrefix(r.srv.URL, "https://") }

func (r *spellRegistry) serve(w http.ResponseWriter, req *http.Request) {
	r.mu.Lock()
	defer r.mu.Unlock()
	p := req.URL.Path
	last := p[strings.LastIndex(p, "/")+1:]
	switch {
	case p == "/v2/":
		w.Header().Set("WWW-Authenticate", `Bearer realm="https://`+req.Host+`/token",service="`+req.Host+`"`)
		w.WriteHeader(http.StatusUnauthorized)
	case p == "/token":
		_, _ = w.Write([]byte(`{"token":"fake"}`))
	case strings.HasPrefix(p, "/upload/"):
		body, _ := io.ReadAll(req.Body)
		r.blobs[req.URL.Query().Get("digest")] = body
		w.WriteHeader(http.StatusCreated)
	case strings.HasSuffix(p, "/blobs/uploads/"):
		r.uploads++
		w.Header().Set("Location", "/upload/1")
		w.WriteHeader(http.StatusAccepted)
	case strings.HasSuffix(p, "/tags/list"):
		var tags []string
		for name := range r.manifests {
			if !strings.Contains(name, ":") {
				tags = append(tags, name)
			}
		}
		slices.Sort(tags)
		body, _ := json.Marshal(map[string]any{"tags": tags})
		_, _ = w.Write(body)
	case strings.Contains(p, "/blobs/"):
		b, ok := r.blobs[last]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if req.Method != http.MethodHead {
			_, _ = w.Write(b)
		}
	case strings.Contains(p, "/manifests/"):
		if req.Method == http.MethodPut {
			body, _ := io.ReadAll(req.Body)
			r.manifests[last] = body
			r.manifests[digest.FromBytes(body).String()] = body
			w.WriteHeader(http.StatusCreated)
			return
		}
		b, ok := r.manifests[last]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write(b)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

// spellRepo commits a spell at spells/x in a fresh git repository with a fixed commit
// time and an ssh remote, returning the repository and the spell directory.
func spellRepo(t *testing.T) (repo, dir string) {
	t.Helper()
	repo = initGitRepo(t)
	dir = filepath.Join(repo, "spells", "x")
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "lib"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "spell.buzz"), []byte("export fun mgs_getName() > str { return \"x\"; }\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "lib", "util.buzz"), []byte("fun util() > void {}\n"), 0o644))
	t.Setenv("GIT_COMMITTER_DATE", "2026-09-01T12:30:00-04:00")
	t.Setenv("GIT_AUTHOR_DATE", "2026-09-01T12:30:00-04:00")
	runGit(t, repo, "remote", "add", "origin", "git@github.com:owner/repo.git")
	runGit(t, repo, "add", ".")
	runGit(t, repo, "-c", "commit.gpgsign=false", "commit", "-m", "add spell")
	// Untracked, so it must not reach the layer.
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".DS_Store"), []byte("finder"), 0o644))
	return repo, dir
}

func gitHead(t *testing.T, repo string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", repo, "rev-parse", "HEAD").Output()
	require.NoError(t, err)
	return strings.TrimSpace(string(out))
}

// runSpell runs one `magus spell` invocation from a cold start and returns its stdout.
func runSpell(t *testing.T, root string, args ...string) string {
	t.Helper()
	resetStartupSingletons()
	t.Cleanup(resetStartupSingletons)
	var err error
	out := captureStdout(t, func() { err = spellCmd(t.Context(), root, args) })
	require.NoError(t, err, "magus spell %v", args)
	return out
}

// The publish walkthrough end to end: build prints the digest push then writes, push
// writes every tag from one upload, ls lists them, and pull by tag verifies and lands
// the same files.
func TestSpellBuildPushLsPull(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	reg := newSpellRegistry(t)
	repo, dir := spellRepo(t)
	layerPath := filepath.Join(t.TempDir(), "spell.tar")

	built := strings.TrimSpace(runSpell(t, repo, "build", dir, "--out", layerPath))
	require.NoError(t, digest.Digest(built).Validate(), "build prints a manifest digest: %q", built)

	var res spellBuildResult
	require.NoError(t, json.Unmarshal([]byte(runSpell(t, repo, "build", dir, "-o", "json")), &res))
	layer, err := os.ReadFile(layerPath)
	require.NoError(t, err)
	assert.Equal(t, spellBuildResult{
		Digest: digest.Digest(built),
		Layer:  digest.FromBytes(layer),
		Size:   int64(len(layer)),
		Annotations: map[string]string{
			ocispec.AnnotationTitle:    "x",
			ocispec.AnnotationSource:   "https://github.com/owner/repo",
			ocispec.AnnotationRevision: gitHead(t, repo),
			ocispec.AnnotationCreated:  "2026-09-01T16:30:00Z",
		},
	}, res)

	repoRef := reg.host() + "/team/spells/x"
	pushed := runSpell(t, repo, "push", dir, repoRef+":v1", "--tag", "latest")
	assert.Equal(t, repoRef+"@"+built+"\n", pushed, "push writes the digest build printed")
	reg.mu.Lock()
	assert.Equal(t, 2, reg.uploads, "the config blob and the layer, once each for both tags")
	reg.mu.Unlock()

	assert.Equal(t, "latest\nv1\n", runSpell(t, repo, "ls", repoRef))

	pulled := strings.Split(strings.TrimSpace(runSpell(t, repo, "pull", repoRef+":latest")), "\n")
	require.Len(t, pulled, 2)
	assert.Equal(t, repoRef+"@"+built, pulled[0])
	assert.FileExists(t, filepath.Join(pulled[1], "spell.buzz"))

	dst := filepath.Join(t.TempDir(), "vendor", "x")
	runSpell(t, repo, "pull", repoRef+"@"+built, dst)
	var got []string
	require.NoError(t, filepath.WalkDir(dst, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			rel, _ := filepath.Rel(dst, p)
			got = append(got, filepath.ToSlash(rel))
		}
		return err
	}))
	assert.Equal(t, []string{"lib/util.buzz", "spell.buzz"}, got, "only tracked files were published")
}

// A registry credential is a secret reference resolved through the workspace's
// provider, so the value is registered for redaction like any magus\secret.read.
func TestRegistryCredentialResolvesThroughTheSecretProvider(t *testing.T) {
	spells := config.SpellsConfig{Registries: []config.SpellRegistry{
		{Host: "ghcr.io", Username: "ci", Password: "SPELL_REGISTRY_TOKEN"},
	}}
	r := secret.New()
	t.Setenv("SPELL_REGISTRY_TOKEN", "ghp_abcdef123456")

	user, pass, err := registryCredential(t.Context(), spells, "ghcr.io", r)
	require.NoError(t, err)
	assert.Equal(t, "ci", user)
	assert.Equal(t, "ghp_abcdef123456", pass.Reveal())
	assert.Equal(t, "token=***", r.RedactString("token=ghp_abcdef123456"))

	_, _, err = registryCredential(t.Context(), spells, "docker.io", r)
	require.ErrorContains(t, err, "no entry for docker.io")

	unset := config.SpellsConfig{Registries: []config.SpellRegistry{{Host: "ghcr.io", Username: "ci", Password: "SPELL_REGISTRY_UNSET"}}}
	_, _, err = registryCredential(t.Context(), unset, "ghcr.io", secret.New())
	require.ErrorContains(t, err, "$SPELL_REGISTRY_UNSET is not set")
}

// The lock walkthrough: a declared spell with no pin fails the frozen check, --update
// resolves the tag and writes magus.lock, the check then passes, and a pull by the bare
// import path lands the locked bytes.
func TestSpellLockThenPullByImportPath(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	reg := newSpellRegistry(t)
	repo, dir := spellRepo(t)
	path := reg.host() + "/team/spells/x"
	pinned := strings.TrimSpace(runSpell(t, repo, "push", dir, path+":v1"))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "magus.yaml"),
		[]byte("spells:\n  \""+path+"\":\n    tag: v1\n"), 0o644))

	resetStartupSingletons()
	err := spellCmd(t.Context(), repo, []string{"lock"})
	require.ErrorIs(t, err, types.RemoteSpellLockStale, "nothing is pinned yet, and the check resolves no tag")

	assert.Equal(t, pinned+"\n", runSpell(t, repo, "lock", "--update"))
	lock, err := remotespell.ReadLock(repo)
	require.NoError(t, err)
	d := digest.Digest(strings.TrimPrefix(pinned, path+"@"))
	assert.Equal(t, remotespell.Lock{Version: 1, Spells: map[string]remotespell.LockEntry{path: {Tag: "v1", Digest: d}}}, lock)

	var res spellLockResult
	require.NoError(t, json.Unmarshal([]byte(runSpell(t, repo, "lock", "-o", "json")), &res))
	assert.Equal(t, spellLockResult{Spells: []spellLockEntry{{Path: path, Tag: "v1", Digest: d}}}, res)

	pulled := strings.Split(strings.TrimSpace(runSpell(t, repo, "pull", path)), "\n")
	require.Len(t, pulled, 2)
	assert.Equal(t, pinned, pulled[0], "a bare import path pulls the locked digest")
	assert.FileExists(t, filepath.Join(pulled[1], "spell.buzz"))
}

// Pulling a spell magus ships prints what a registry pull of its published artifact
// prints, and that artifact is what `spell build` makes of a checkout of the same
// directory: the same layer bytes, and the same manifest once the provenance is the
// shipped one, which carries no revision or creation time.
func TestSpellPullShippedMatchesBuild(t *testing.T) {
	repo := initGitRepo(t)
	require.NoError(t, os.WriteFile(filepath.Join(repo, "magus.yaml"), nil, 0o644))
	dir := filepath.Join(repo, "spells", "golang")
	require.NoError(t, os.CopyFS(dir, os.DirFS(filepath.Join("..", "..", "spells", "golang"))))
	runGit(t, repo, "remote", "add", "origin", "https://github.com/egladman/magus.git")
	runGit(t, repo, "add", ".")
	runGit(t, repo, "-c", "commit.gpgsign=false", "commit", "-m", "the go spell")

	dst := filepath.Join(repo, "spells", "go")
	pulled := strings.Split(strings.TrimSpace(runSpell(t, repo, "pull", "magus/spell/go", dst)), "\n")
	require.Len(t, pulled, 2)
	ref, err := oci.ParseReference(pulled[0])
	require.NoError(t, err)
	assert.Equal(t, "ghcr.io/egladman/magus/spells/go", ref.Registry+"/"+ref.Repository)
	assert.Equal(t, dst, pulled[1])

	driver, err := spellVCS(t.Context(), repo, dir)
	require.NoError(t, err)
	built, err := remotespell.Build(t.Context(), dir, driver, remotespell.ShippedProvenance("golang"))
	require.NoError(t, err)
	_, d, err := built.Manifest()
	require.NoError(t, err)
	assert.Equal(t, d, ref.Digest, "the stamped digest is the manifest a build of the checkout makes")

	var res spellBuildResult
	require.NoError(t, json.Unmarshal([]byte(runSpell(t, repo, "build", dir, "-o", "json")), &res))
	assert.Equal(t, digest.FromBytes(built.Layers[0].Payload), res.Layer, "`spell build` packs the same layer")

	spellBuzz, err := os.ReadFile(filepath.Join(dst, "spell.buzz"))
	require.NoError(t, err)
	stamp, _, _ := strings.Cut(string(spellBuzz), "\n")
	assert.Equal(t, "// magus:origin "+pulled[0], stamp)
	require.NoError(t, bindings.CheckSpellFile(t.Context(), filepath.Join(dst, "spell.buzz")), "the copy loads as a workspace spell")

	var pullRes spellPullResult
	require.NoError(t, json.Unmarshal([]byte(runSpell(t, repo, "pull", "magus/spell/go", t.TempDir(), "-o", "json")), &pullRes))
	assert.Equal(t, spellOverride{Import: "magus/spell/go", Path: filepath.ToSlash(pullRes.Dir)}, pullRes.Override,
		"a copy outside the workspace is declared by its absolute path")
	assert.Equal(t, ref.Digest, pullRes.Digest)
}

// A spell that ships as source only has no built-in to replace, so pull offers no
// override, and the copy loads with the host modules it imports.
func TestSpellPullSourceOnlySpell(t *testing.T) {
	dst := filepath.Join(t.TempDir(), "endoflife-date")
	var res spellPullResult
	require.NoError(t, json.Unmarshal([]byte(runSpell(t, t.TempDir(), "pull", "magus/spell/endoflife-date", dst, "-o", "json")), &res))
	assert.Equal(t, "ghcr.io/egladman/magus/spells/endoflife-date@"+res.Digest.String(), res.Reference)
	assert.Zero(t, res.Override)
	require.NoError(t, bindings.CheckSpellFile(t.Context(), filepath.Join(dst, "spell.buzz")))
}

func TestSpellPullShippedRefuses(t *testing.T) {
	repo := t.TempDir()
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"pull", "magus/spell/go"}, "want the <dir>"},
		{[]string{"pull", "magus/spell/nope", t.TempDir()}, `magus ships no spell "nope"`},
		{[]string{"pull", "magus/spell/golang", t.TempDir()}, `magus ships no spell "golang"`},
	} {
		resetStartupSingletons()
		err := spellCmd(t.Context(), repo, c.args)
		require.ErrorContains(t, err, c.want, "%v", c.args)
	}
}

// A push of magus/spell/<name> writes the artifact the binary ships, byte for byte, so
// the registry holds the digest `spell pull magus/spell/<name>` stamps on a copy, for a
// built-in and a nested provider alike.
func TestSpellPushShipped(t *testing.T) {
	reg := newSpellRegistry(t)
	repo := t.TempDir()
	for name, dir := range map[string]string{"go": "golang", "harness/cursor": "harness/cursor"} {
		want, content, err := remotespell.Shipped(name, dir)
		require.NoError(t, err)
		manifest, _, err := content.Manifest()
		require.NoError(t, err)

		dest := reg.host() + "/team/spells/" + name
		pushed := runSpell(t, repo, "push", "magus/spell/"+name, dest+":v1.2.3")
		assert.Equal(t, dest+"@"+want.Digest.String()+"\n", pushed, name)
		reg.mu.Lock()
		assert.Equal(t, manifest, reg.manifests["v1.2.3"], name)
		reg.mu.Unlock()
	}

	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"push", "magus/spell/golang", reg.host() + "/team/spells/go:v1"}, `magus ships no spell "golang"`},
		{[]string{"push", "magus/spell/harness", reg.host() + "/team/spells/harness:v1"}, `magus ships no spell "harness"`},
		{[]string{"push", "magus/spell/go", reg.host() + "/team/spells/go:v1", "--source", "https://example.com/fork"}, "--source does not apply"},
	} {
		resetStartupSingletons()
		require.ErrorContains(t, spellCmd(t.Context(), repo, c.args), c.want, "%v", c.args)
	}
}

// `spell ls magus/spell` is the list spell-publish reads: every directory of the
// embedded spells holding a spell.buzz, under the name magus/spell/<name> takes, pinned
// to the digest a push of it writes.
func TestSpellLsShipped(t *testing.T) {
	var dirs []string
	require.NoError(t, fs.WalkDir(spells.Shipped(), ".", func(p string, _ fs.DirEntry, err error) error {
		if dir, ok := strings.CutSuffix(p, "/spell.buzz"); ok && err == nil {
			dirs = append(dirs, dir)
		}
		return err
	}))

	var res shippedSpellsResult
	require.NoError(t, json.Unmarshal([]byte(runSpell(t, t.TempDir(), "ls", "magus/spell", "-o", "json")), &res))
	var listed, names, refs []string
	for _, s := range res.Spells {
		listed = append(listed, s.Dir)
		names = append(names, s.Name)
		refs = append(refs, s.Reference)
		_, builtIn := spell.Builtins()[s.Name]
		assert.Equal(t, builtIn, s.BuiltIn, s.Name)
		if !builtIn {
			assert.Equal(t, s.Dir, s.Name, "a source-only spell is named by its directory")
		}
		ref, _, err := remotespell.Shipped(s.Name, s.Dir)
		require.NoError(t, err)
		assert.Equal(t, ref.String(), s.Reference, s.Name)
		assert.Equal(t, ref.Digest, s.Digest, s.Name)
	}
	assert.ElementsMatch(t, dirs, listed, "every shipped spell, once")
	assert.True(t, slices.IsSorted(names), "sorted by name: %v", names)
	assert.Subset(t, names, []string{"go", "endoflife-date", "harness/claude-code", "harness/codex", "harness/cursor",
		"harness/opencode", "aws/s3-cache", "github/actions", "github/review", "gitlab/ci"})
	assert.NotContains(t, names, "golang")

	assert.Equal(t, strings.Join(refs, "\n")+"\n", runSpell(t, t.TempDir(), "ls", "magus/spell"))
}
