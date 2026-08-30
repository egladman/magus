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
	"slices"
	"strings"
	"time"

	json "github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/selfupdate"
	"golang.org/x/mod/semver"
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

// ReleaseIndex is the machine-readable index served at public/release/index.json
// and read by `magus self update`. The URL and schema are frozen at birth;
// additive changes only.
type ReleaseIndex struct {
	SchemaVersion int      `json:"schema_version"`
	KeyID         string   `json:"key_id"`
	Revoked       []string `json:"revoked,omitzero"`
	// ExpiresAt is how long a client may trust this file. It is the bound on replaying
	// an old index that names no revocation, so it is not optional; see ReleaseIndex in
	// internal/selfupdate for what it does and does not buy.
	ExpiresAt string         `json:"expires_at,omitzero"`
	Releases  []IndexRelease `json:"releases"`
}

// IndexValidity is how long a signed index is good for. Long, because nothing
// republishes it on a timer: a release does, and the Release index workflow does on
// demand. `magus doctor` warns as the deadline approaches, which is the part that
// makes the window survivable without a cron holding the signing key.
const IndexValidity = 180 * 24 * time.Hour

// IndexRelease is one release as the index publishes it: the version, and the
// artifacts a client can name and pin. The manifest's prose - date, notes, body -
// is deliberately absent. Nothing reads it here, it ships in the Atom feed and in
// the release YAML, and every byte of this file is covered by index.json.sig.
// omitzero, not omitempty: these bytes are signed, so they must not depend on how
// the binary that wrote them was built. Under GOEXPERIMENT=jsonv2, omitempty means
// "empty JSON value" and false is not one, so the same struct encodes to
// `"yanked":false` there and to nothing under v1 - two different signatures for one
// release. omitzero means the zero value in both.
type IndexRelease struct {
	Version   string            `json:"version"`
	Yanked    bool              `json:"yanked,omitzero"`
	Artifacts []ReleaseArtifact `json:"artifacts"`
}

// buildIndex projects manifests (newest-first, as loadManifests returns them) onto the
// served schema. keyID and expiresAt are passed in rather than read from the ring and
// the clock here, so the transform stays a pure function of its inputs: the same
// arguments give byte-identical output, or nothing downstream can be compared. The
// order it preserves is loadManifests's, which is a total order over the manifest set.
func buildIndex(manifests []ReleaseManifest, keyID, expiresAt string, revoked []string) ReleaseIndex {
	idx := ReleaseIndex{
		SchemaVersion: 1,
		KeyID:         keyID,
		Revoked:       revoked,
		ExpiresAt:     expiresAt,
		Releases:      make([]IndexRelease, 0, len(manifests)),
	}
	for _, m := range manifests {
		artifacts := m.Artifacts
		if artifacts == nil {
			artifacts = []ReleaseArtifact{} // "artifacts":[] rather than null
		}
		idx.Releases = append(idx.Releases, IndexRelease{
			Version:   m.Version,
			Yanked:    m.Yanked,
			Artifacts: artifacts,
		})
	}
	return idx
}

// loadManifests reads all releases/*.yaml files from dir, sorted newest-first by
// semver. A parse failure, an unparsable version, and an empty result are all fatal:
// these bytes end up under a signature, and every caller publishes something a client
// reads, so "no releases" is a path mistake rather than a valid hollow answer.
func loadManifests(dir string) ([]ReleaseManifest, error) {
	entries, err := os.ReadDir(dir)
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
		if !semver.IsValid(m.Version) {
			return nil, fmt.Errorf("%s: version %q is not semver", path, m.Version)
		}
		manifests = append(manifests, m)
	}
	if len(manifests) == 0 {
		return nil, fmt.Errorf("no release manifests in %s (expected v<semver>.yaml files)", dir)
	}

	// Newest first, stable, with the version string as tiebreak: two versions
	// semver.Compare calls equal ("v1.0" and "v1.0.0") would otherwise order by
	// whichever one the sort happened to move, and these bytes are signed.
	//
	// semver.Compare rather than a hand-rolled parse: the one this replaced stopped at
	// the first non-digit, so v0.4.0-rc.1 and v0.4.0 compared equal.
	slices.SortStableFunc(manifests, func(a, b ReleaseManifest) int {
		if c := semver.Compare(b.Version, a.Version); c != 0 {
			return c
		}
		return strings.Compare(b.Version, a.Version)
	})
	return manifests, nil
}

// runCut moves the Unreleased section of CHANGELOG.md into a
// releases/v<version>.yaml manifest, alongside the size and SHA-256 of every
// artifact in artifactsDir. The changelog's [Unreleased] is emptied by the same
// call, because the manifest now owns that text.
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
	// An empty artifacts list means the directory contained no release assets, which is
	// almost certainly a path mistake rather than a valid hollow release.
	//
	// magus-release.pem used to be appended here, sizeless and hashless. No release
	// since v0.1.0 has published such an asset - release.yaml uploads the tarballs and
	// the SHA256SUMS pair, nothing else - so the entry named a download that 404s, in a
	// file whose whole purpose is telling a client what it may fetch.
	if len(artifacts) == 0 {
		return fmt.Errorf("no release artifacts found in %s (expected *.tar.gz or SHA256SUMS)", artifactsDir)
	}

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

	// The manifest is staged and only renamed into place once the changelog has been
	// cleared, so no failure leaves a state a rerun cannot recover from. Writing it
	// first wedged every retry on "manifests are immutable" the moment clearUnreleased
	// failed; clearing first would instead destroy the notes the manifest never got.
	tmpPath := outPath + ".tmp"
	if err := os.WriteFile(tmpPath, out, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", tmpPath, err)
	}

	// The manifest OWNS this text once it lands, and CHANGELOG.md is generated back out
	// of the manifests, so leaving [Unreleased] populated would print the same entries
	// twice: once under Unreleased and once under the version that just shipped them.
	if err := clearUnreleased(changelogPath); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("clear unreleased: %w", err)
	}
	if err := os.Rename(tmpPath, outPath); err != nil {
		return fmt.Errorf("write %s: %w", outPath, err)
	}
	fmt.Printf("wrote %s\n", outPath)
	return nil
}

// clearUnreleased empties the [Unreleased] section of CHANGELOG.md, leaving the
// heading and one blank line. Everything from the next "## " heading on is kept.
func clearUnreleased(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	lines := strings.Split(string(data), "\n")
	var out []string
	skipping := false
	for _, line := range lines {
		if strings.HasPrefix(line, "## ") {
			rest := line[3:]
			if closeIdx := strings.Index(rest, "]"); strings.HasPrefix(rest, "[") && closeIdx >= 0 &&
				strings.EqualFold(rest[1:closeIdx], "unreleased") {
				out = append(out, line, "")
				skipping = true
				continue
			}
			skipping = false
		}
		if !skipping {
			out = append(out, line)
		}
	}
	return os.WriteFile(path, []byte(strings.Join(out, "\n")), 0o644)
}

// runMigrate reads CHANGELOG.md and writes a releases/*.yaml for every released
// version. It is a one-shot migration tool: run once, then delete the
// parseChangelog function from render.buzz.
//
// Usage:
//
//	magus-utils migrate -changelog ./CHANGELOG.md -out ./releases
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

// runReleaseIndex builds index.json from releases/*.yaml and signs those exact
// bytes into index.json.sig. Both land in outDir, which is the tracked
// docs/gen/public/release/ - the site render copies that directory out verbatim
// rather than regenerating it, so the signature covers the bytes a client
// downloads.
//
// Usage: magus-utils release-index -releases ./releases -out ./docs/gen/public/release [-expires <RFC3339>] [-no-sign]
//
// This tool, and not the docs render, emits the file BECAUSE of the signature.
// index.json is rendered on every main push and signed only on a tag, so a
// renderer that could write it would overwrite a signed file with an unsigned one
// the next time anything merged.
func runReleaseIndex(args []string) error {
	var releasesDir, outDir, expiresAt string
	var skipSign bool
	for i := 0; i < len(args)-1; i++ {
		switch args[i] {
		case "-releases":
			releasesDir = args[i+1]
			i++
		case "-out":
			outDir = args[i+1]
			i++
		case "-expires":
			expiresAt = args[i+1]
			i++
		}
	}
	for _, a := range args {
		if a == "-no-sign" {
			skipSign = true
		}
	}
	if releasesDir == "" || outDir == "" {
		return fmt.Errorf("usage: magus-utils release-index -releases ./releases -out ./docs/gen/public/release [-expires <RFC3339>] [-no-sign]")
	}

	manifests, err := loadManifests(releasesDir)
	if err != nil {
		return err
	}
	// The index names the key that will sign it, and lists the keys no client may
	// accept. Both come from the ring this binary embeds, so they cannot disagree with
	// what a magus built from the same commit trusts.
	active, err := selfupdate.ReleaseKeys.Active()
	if err != nil {
		return err
	}
	if expiresAt == "" {
		expiresAt = time.Now().UTC().Add(IndexValidity).Format(time.RFC3339)
	} else if _, err := time.Parse(time.RFC3339, expiresAt); err != nil {
		return fmt.Errorf("-expires %q is not RFC3339: %w", expiresAt, err)
	}
	data, err := json.Marshal(buildIndex(manifests, active.ID, expiresAt, selfupdate.ReleaseKeys.RevokedIDs()))
	if err != nil {
		return fmt.Errorf("marshal index: %w", err)
	}
	data = append(data, '\n')

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", outDir, err)
	}
	idxPath := filepath.Join(outDir, "index.json")
	if err := os.WriteFile(idxPath, data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", idxPath, err)
	}
	fmt.Printf("wrote %s (%d releases, %d bytes)\n", idxPath, len(manifests), len(data))

	if skipSign {
		return nil
	}

	// An unset key is fatal rather than a warning. A release job that quietly skipped
	// the signature is how index.json.sig came to 404 for every user of v0.3.0; a
	// caller that genuinely wants the unsigned file asks for it with -no-sign.
	keyHex := os.Getenv("MAGUS_SIGNING_KEY")
	if keyHex == "" {
		return fmt.Errorf("MAGUS_SIGNING_KEY is not set; pass -no-sign to write index.json without a signature")
	}
	keyBytes, err := hex.DecodeString(keyHex)
	if err != nil {
		return fmt.Errorf("decode MAGUS_SIGNING_KEY: %w", err)
	}
	if len(keyBytes) != ed25519.PrivateKeySize {
		return fmt.Errorf("MAGUS_SIGNING_KEY must be %d bytes (%d hex chars), got %d bytes",
			ed25519.PrivateKeySize, ed25519.PrivateKeySize*2, len(keyBytes))
	}
	sigPath := idxPath + ".sig"
	if err := os.WriteFile(sigPath, ed25519.Sign(ed25519.PrivateKey(keyBytes), data), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", sigPath, err)
	}

	// Self-check against the key BINARIES carry, not against the one that just signed:
	// that is the only pair whose disagreement strands clients, and it is what
	// release_sign() checks for SHA256SUMS.
	pubKey, err := releaseVerifyKey()
	if err != nil {
		return err
	}
	if err := verifyIndexSigFile(outDir, pubKey); err != nil {
		return err
	}
	fmt.Printf("signed %s -> %s\n", idxPath, sigPath)
	return nil
}

// runGenerateChangelog regenerates CHANGELOG.md from releases/*.yaml, preserving
// the [Unreleased] section verbatim. This is the drift-gate-safe inverse of migration.
//
// Usage:
//
//	magus-utils generate-changelog -releases ./releases -changelog ./CHANGELOG.md
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
	// TrimRight rather than != "": the section a freshly cut release leaves behind is
	// newlines only, and writing those back adds a blank line per release.
	b.WriteString("## [Unreleased]\n")
	if trimmed := strings.TrimRight(unreleased, "\n"); trimmed != "" {
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
// This mirrors the logic of render.buzz's parseChangelog.
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
// For example, "magus_v0.2.0_linux_amd64.tar.gz" becomes "linux/amd64".
func platformFromName(name, version string) string {
	// Strip "magus_<version>_" prefix and ".tar.gz" suffix.
	prefix := "magus_" + version + "_"
	if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, ".tar.gz") {
		return ""
	}
	mid := name[len(prefix) : len(name)-len(".tar.gz")]
	// mid is e.g. "linux_amd64", or "linux_amd64_static" for the marked variant. Strip a
	// trailing variant token first: both variants describe the SAME platform, and without
	// this the SplitN below yields "linux/amd64_static" as the platform string.
	// Both separators, because the scheme changed: releases through v0.3.0 wrote
	// `-static` / `-cgo`, later ones write `_static`. Stripping only the current spelling
	// yields "darwin/arm64-static" as a platform for every asset already published.
	for _, variant := range []string{"_static", "_dynamic", "-static", "-dynamic", "-cgo"} {
		mid = strings.TrimSuffix(mid, variant)
	}
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
