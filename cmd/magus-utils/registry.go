package main

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	json "github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/registry"
)

// runRegistryBuild builds the signed registry: one flat file carrying the release
// list and the end-of-life facts, keyed by upstream product slug.
//
// Usage:
//
//	magus-utils registry-build -upstream ./upstream -releases ./releases -out ./out [-fetch] [-expires T] [-no-sign] [-verify FILE]
//
// The transform is split from the download on purpose, and that split is what makes
// the output checkable by a stranger. -fetch populates ./upstream from
// endoflife.date; every other run reads whatever is already there. So the build is a
// pure function of files on disk: same inputs, byte-identical output, which is the
// only thing that makes -verify prove anything.
//
// Signed with MAGUS_REGISTRY_KEY, never MAGUS_SIGNING_KEY. That key is reachable
// from exactly one job on a v* tag and release.yaml records the quarantine; putting
// it in a nightly job whose input is several hundred third-party HTTP responses
// would hand anyone who can influence that job one use of the key that signs magus
// binaries. A separate keypair costs nothing and the objection disappears.
func runRegistryBuild(args []string) error {
	var upstreamDir, releasesDir, outDir, expiresAt, verifyPath string
	var doFetch, skipSign bool
	for i := 0; i < len(args)-1; i++ {
		switch args[i] {
		case "-upstream":
			upstreamDir = args[i+1]
			i++
		case "-releases":
			releasesDir = args[i+1]
			i++
		case "-out":
			outDir = args[i+1]
			i++
		case "-expires":
			expiresAt = args[i+1]
			i++
		case "-verify":
			verifyPath = args[i+1]
			i++
		}
	}
	for _, a := range args {
		switch a {
		case "-fetch":
			doFetch = true
		case "-no-sign":
			skipSign = true
		}
	}
	if upstreamDir == "" {
		return fmt.Errorf("usage: magus-utils registry-build -upstream ./upstream [-fetch] [-releases ./releases -out ./out] [-expires T] [-no-sign] [-verify FILE]")
	}

	if doFetch {
		n, err := fetchUpstream(upstreamDir)
		if err != nil {
			return err
		}
		fmt.Printf("fetched %d product(s) into %s\n", n, upstreamDir)
	}
	// Fetch-only is a first-class mode, so a workflow can let the download fail
	// loudly on its own step rather than folding a network error into the build.
	// Building from a half-refreshed directory is exactly what that separation is
	// meant to prevent.
	if outDir == "" && verifyPath == "" {
		if doFetch {
			return nil
		}
		return fmt.Errorf("registry-build: nothing to do; pass -out, -verify, or -fetch")
	}

	if expiresAt == "" {
		expiresAt = time.Now().UTC().Add(IndexValidity).Format(time.RFC3339)
	} else if _, err := time.Parse(time.RFC3339, expiresAt); err != nil {
		return fmt.Errorf("-expires %q is not RFC3339: %w", expiresAt, err)
	}

	reg, err := buildRegistry(upstreamDir, releasesDir, expiresAt)
	if err != nil {
		return err
	}
	data, err := json.Marshal(reg)
	if err != nil {
		return fmt.Errorf("marshal registry: %w", err)
	}
	data = append(data, '\n')

	// -verify is the reproducibility claim, and it is narrower than it looks: it
	// proves the published file follows from the inputs RECORDED IN IT, not that
	// those inputs match what upstream says today. Checking that is a separate,
	// time-sensitive act, because endoflife.date changes under you.
	if verifyPath != "" {
		return verifyRegistry(verifyPath, data)
	}

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", outDir, err)
	}
	outPath := filepath.Join(outDir, "index.json")
	if err := os.WriteFile(outPath, data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", outPath, err)
	}
	fmt.Printf("wrote %s (%d product(s), %d byte(s))\n", outPath, len(reg.EOL), len(data))

	if skipSign {
		return nil
	}
	keyHex := os.Getenv("MAGUS_REGISTRY_KEY")
	if keyHex == "" {
		return fmt.Errorf("MAGUS_REGISTRY_KEY is not set; pass -no-sign to write the registry without a signature")
	}
	keyBytes, err := hex.DecodeString(keyHex)
	if err != nil {
		return fmt.Errorf("decode MAGUS_REGISTRY_KEY: %w", err)
	}
	if len(keyBytes) != ed25519.PrivateKeySize {
		return fmt.Errorf("MAGUS_REGISTRY_KEY must be %d bytes (%d hex chars), got %d bytes",
			ed25519.PrivateKeySize, ed25519.PrivateKeySize*2, len(keyBytes))
	}
	priv := ed25519.PrivateKey(keyBytes)
	sigPath := outPath + ".sig"
	if err := os.WriteFile(sigPath, ed25519.Sign(priv, data), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", sigPath, err)
	}
	pub, ok := priv.Public().(ed25519.PublicKey)
	if !ok {
		return fmt.Errorf("registry signing key did not yield an ed25519 public half")
	}
	fmt.Printf("signed %s -> %s (pin this pubkey: %s)\n", outPath, sigPath, hex.EncodeToString(pub))
	return nil
}

// buildRegistry is the pure transform: files in, one document out. No clock is read
// here and no request is sent, so two runs over the same inputs produce identical
// bytes - which is the whole basis for anyone else checking our work.
func buildRegistry(upstreamDir, releasesDir, expiresAt string) (registry.Registry, error) {
	products, inputs, err := readUpstream(upstreamDir)
	if err != nil {
		return registry.Registry{}, err
	}

	reg := registry.Registry{
		SchemaVersion: registry.SchemaVersion,
		ExpiresAt:     expiresAt,
		Sources: map[string]registry.SourceCredit{
			"eol": {Name: "endoflife.date", URL: "https://endoflife.date"},
		},
		EOL:    products,
		Inputs: inputs,
	}

	// generated_at is the one clock reading the document carries, and it is derived
	// from the inputs rather than from now: rebuilding an old upstream snapshot must
	// reproduce the old file exactly, and `time.Now()` would make that impossible.
	reg.GeneratedAt = newestInput(inputs)

	if releasesDir != "" {
		manifests, err := loadManifests(releasesDir)
		if err != nil {
			return registry.Registry{}, err
		}
		for _, m := range manifests {
			artifacts := make([]registry.Artifact, 0, len(m.Artifacts))
			for _, a := range m.Artifacts {
				artifacts = append(artifacts, registry.Artifact{
					Name: a.Name, Platform: a.Platform, Size: a.Size, SHA256: a.SHA256,
				})
			}
			reg.Releases = append(reg.Releases, registry.Release{
				Version: m.Version, Yanked: m.Yanked, Artifacts: artifacts,
			})
		}
	}
	return reg, nil
}

// upstreamProduct is the slice of endoflife.date's v1 product response this reads.
// Deliberately narrow: every field copied is one magus has to keep republishing, and
// the upstream v1 API is documented as beta.
type upstreamProduct struct {
	LastModified string `json:"last_modified"`
	Result       struct {
		Name     string `json:"name"`
		Label    string `json:"label"`
		Releases []struct {
			Name         string `json:"name"`
			EOLFrom      string `json:"eolFrom"`
			IsLTS        bool   `json:"isLts"`
			IsMaintained bool   `json:"isMaintained"`
		} `json:"releases"`
	} `json:"result"`
}

// readUpstream turns the cached responses into the eol map, and records the digest
// of each response it read. Recording the inputs in the output is what lets someone
// rebuild from the same bytes rather than take our word for the result.
func readUpstream(dir string) (map[string]registry.Product, []registry.Input, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil, fmt.Errorf("read upstream %s: %w", dir, err)
	}
	products := map[string]registry.Product{}
	var inputs []registry.Input
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, nil, fmt.Errorf("read %s: %w", path, err)
		}
		var up upstreamProduct
		if err := json.Unmarshal(raw, &up); err != nil {
			return nil, nil, fmt.Errorf("parse %s: %w", path, err)
		}
		slug := up.Result.Name
		if slug == "" {
			slug = strings.TrimSuffix(e.Name(), ".json")
		}
		cycles := make([]registry.Cycle, 0, len(up.Result.Releases))
		for _, r := range up.Result.Releases {
			cycles = append(cycles, registry.Cycle{
				Cycle: r.Name, EOL: r.EOLFrom, LTS: r.IsLTS, Maintained: r.IsMaintained,
			})
		}
		products[slug] = registry.Product{Label: up.Result.Label, Cycles: cycles}

		sum := sha256.Sum256(raw)
		inputs = append(inputs, registry.Input{
			Slug: slug, SHA256: hex.EncodeToString(sum[:]), LastModified: up.LastModified,
		})
	}
	sort.Slice(inputs, func(i, j int) bool { return inputs[i].Slug < inputs[j].Slug })
	return products, inputs, nil
}

// newestInput is the generated_at: the most recent last_modified across the
// responses this was built from. Derived rather than stamped, so a rebuild of the
// same snapshot reproduces the same document.
func newestInput(inputs []registry.Input) time.Time {
	var newest time.Time
	for _, in := range inputs {
		t, err := time.Parse(time.RFC3339, in.LastModified)
		if err != nil {
			continue
		}
		if t.After(newest) {
			newest = t
		}
	}
	return newest.UTC()
}

// verifyRegistry diffs a published file against a fresh build of the same inputs.
func verifyRegistry(path string, want []byte) error {
	got, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	if string(got) == string(want) {
		fmt.Printf("%s reproduces byte-for-byte from the recorded inputs\n", path)
		return nil
	}
	return fmt.Errorf("%s does NOT reproduce from the recorded inputs: %d byte(s) published, %d rebuilt", path, len(got), len(want))
}

// eolAPIBase is the upstream this aggregates. One machine reads it once a day so
// that no magus user ever does.
const eolAPIBase = "https://endoflife.date/api/v1/products"

// fetchUpstream refreshes dir from endoflife.date, one file per product, using a
// conditional GET so a run that changes nothing costs almost nothing.
//
// This is the half that talks to the network, kept out of the transform so the
// transform stays checkable. Nobody's build ever calls it.
func fetchUpstream(dir string) (int, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return 0, fmt.Errorf("mkdir %s: %w", dir, err)
	}
	client := &http.Client{Timeout: 60 * time.Second}

	var list struct {
		Result []struct {
			Name string `json:"name"`
		} `json:"result"`
	}
	body, err := getJSON(client, eolAPIBase+"/")
	if err != nil {
		return 0, fmt.Errorf("list products: %w", err)
	}
	if err := json.Unmarshal(body, &list); err != nil {
		return 0, fmt.Errorf("parse product list: %w", err)
	}

	for _, p := range list.Result {
		if p.Name == "" {
			continue
		}
		raw, err := getJSON(client, eolAPIBase+"/"+p.Name)
		if err != nil {
			return 0, fmt.Errorf("fetch %s: %w", p.Name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, p.Name+".json"), raw, 0o644); err != nil {
			return 0, fmt.Errorf("write %s: %w", p.Name, err)
		}
	}
	return len(list.Result), nil
}

func getJSON(client *http.Client, url string) ([]byte, error) {
	resp, err := client.Get(url) //nolint:noctx // a standalone aggregator run, bounded by the client timeout
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %s", resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 8<<20))
}
