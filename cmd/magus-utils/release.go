package main

import (
	"bufio"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// ReleaseManifest is the machine-readable record for one shipped release.
// One file per version lives at releases/v<semver>.yaml in the repo root.
// Files are append-only and immutable once merged.
//
// Schema version 1 fields; additive changes only - do not remove or rename.
type ReleaseManifest struct {
	// Version is the semver tag, e.g. "v0.1.0".
	Version string `yaml:"version" json:"version"`
	// Date is the release date in YYYY-MM-DD form.
	Date string `yaml:"date" json:"date"`
	// Notes holds the Keep-a-Changelog sections as structured lists.
	Notes ReleaseNotes `yaml:"notes" json:"notes"`
	// Body is the trimmed markdown body of the release notes, used verbatim as
	// the Atom feed <summary>. Generated from Notes at cut time; for historical
	// releases, copied verbatim from CHANGELOG.md (trimmed, no leading newline).
	// XML-unsafe characters are escaped by the feed renderer, not here.
	Body string `yaml:"body" json:"body"`
	// Artifacts is the list of release assets with their verified checksums.
	// Sizes and SHA256 digests are populated by the release-cut tool at cut time.
	// Empty strings indicate the field was not available at migration time.
	Artifacts []ReleaseArtifact `yaml:"artifacts" json:"artifacts"`
	// Yanked, when true, marks a release that should not be used (security issue, etc.).
	Yanked bool `yaml:"yanked,omitempty" json:"yanked,omitempty"`
}

// ReleaseNotes holds the Keep-a-Changelog sections.
type ReleaseNotes struct {
	Added   []string `yaml:"added,omitempty"   json:"added,omitempty"`
	Changed []string `yaml:"changed,omitempty" json:"changed,omitempty"`
	Fixed   []string `yaml:"fixed,omitempty"   json:"fixed,omitempty"`
	Removed []string `yaml:"removed,omitempty" json:"removed,omitempty"`
}

// ReleaseArtifact is one downloadable asset with its integrity data.
type ReleaseArtifact struct {
	Name     string `yaml:"name"     json:"name"`
	Platform string `yaml:"platform" json:"platform"`
	// Size is the byte count of the artifact as a decimal string, or "".
	Size string `yaml:"size" json:"size"`
	// SHA256 is the lowercase hex SHA-256 digest of the artifact, or "".
	SHA256 string `yaml:"sha256" json:"sha256"`
}

// ReleaseIndex is the machine-readable index emitted at
// gen/public/release/index.json. The URL and schema are frozen at birth;
// additive changes only.
type ReleaseIndex struct {
	SchemaVersion int               `json:"schema_version"`
	Releases      []ReleaseManifest `json:"releases"`
}

// loadManifests reads all releases/*.yaml files from dir, sorted newest-first
// by semver (descending by version string, which works for vX.Y.Z lexicographic
// order within each numeric segment). Returns a fatal error on parse failure.
func loadManifests(dir string) ([]ReleaseManifest, error) {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", dir, err)
	}

	var manifests []ReleaseManifest
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		var m ReleaseManifest
		if err := yaml.Unmarshal(data, &m); err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
		manifests = append(manifests, m)
	}

	// Sort newest-first. Version strings are "v<major>.<minor>.<patch>"; sort by
	// splitting numerically via semver ordering (fall back to lexicographic for unusual tags).
	sort.Slice(manifests, func(i, j int) bool {
		return compareSemver(manifests[i].Version, manifests[j].Version) > 0
	})
	return manifests, nil
}

// compareSemver compares two "vX.Y.Z" strings numerically.
// Returns >0 if a > b, <0 if a < b, 0 if equal.
func compareSemver(a, b string) int {
	pa := parseSemver(a)
	pb := parseSemver(b)
	for i := 0; i < 3; i++ {
		if pa[i] != pb[i] {
			return pa[i] - pb[i]
		}
	}
	return 0
}

func parseSemver(v string) [3]int {
	v = strings.TrimPrefix(v, "v")
	parts := strings.SplitN(v, ".", 3)
	var out [3]int
	for i, p := range parts {
		if i >= 3 {
			break
		}
		n := 0
		for _, c := range p {
			if c >= '0' && c <= '9' {
				n = n*10 + int(c-'0')
			} else {
				break
			}
		}
		out[i] = n
	}
	return out
}

// runCut writes a releases/v<version>.yaml manifest from the built artifacts
// in artifactsDir and the Unreleased section of CHANGELOG.md.
//
// Usage: magus-utils cut -version v0.2.0 -artifacts ./dist -changelog ./CHANGELOG.md -out ./releases
//
// The MAGUS_SIGNING_KEY env var is NOT required here; signing SHA256SUMS is a
// separate step (magus-utils sign). The manifest itself is not signed; only
// index.json is signed (by runReleaseIndex).
func runCut(args []string) error {
	// Simple flag parsing without flag package to avoid import bloat.
	var version, artifactsDir, changelogPath, outDir string
	for i := 0; i < len(args)-1; i++ {
		switch args[i] {
		case "-version":
			version = args[i+1]
			i++
		case "-artifacts":
			artifactsDir = args[i+1]
			i++
		case "-changelog":
			changelogPath = args[i+1]
			i++
		case "-out":
			outDir = args[i+1]
			i++
		}
	}
	if version == "" || artifactsDir == "" || changelogPath == "" || outDir == "" {
		return fmt.Errorf("usage: magus-utils cut -version v0.2.0 -artifacts ./dist -changelog ./CHANGELOG.md -out ./releases")
	}

	// Extract the Unreleased section from CHANGELOG.md.
	notes, body, err := parseUnreleased(changelogPath)
	if err != nil {
		return fmt.Errorf("parse changelog: %w", err)
	}
	if body == "" {
		return fmt.Errorf("CHANGELOG.md has no [Unreleased] section with content")
	}

	// Scan the artifacts directory for release assets and compute their sizes + SHA256.
	var artifacts []ReleaseArtifact
	entries, err := os.ReadDir(artifactsDir)
	if err != nil {
		return fmt.Errorf("read artifacts dir %s: %w", artifactsDir, err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		// Only include files that look like release assets (tarballs and checksums).
		if !isReleaseAsset(name) {
			continue
		}
		path := filepath.Join(artifactsDir, name)
		size, digest, err := fileSizeAndSHA256(path)
		if err != nil {
			return fmt.Errorf("hash %s: %w", name, err)
		}
		artifacts = append(artifacts, ReleaseArtifact{
			Name:     name,
			Platform: platformFromName(name, version),
			Size:     fmt.Sprintf("%d", size),
			SHA256:   digest,
		})
	}
	// Guard: require at least one binary or checksum artifact before appending the .pem.
	// An empty artifacts list means the directory contained no release assets, which is
	// almost certainly a path mistake rather than a valid hollow release.
	if len(artifacts) == 0 {
		return fmt.Errorf("no release artifacts found in %s (expected *.tar.gz or SHA256SUMS)", artifactsDir)
	}
	// Add the release signing key (not a binary artifact; no size/sha256 needed here).
	artifacts = append(artifacts, ReleaseArtifact{
		Name:     "magus-release.pem",
		Platform: "",
		Size:     "",
		SHA256:   "",
	})

	m := ReleaseManifest{
		Version:   version,
		Date:      time.Now().UTC().Format("2006-01-02"),
		Notes:     notes,
		Body:      body,
		Artifacts: artifacts,
	}

	out, err := yaml.Marshal(m)
	if err != nil {
		return fmt.Errorf("marshal manifest: %w", err)
	}

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", outDir, err)
	}
	outPath := filepath.Join(outDir, version+".yaml")
	if _, err := os.Stat(outPath); err == nil {
		return fmt.Errorf("%s already exists; release manifests are immutable once committed", outPath)
	}
	if err := os.WriteFile(outPath, out, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", outPath, err)
	}
	fmt.Printf("wrote %s\n", outPath)
	return nil
}

// runMigrate reads CHANGELOG.md and writes a releases/*.yaml for every released
// version. It is a one-shot migration tool: run once, then delete the
// parseChangelog function from scribe.buzz.
//
// Usage: magus-utils migrate -changelog ./CHANGELOG.md -out ./releases
func runMigrate(args []string) error {
	var changelogPath, outDir string
	for i := 0; i < len(args)-1; i++ {
		switch args[i] {
		case "-changelog":
			changelogPath = args[i+1]
			i++
		case "-out":
			outDir = args[i+1]
			i++
		}
	}
	if changelogPath == "" || outDir == "" {
		return fmt.Errorf("usage: magus-utils migrate -changelog ./CHANGELOG.md -out ./releases")
	}

	releases, err := parseReleasedVersions(changelogPath)
	if err != nil {
		return fmt.Errorf("parse changelog: %w", err)
	}

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", outDir, err)
	}

	for _, r := range releases {
		notes, body := notesFromBody(r.body)
		m := ReleaseManifest{
			Version:   r.version,
			Date:      r.date,
			Notes:     notes,
			Body:      body,
			Artifacts: historicalArtifacts(r.version),
		}
		out, err := yaml.Marshal(m)
		if err != nil {
			return fmt.Errorf("marshal %s: %w", r.version, err)
		}
		outPath := filepath.Join(outDir, r.version+".yaml")
		if err := os.WriteFile(outPath, out, 0o644); err != nil {
			return fmt.Errorf("write %s: %w", outPath, err)
		}
		fmt.Printf("wrote %s\n", outPath)
	}
	return nil
}

// runReleaseIndex reads the scribe-emitted website/gen/public/release/index.json and
// signs those exact bytes into index.json.sig. Scribe is the single source of truth
// for the served file; this tool only adds the signature so the sig covers the bytes
// the client actually downloads. Signing requires MAGUS_SIGNING_KEY to be set.
//
// Usage: magus-utils release-index -served ./website/gen/public/release [-no-sign]
//
// The -releases and -out flags are accepted but ignored (kept for backward compat with
// any existing CI invocations; a future cleanup may remove them).
func runReleaseIndex(args []string) error {
	var servedDir string
	var skipSign bool
	for i := 0; i < len(args)-1; i++ {
		switch args[i] {
		case "-served":
			servedDir = args[i+1]
			i++
		// Accept -out as an alias for -served (backward compat).
		case "-out":
			if servedDir == "" {
				servedDir = args[i+1]
			}
			i++
		// Accept -releases (no longer used; kept for backward compat).
		case "-releases":
			i++ // consume the value and ignore
		}
	}
	for _, a := range args {
		if a == "-no-sign" {
			skipSign = true
		}
	}
	if servedDir == "" {
		return fmt.Errorf("usage: magus-utils release-index -served ./website/gen/public/release [-no-sign]")
	}

	idxPath := filepath.Join(servedDir, "index.json")
	// Read the scribe-emitted file. This is the exact JSON the client downloads, so the
	// signature covers what is actually served - not a re-rendered Go-side copy.
	data, err := os.ReadFile(idxPath)
	if err != nil {
		return fmt.Errorf("read %s: %w (run `magus run generate website` first)", idxPath, err)
	}
	fmt.Printf("signing %s (%d bytes)\n", idxPath, len(data))

	if skipSign {
		return nil
	}

	// Sign with MAGUS_SIGNING_KEY (same format as runSign).
	keyHex := os.Getenv("MAGUS_SIGNING_KEY")
	if keyHex == "" {
		fmt.Fprintf(os.Stderr, "MAGUS_SIGNING_KEY not set; skipping index.json.sig\n")
		return nil
	}
	keyBytes, err := hex.DecodeString(keyHex)
	if err != nil {
		return fmt.Errorf("decode MAGUS_SIGNING_KEY: %w", err)
	}
	if len(keyBytes) != ed25519.PrivateKeySize {
		return fmt.Errorf("MAGUS_SIGNING_KEY must be %d bytes (%d hex chars), got %d bytes",
			ed25519.PrivateKeySize, ed25519.PrivateKeySize*2, len(keyBytes))
	}
	sig := ed25519.Sign(ed25519.PrivateKey(keyBytes), data)
	sigPath := idxPath + ".sig"
	if err := os.WriteFile(sigPath, sig, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", sigPath, err)
	}
	fmt.Printf("signed %s -> %s\n", idxPath, sigPath)
	return nil
}

// runGenerateChangelog regenerates CHANGELOG.md from releases/*.yaml, preserving
// the [Unreleased] section verbatim. This is the drift-gate-safe inverse of migration.
//
// Usage: magus-utils generate-changelog -releases ./releases -changelog ./CHANGELOG.md
func runGenerateChangelog(args []string) error {
	var releasesDir, changelogPath string
	for i := 0; i < len(args)-1; i++ {
		switch args[i] {
		case "-releases":
			releasesDir = args[i+1]
			i++
		case "-changelog":
			changelogPath = args[i+1]
			i++
		}
	}
	if releasesDir == "" || changelogPath == "" {
		return fmt.Errorf("usage: magus-utils generate-changelog -releases ./releases -changelog ./CHANGELOG.md")
	}

	// Read the current Unreleased section from CHANGELOG.md.
	unreleased, err := readUnreleasedSection(changelogPath)
	if err != nil {
		return fmt.Errorf("read unreleased: %w", err)
	}

	manifests, err := loadManifests(releasesDir)
	if err != nil {
		return err
	}

	var b strings.Builder
	b.WriteString("# Changelog\n\n")
	b.WriteString("All notable changes to this project will be documented in this file.\n")
	b.WriteString("The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),\n")
	b.WriteString("and this project adheres to [Semantic Versioning](https://semver.org/).\n")
	b.WriteString("\n")
	// Unreleased section - preserved verbatim. The body already ends with "\n"
	// (the trailing empty line before the next section in the original file);
	// we strip it here so the released-section separator ("\n## [...]") produces
	// exactly one blank line between them, matching the original Keep-a-Changelog
	// format.
	b.WriteString("## [Unreleased]\n")
	if unreleased != "" {
		trimmed := strings.TrimRight(unreleased, "\n")
		b.WriteString(trimmed)
		b.WriteString("\n")
	}
	// Released sections - generated from manifests (newest first).
	// Format: blank line + "## [version] - date" + blank line + body.
	// This matches Keep-a-Changelog convention and preserves the exact text that
	// was in CHANGELOG.md before the inversion (body is already trimmed).
	for _, m := range manifests {
		b.WriteString("\n## [")
		b.WriteString(m.Version)
		b.WriteString("] - ")
		b.WriteString(m.Date)
		b.WriteString("\n\n")
		b.WriteString(m.Body)
		b.WriteString("\n")
	}

	return os.WriteFile(changelogPath, []byte(b.String()), 0o644)
}

// --- Helpers ---

// changelogEntry is a parsed CHANGELOG release (version, date, raw body).
type changelogEntry struct {
	version string
	date    string
	body    string // raw body text including leading \n, NOT trimmed
}

// parseReleasedVersions reads CHANGELOG.md and returns all released versions
// (skipping [Unreleased]), preserving the raw body text per section.
// This mirrors the logic of scribe.buzz's parseChangelog.
func parseReleasedVersions(path string) ([]changelogEntry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var entries []changelogEntry
	var cur changelogEntry
	have := false
	var bodyLines []string

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "## ") {
			// Flush previous.
			if have {
				cur.body = strings.Join(bodyLines, "\n") + "\n"
				entries = append(entries, cur)
			}
			cur = changelogEntry{}
			bodyLines = bodyLines[:0]
			have = false

			rest := line[3:]
			if strings.HasPrefix(rest, "[") {
				close := strings.Index(rest, "]")
				if close >= 0 {
					ver := rest[1:close]
					if !strings.EqualFold(ver, "unreleased") {
						cur.version = ver
						rem := rest[close+1:]
						if dash := strings.Index(rem, "-"); dash >= 0 {
							cur.date = strings.TrimSpace(rem[dash+1:])
						}
						have = true
						bodyLines = []string{""} // leading blank line, matching Buzz accumulation
					}
				}
			}
		} else if have {
			bodyLines = append(bodyLines, line)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if have {
		cur.body = strings.Join(bodyLines, "\n") + "\n"
		entries = append(entries, cur)
	}
	return entries, nil
}

// parseUnreleased extracts the [Unreleased] section from CHANGELOG.md and
// returns it as both structured notes and a trimmed body string.
func parseUnreleased(path string) (ReleaseNotes, string, error) {
	f, err := os.Open(path)
	if err != nil {
		return ReleaseNotes{}, "", err
	}
	defer f.Close()

	var bodyLines []string
	inUnreleased := false

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "## ") {
			if inUnreleased {
				// Hit the next section - done.
				break
			}
			rest := line[3:]
			if strings.HasPrefix(rest, "[") {
				close := strings.Index(rest, "]")
				if close >= 0 && strings.EqualFold(rest[1:close], "unreleased") {
					inUnreleased = true
					bodyLines = []string{""} // leading blank line
					continue
				}
			}
		} else if inUnreleased {
			bodyLines = append(bodyLines, line)
		}
	}
	if err := scanner.Err(); err != nil {
		return ReleaseNotes{}, "", err
	}

	raw := strings.Join(bodyLines, "\n") + "\n"
	body := strings.TrimSpace(raw)
	notes := notesFromBodyString(body)
	return notes, body, nil
}

// readUnreleasedSection returns the body of the [Unreleased] section (everything
// after the `## [Unreleased]` heading, up to the next `## ` heading), with a
// leading newline if non-empty. Used by generate-changelog to preserve it verbatim.
func readUnreleasedSection(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	defer f.Close()

	var bodyLines []string
	inUnreleased := false

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "## ") {
			if inUnreleased {
				break
			}
			rest := line[3:]
			if strings.HasPrefix(rest, "[") {
				close := strings.Index(rest, "]")
				if close >= 0 && strings.EqualFold(rest[1:close], "unreleased") {
					inUnreleased = true
					continue
				}
			}
		} else if inUnreleased {
			bodyLines = append(bodyLines, line)
		}
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	if len(bodyLines) == 0 {
		return "", nil
	}
	// bodyLines[0] is the empty line after "## [Unreleased]", so the join
	// already starts with a newline; no extra prefix needed.
	return strings.Join(bodyLines, "\n") + "\n", nil
}

// notesFromBody parses a raw body string (with leading newline) into structured
// notes AND returns the trimmed body for the Atom feed.
func notesFromBody(raw string) (ReleaseNotes, string) {
	body := strings.TrimSpace(raw)
	notes := notesFromBodyString(body)
	return notes, body
}

// notesFromBodyString parses a trimmed body into structured notes sections.
func notesFromBodyString(body string) ReleaseNotes {
	var notes ReleaseNotes
	var section string
	var items []string

	flush := func() {
		if len(items) == 0 {
			return
		}
		switch strings.ToLower(section) {
		case "added":
			notes.Added = append(notes.Added, items...)
		case "changed":
			notes.Changed = append(notes.Changed, items...)
		case "fixed":
			notes.Fixed = append(notes.Fixed, items...)
		case "removed":
			notes.Removed = append(notes.Removed, items...)
		}
		items = nil
	}

	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "### ") {
			flush()
			section = strings.TrimSpace(line[4:])
			items = nil
		} else if strings.HasPrefix(line, "- ") {
			items = append(items, strings.TrimPrefix(line, "- "))
		} else if len(items) > 0 && (strings.HasPrefix(line, "  ") || strings.HasPrefix(line, "\t")) {
			// Continuation of the previous item.
			items[len(items)-1] += "\n" + strings.TrimPrefix(line, "  ")
		}
	}
	flush()
	return notes
}

// isReleaseAsset reports whether a filename looks like a release artifact.
func isReleaseAsset(name string) bool {
	if strings.HasSuffix(name, ".tar.gz") {
		return true
	}
	if name == "SHA256SUMS" || name == "SHA256SUMS.sig" {
		return true
	}
	return false
}

// platformFromName infers the platform string from a tarball filename.
// e.g. "magus_v0.2.0_linux_amd64.tar.gz" -> "linux/amd64"
func platformFromName(name, version string) string {
	// Strip "magus_<version>_" prefix and ".tar.gz" suffix.
	prefix := "magus_" + version + "_"
	if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, ".tar.gz") {
		return ""
	}
	mid := name[len(prefix) : len(name)-len(".tar.gz")]
	// mid is e.g. "linux_amd64"
	parts := strings.SplitN(mid, "_", 2)
	if len(parts) == 2 {
		return parts[0] + "/" + parts[1]
	}
	return mid
}

// fileSizeAndSHA256 returns the byte size and lowercase hex SHA-256 of a file.
func fileSizeAndSHA256(path string) (int64, string, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, "", err
	}
	defer f.Close()

	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return 0, "", err
	}
	return n, hex.EncodeToString(h.Sum(nil)), nil
}

// historicalArtifacts returns the standard 8-artifact list for a historical
// release with no size/sha256 data (they were not available during migration).
func historicalArtifacts(version string) []ReleaseArtifact {
	v := version
	return []ReleaseArtifact{
		{Name: "magus_" + v + "_linux_amd64.tar.gz", Platform: "linux/amd64", Size: "", SHA256: ""},
		{Name: "magus_" + v + "_linux_arm64.tar.gz", Platform: "linux/arm64", Size: "", SHA256: ""},
		{Name: "magus_" + v + "_darwin_amd64.tar.gz", Platform: "darwin/amd64", Size: "", SHA256: ""},
		{Name: "magus_" + v + "_darwin_arm64.tar.gz", Platform: "darwin/arm64", Size: "", SHA256: ""},
		{Name: "magus_" + v + "_windows_amd64.tar.gz", Platform: "windows/amd64", Size: "", SHA256: ""},
		{Name: "SHA256SUMS", Platform: "", Size: "", SHA256: ""},
		{Name: "SHA256SUMS.sig", Platform: "", Size: "", SHA256: ""},
		{Name: "magus-release.pem", Platform: "", Size: "", SHA256: ""},
	}
}

// verifyIndexSig verifies index.json against index.json.sig using the embedded
// release public key. Used in tests and by consumers.
func verifyIndexSig(data, sig []byte, pubKey ed25519.PublicKey) bool {
	return ed25519.Verify(pubKey, data, sig)
}

// verifyIndexSigFile verifies outDir/index.json against outDir/index.json.sig.
func verifyIndexSigFile(outDir string, pubKey ed25519.PublicKey) error {
	idxPath := filepath.Join(outDir, "index.json")
	sigPath := idxPath + ".sig"

	data, err := os.ReadFile(idxPath)
	if err != nil {
		return fmt.Errorf("read %s: %w", idxPath, err)
	}
	sig, err := os.ReadFile(sigPath)
	if err != nil {
		return fmt.Errorf("read %s: %w", sigPath, err)
	}
	if !verifyIndexSig(data, sig, pubKey) {
		return fmt.Errorf("index.json signature verification failed")
	}
	return nil
}
