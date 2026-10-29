package container

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/libs/image"
	"github.com/egladman/magus/libs/oci"
)

type containerSuite struct {
	suite.Suite
	engine *fakeEngine
	srv    *httptest.Server
	client *engineClient
}

func TestContainerSuite(t *testing.T) { suite.Run(t, new(containerSuite)) }

func (s *containerSuite) SetupTest() {
	s.engine = &fakeEngine{files: map[string][]byte{}, arch: "aarch64"}
	s.srv = httptest.NewServer(s.engine.mux())
	s.client = &engineClient{http: s.srv.Client(), base: s.srv.URL}
}

func (s *containerSuite) TearDownTest() { s.srv.Close() }

var (
	amd64 = image.Platform{OS: "linux", Architecture: "amd64"}
	arm64 = image.Platform{OS: "linux", Architecture: "arm64"}
)

func (s *containerSuite) container() *instance {
	img := pinnedImage{Ref: oci.Reference{Registry: "docker.io", Repository: "swebench/x", Tag: "latest"}, Digest: digest.FromString("m"), Platform: arm64}
	c, err := createInstance(context.Background(), s.client, "trial", img, limits{CPUs: 2, MemoryBytes: 4 << 30})
	s.Require().NoError(err)
	return c
}

func (s *containerSuite) TestPlatformMapsTheKernelArchitectureToGoarch() {
	p, err := s.client.Platform(context.Background())
	s.Require().NoError(err)
	s.Equal(arm64, p)
}

func (s *containerSuite) TestPullRefusesAReferenceWithoutADigest() {
	err := s.client.Pull(context.Background(), "docker.io/swebench/x:latest", amd64)
	s.ErrorContains(err, "name a digest")
}

func (s *containerSuite) TestPullFailsOnAnErrorInsideTheProgressStream() {
	s.engine.pullError = "no matching manifest for linux/arm64"
	err := s.client.Pull(context.Background(), "docker.io/swebench/x@"+digest.FromString("m").String(), arm64)
	s.ErrorContains(err, "no matching manifest for linux/arm64")
}

func (s *containerSuite) TestPullAsksForTheDigestAndPlatform() {
	ref := "docker.io/swebench/x@" + digest.FromString("m").String()
	s.Require().NoError(s.client.Pull(context.Background(), ref, amd64))
	s.Equal(ref, s.engine.pulled.Get("fromImage"))
	s.Equal("linux/amd64", s.engine.pulled.Get("platform"))
}

func (s *containerSuite) TestCreateRefusesAMissingLimit() {
	_, err := createInstance(context.Background(), s.client, "trial", pinnedImage{Platform: arm64}, limits{CPUs: 2})
	s.ErrorContains(err, "set both a CPU and a memory limit")
	s.Empty(s.engine.created.Image, "nothing reaches the engine")
}

func (s *containerSuite) TestCreatePinsTheDigestAndSetsTheLimits() {
	c := s.container()
	s.Equal(createRequest{
		Image:      c.Image.Pinned(),
		Cmd:        []string{"sleep", "infinity"},
		WorkingDir: testbedDir,
		HostConfig: hostConfig{NanoCPUs: 2e9, Memory: 4 << 30},
	}, s.engine.created)
	s.Equal("linux/arm64", s.engine.createdPlatform)
	s.True(s.engine.started)
}

func (s *containerSuite) TestEngineErrorsCarryTheEngineMessage() {
	s.engine.createError = "Conflict. The container name \"/trial\" is already in use"
	_, err := createInstance(context.Background(), s.client, "trial", pinnedImage{Platform: arm64}, limits{CPUs: 1, MemoryBytes: 1})
	s.ErrorContains(err, "already in use")
}

func (s *containerSuite) TestCopyMagusRefusesABinaryForAnotherMachine() {
	c := s.container()
	err := copyMagus(context.Background(), c, writeELF(s.T(), uint16(0x3e)))
	s.ErrorContains(err, "linux/arm64 build of magus")
	s.Empty(s.engine.files)
}

func (s *containerSuite) TestCopyMagusRefusesAHostBinaryThatIsNotELF() {
	bin := filepath.Join(s.T().TempDir(), "magus")
	s.Require().NoError(os.WriteFile(bin, []byte("\xcf\xfa\xed\xfe mach-o"), 0o755))
	err := copyMagus(context.Background(), s.container(), bin)
	s.ErrorContains(err, "is not an ELF binary")
}

func (s *containerSuite) TestCopyMagusPlacesAnExecutableOnThePath() {
	bin := writeELF(s.T(), uint16(0xb7))
	s.Require().NoError(copyMagus(context.Background(), s.container(), bin))
	want, err := os.ReadFile(bin)
	s.Require().NoError(err)
	s.Equal(want, s.engine.files[magusPath])
	s.Equal(int64(0o755), s.engine.modes[magusPath])
}

func (s *containerSuite) TestApplyPatchWithAnEmptyPatchRunsNoRung() {
	rung, err := applyPatch(context.Background(), s.container(), nil, io.Discard)
	s.Require().NoError(err)
	s.Empty(rung)
	s.Equal([]string{"git config --global --add safe.directory /testbed"}, s.engine.commands())
}

func (s *containerSuite) TestApplyPatchResetsTheTreeBeforeEachRetry() {
	s.engine.exit = func(argv string) int {
		if strings.HasPrefix(argv, "git apply --verbose --reject") || !strings.HasPrefix(argv, "git apply") {
			return 0
		}
		return 1
	}
	var log bytes.Buffer
	rung, err := applyPatch(context.Background(), s.container(), []byte("diff"), &log)
	s.Require().NoError(err)
	s.Equal("git apply --verbose --reject", rung)
	s.Equal([]string{
		"git config --global --add safe.directory /testbed",
		"git apply --verbose " + patchPath,
		"git checkout -- .", "git clean -fd",
		"git apply --verbose --3way " + patchPath,
		"git checkout -- .", "git clean -fd",
		"git apply --verbose --reject " + patchPath,
	}, s.engine.commands())
	s.Contains(log.String(), ">>>>> Applied Patch\n")
	s.Equal([]byte("diff"), s.engine.files[patchPath])
}

func (s *containerSuite) TestApplyPatchReportsAnExhaustedLadder() {
	s.engine.exit = func(argv string) int {
		if strings.HasPrefix(argv, "git apply") || strings.HasPrefix(argv, "patch") {
			return 1
		}
		return 0
	}
	var log bytes.Buffer
	_, err := applyPatch(context.Background(), s.container(), []byte("diff"), &log)
	s.ErrorIs(err, errPatchFailed)
	s.Contains(log.String(), ">>>>> Patch Apply Failed\n")
	s.Contains(s.engine.commands(), "patch --batch --forward --fuzz=5 -p1 -i "+patchPath)
}

func (s *containerSuite) TestRunEvalReturnsTheScriptExitCodeAndOutput() {
	s.engine.exit = func(string) int { return 1 }
	s.engine.stdout = "--- FAIL: TestX\n"
	var log bytes.Buffer
	got, err := runEval(context.Background(), s.container(), []byte("go test ./..."), time.Minute, &log)
	s.Require().NoError(err)
	s.Equal(evalResult{ExitCode: 1}, got)
	s.Equal("--- FAIL: TestX\n", log.String())
	s.Equal([]byte("go test ./..."), s.engine.files[evalPath])
}

func (s *containerSuite) TestRunEvalKillsTheContainerWhenTheTimeoutPasses() {
	s.engine.hang = true
	var log bytes.Buffer
	got, err := runEval(context.Background(), s.container(), []byte("sleep 1d"), 50*time.Millisecond, &log)
	s.Require().NoError(err)
	s.Equal(evalResult{TimedOut: true}, got)
	s.Contains(log.String(), ">>>>> Tests Timed Out")
	s.True(s.engine.killed)
}

func (s *containerSuite) TestRunEvalRefusesAMissingTimeout() {
	_, err := runEval(context.Background(), s.container(), nil, 0, io.Discard)
	s.ErrorContains(err, "set a timeout")
}

func (s *containerSuite) TestDiffKeepsStderrOutOfThePatch() {
	s.engine.stdout = "diff --git a/x b/x\n"
	s.engine.stderr = "warning: LF will be replaced\n"
	got, err := collectDiff(context.Background(), s.container())
	s.Require().NoError(err)
	s.Equal("diff --git a/x b/x\n", string(got))
	s.Equal([]string{"git add --all --intent-to-add", "git diff"}, s.engine.commands())
}

func (s *containerSuite) TestSeedWritesOneMagusfilePerModuleAndExcludesThem() {
	s.engine.stdout = "go.mod\x00internal/tools/go.mod\x00vendor/x/go.mod\x00cmd/testdata/m/go.mod\x00"
	modules, err := seedModules(context.Background(), s.container())
	s.Require().NoError(err)
	s.Equal([]string{".", "internal/tools"}, modules)
	s.Equal(goModuleMagusfile, string(s.engine.files["/testbed/magusfile.buzz"]))
	s.Equal(goModuleMagusfile, string(s.engine.files["/testbed/internal/tools/magusfile.buzz"]))
	s.NotContains(s.engine.files, "/testbed/vendor/x/magusfile.buzz")
	s.Equal(seedMagusYAML, string(s.engine.files["/testbed/magus.yaml"]))
	s.Equal(seedExcludes, string(s.engine.files["/tmp/magus-bench/exclude"]))
	s.Contains(s.engine.commands(), `sh -c cat "$1" >> .git/info/exclude sh /tmp/magus-bench/exclude`)
}

func (s *containerSuite) TestSeedRefusesACheckoutWithoutARootModule() {
	s.engine.stdout = "tools/go.mod\x00"
	_, err := seedModules(context.Background(), s.container())
	s.ErrorContains(err, "no go.mod at its root")
	s.Empty(s.engine.files)
}

func (s *containerSuite) TestResolveImagePicksTheEngineBuildFromAnIndex() {
	reg, ref := s.registry(imageIndex(amd64, arm64), nil)
	img, err := resolveImage(context.Background(), reg, ref, arm64, false)
	s.Require().NoError(err)
	s.Equal(arm64.String(), img.Platform.String())
	s.False(img.Emulated)
	s.Equal(digest.FromBytes(imageIndex(amd64, arm64)), img.Digest)
	s.Equal(fmt.Sprintf("%s/swebench/x@%s", img.Ref.Registry, img.Digest), img.Pinned())
}

func (s *containerSuite) TestResolveImageRefusesAnAmd64OnlyImageOnArm64() {
	reg, ref := s.registry(nil, imageConfig(amd64))
	_, err := resolveImage(context.Background(), reg, ref, arm64, false)
	s.ErrorContains(err, "is built for linux/amd64 only and the engine runs linux/arm64, where it would run emulated")
}

func (s *containerSuite) TestResolveImageRunsAnAmd64OnlyImageEmulatedWhenAllowed() {
	reg, ref := s.registry(nil, imageConfig(amd64))
	img, err := resolveImage(context.Background(), reg, ref, arm64, true)
	s.Require().NoError(err)
	s.Equal(amd64, img.Platform)
	s.True(img.Emulated)
}

func (s *containerSuite) TestQualifyReadsANameAsTheDockerCLIDoes() {
	s.Equal("docker.io/swebench/sweb.eval.x86_64.gin-gonic__gin-1:latest", qualify("swebench/sweb.eval.x86_64.gin-gonic__gin-1"))
	s.Equal("ghcr.io/a/b:v1", qualify("ghcr.io/a/b:v1"))
	s.Equal("localhost:5000/a/b:latest", qualify("localhost:5000/a/b"))
}

// registry serves one repository, swebench/x:latest, whose tag names index when it
// is set and otherwise a single manifest over config.
func (s *containerSuite) registry(index, config []byte) (*oci.Client, string) {
	body, mediaType := index, ocispec.MediaTypeImageIndex
	if index == nil {
		man, err := json.Marshal(ocispec.Manifest{
			MediaType: ocispec.MediaTypeImageManifest,
			Config:    ocispec.Descriptor{MediaType: ocispec.MediaTypeImageConfig, Digest: digest.FromBytes(config), Size: int64(len(config))},
		})
		s.Require().NoError(err)
		body, mediaType = man, ocispec.MediaTypeImageManifest
	}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v2/":
			w.WriteHeader(http.StatusOK)
		case r.URL.Path == "/v2/swebench/x/manifests/latest":
			w.Header().Set("Content-Type", mediaType)
			_, _ = w.Write(body)
		case r.URL.Path == "/v2/swebench/x/blobs/"+digest.FromBytes(config).String():
			_, _ = w.Write(config)
		default:
			http.NotFound(w, r)
		}
	}))
	s.T().Cleanup(srv.Close)
	return &oci.Client{HTTP: srv.Client()}, strings.TrimPrefix(srv.URL, "https://") + "/swebench/x"
}

func imageIndex(ps ...image.Platform) []byte {
	idx := ocispec.Index{MediaType: ocispec.MediaTypeImageIndex}
	for i, p := range ps {
		idx.Manifests = append(idx.Manifests, ocispec.Descriptor{
			MediaType: ocispec.MediaTypeImageManifest, Digest: digest.FromString(fmt.Sprint(i)), Size: 1,
			Platform: &ocispec.Platform{OS: p.OS, Architecture: p.Architecture},
		})
	}
	idx.Manifests = append(idx.Manifests, ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest, Digest: digest.FromString("att"), Size: 1,
		Platform: &ocispec.Platform{OS: "unknown", Architecture: "unknown"},
	})
	raw, err := json.Marshal(idx)
	if err != nil {
		panic(err)
	}
	return raw
}

func imageConfig(p image.Platform) []byte {
	raw, err := json.Marshal(ocispec.Image{Platform: ocispec.Platform{OS: p.OS, Architecture: p.Architecture}})
	if err != nil {
		panic(err)
	}
	return raw
}

// writeELF writes the 64-byte header of an ELF64 executable for machine, which is
// all debug/elf reads of a file with no sections.
func writeELF(t *testing.T, machine uint16) string {
	t.Helper()
	var buf bytes.Buffer
	buf.Write([]byte{0x7f, 'E', 'L', 'F', 2, 1, 1, 0})
	buf.Write(make([]byte, 8))
	le := binary.LittleEndian
	for _, v := range []any{uint16(2), machine, uint32(1), uint64(0), uint64(0), uint64(0), uint32(0),
		uint16(64), uint16(56), uint16(0), uint16(64), uint16(0), uint16(0)} {
		if err := binary.Write(&buf, le, v); err != nil {
			t.Fatal(err)
		}
	}
	bin := filepath.Join(t.TempDir(), "magus")
	if err := os.WriteFile(bin, buf.Bytes(), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

// fakeEngine answers the engineClient API calls this package makes. Every exec but the
// ones exit fails prints stdout and stderr and exits 0.
type fakeEngine struct {
	mu              sync.Mutex
	arch            string
	pulled          url.Values
	pullError       string
	createError     string
	created         createRequest
	createdPlatform string
	started         bool
	killed          bool
	files           map[string][]byte
	modes           map[string]int64
	pending         map[string][]string
	execs           [][]string
	exit            func(argv string) int
	stdout, stderr  string
	hang            bool
	lastExit        int
}

func (f *fakeEngine) commands() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.execs))
	for i, argv := range f.execs {
		out[i] = strings.Join(argv, " ")
	}
	return out
}

func (f *fakeEngine) mux() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("GET /v1.41/info", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]string{"OSType": "linux", "Architecture": f.arch})
	})
	m.HandleFunc("POST /v1.41/images/create", func(w http.ResponseWriter, r *http.Request) {
		f.pulled = r.URL.Query()
		writeJSON(w, map[string]string{"status": "Pulling"})
		if f.pullError != "" {
			writeJSON(w, map[string]string{"error": f.pullError})
		}
	})
	m.HandleFunc("POST /v1.41/containers/create", func(w http.ResponseWriter, r *http.Request) {
		if f.createError != "" {
			w.WriteHeader(http.StatusConflict)
			writeJSON(w, map[string]string{"message": f.createError})
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&f.created); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		f.createdPlatform = r.URL.Query().Get("platform")
		w.WriteHeader(http.StatusCreated)
		writeJSON(w, map[string]string{"Id": "c1"})
	})
	m.HandleFunc("POST /v1.41/containers/c1/start", func(w http.ResponseWriter, _ *http.Request) {
		f.started = true
		w.WriteHeader(http.StatusNoContent)
	})
	m.HandleFunc("POST /v1.41/containers/c1/kill", func(w http.ResponseWriter, _ *http.Request) {
		f.mu.Lock()
		f.killed = true
		f.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})
	m.HandleFunc("DELETE /v1.41/containers/c1", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	m.HandleFunc("PUT /v1.41/containers/c1/archive", f.archive)
	m.HandleFunc("POST /v1.41/containers/c1/exec", func(w http.ResponseWriter, r *http.Request) {
		var req execRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		f.mu.Lock()
		if f.pending == nil {
			f.pending = map[string][]string{}
		}
		id := fmt.Sprintf("e%d", len(f.execs))
		f.pending[id] = req.Cmd
		f.execs = append(f.execs, req.Cmd)
		f.mu.Unlock()
		w.WriteHeader(http.StatusCreated)
		writeJSON(w, map[string]string{"Id": id})
	})
	m.HandleFunc("POST /v1.41/exec/{id}/start", f.start)
	m.HandleFunc("GET /v1.41/exec/{id}/json", func(w http.ResponseWriter, _ *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		writeJSON(w, map[string]any{"Running": false, "ExitCode": f.lastExit})
	})
	return m
}

func (f *fakeEngine) archive(w http.ResponseWriter, r *http.Request) {
	tr := tar.NewReader(r.Body)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		data, err := io.ReadAll(tr)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		name := filepath.Join(r.URL.Query().Get("path"), hdr.Name)
		f.mu.Lock()
		f.files[name] = data
		if f.modes == nil {
			f.modes = map[string]int64{}
		}
		f.modes[name] = hdr.Mode
		f.mu.Unlock()
	}
	w.WriteHeader(http.StatusOK)
}

func (f *fakeEngine) start(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	argv := strings.Join(f.pending[r.PathValue("id")], " ")
	code := 0
	if f.exit != nil {
		code = f.exit(argv)
	}
	f.lastExit = code
	hang := f.hang && strings.HasPrefix(argv, "bash ")
	f.mu.Unlock()
	w.Header().Set("Content-Type", "application/vnd.docker.multiplexed-stream")
	w.WriteHeader(http.StatusOK)
	if hang {
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		return
	}
	writeFrame(w, 1, f.stdout)
	writeFrame(w, 2, f.stderr)
}

func writeFrame(w io.Writer, stream byte, payload string) {
	if payload == "" {
		return
	}
	header := []byte{stream, 0, 0, 0, 0, 0, 0, 0}
	binary.BigEndian.PutUint32(header[4:], uint32(len(payload)))
	_, _ = w.Write(append(header, payload...))
}

func writeJSON(w io.Writer, v any) {
	raw, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	_, _ = w.Write(append(raw, '\n'))
}
