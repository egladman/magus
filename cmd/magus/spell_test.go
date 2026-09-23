package main

import (
	"io"
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
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/secret"
	remotespell "github.com/egladman/magus/internal/spell/remote"
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
