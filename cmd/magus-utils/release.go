package main

import (
	"bufio"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"errors"
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
// Schema version 1 fields; additive changes only: do not remove or rename.
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
	Added      []string `yaml:"added,omitempty"      json:"added,omitempty"`
	Changed    []string `yaml:"changed,omitempty"    json:"changed,omitempty"`
	Deprecated []string `yaml:"deprecated,omitempty" json:"deprecated,omitempty"`
	Removed    []string `yaml:"removed,omitempty"    json:"removed,omitempty"`
	Fixed      []string `yaml:"fixed,omitempty"      json:"fixed,omitempty"`
	Security   []string `yaml:"security,omitempty"   json:"security,omitempty"`
}

// changelogSections is Keep a Changelog 1.1.0's section set, in the order it lists them.
var changelogSections = []string{"Added", "Changed", "Deprecated", "Removed", "Fixed", "Security"}

// changelogEntryWordCap bounds one entry. An entry states the change and why a reader
// cares; the reasoning behind it belongs in the diff, the PR or an ADR.
const changelogEntryWordCap = 60

// lintUnreleased reports every way an Unreleased body departs from the changelog formula,
// one line per violation, empty when it conforms.
//
// The formula is Keep a Changelog plus this repo's entry shape: sections from
// changelogSections, in that order, each once; every entry a `- **headline**` sentence
// that ends in a period, continued on lines indented two spaces, within
// changelogEntryWordCap words. It is enforced rather than described because notesFromBody
// DROPS what it cannot place: an unknown heading or a stray line vanished from the
// release notes without a word.
func lintUnreleased(body string) []string {
	var problems []string
	last := -1
	inSection := false
	var entry strings.Builder
	entryLine := 0
	finish := func() {
		if entryLine == 0 {
			return
		}
		text := strings.TrimSpace(entry.String())
		if !strings.HasPrefix(text, "**") || strings.Count(text, "**") < 2 {
			problems = append(problems, fmt.Sprintf("line %d: an entry opens with a **bold headline**", entryLine))
		}
		// A headline-only entry closes its sentence inside the bold.
		if !strings.HasSuffix(text, ".") && !strings.HasSuffix(text, ".**") {
			problems = append(problems, fmt.Sprintf("line %d: an entry ends with a period", entryLine))
		}
		if n := len(strings.Fields(text)); n > changelogEntryWordCap {
			problems = append(problems, fmt.Sprintf("line %d: entry is %d words; the cap is %d", entryLine, n, changelogEntryWordCap))
		}
		entry.Reset()
		entryLine = 0
	}
	for i, line := range strings.Split(body, "\n") {
		n := i + 1
		switch {
		case strings.TrimSpace(line) == "":
			finish()
		case strings.HasPrefix(line, "### "):
			finish()
			inSection = true
			name := strings.TrimSpace(line[4:])
			idx := slices.Index(changelogSections, name)
			switch {
			case idx < 0:
				problems = append(problems, fmt.Sprintf("line %d: %q is not a Keep a Changelog section (%s)", n, name, strings.Join(changelogSections, ", ")))
			case idx <= last:
				problems = append(problems, fmt.Sprintf("line %d: %q is out of order or repeated; the order is %s", n, name, strings.Join(changelogSections, ", ")))
			default:
				last = idx
			}
		case strings.HasPrefix(line, "- "):
			finish()
			if !inSection {
				problems = append(problems, fmt.Sprintf("line %d: an entry sits under a section heading", n))
			}
			entryLine = n
			entry.WriteString(strings.TrimPrefix(line, "- "))
		case strings.HasPrefix(line, "  ") && entryLine > 0:
			entry.WriteString(" " + strings.TrimSpace(line))
		default:
			finish()
			problems = append(problems, fmt.Sprintf("line %d: not a heading, an entry, or an entry's two-space continuation", n))
		}
	}
	finish()
	return problems
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
// artifacts a client can name and pin. The manifest's prose (date, notes, body)
// is deliberately absent. Nothing reads it here, it ships in the Atom feed and in
// the release YAML, and every byte of this file is covered by index.json.sig.
// omitzero, not omitempty: these bytes are signed, so they must not depend on how
// the binary that wrote them was built. Under GOEXPERIMENT=jsonv2, omitempty means
// "empty JSON value" and false is not one, so the same struct encodes to
// `"yanked":false` there and to nothing under v1, two different signatures for one
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

// runCut folds the changelog fragments in unreleasedDir into a
// releases/v<version>.yaml manifest, alongside the size and SHA-256 of every
// artifact in artifactsDir, and deletes the fragments it folded: the manifest owns
// that text now, and CHANGELOG.md is generated back out of the manifests.
//
// Usage: magus-utils cut -version v0.2.0 -artifacts ./dist -unreleased ./changes/unreleased -out ./releases
//
// The MAGUS_SIGNING_KEY env var is NOT required here; signing SHA256SUMS is a
// separate step (magus-utils sign). The manifest itself is not signed; only
// index.json is signed (by runReleaseIndex).
func runCut(args []string) error {
	// Simple flag parsing without flag package to avoid import bloat.
	var version, artifactsDir, unreleasedDir, outDir string
	for i := 0; i < len(args)-1; i++ {
		switch args[i] {
		case "-version":
			version = args[i+1]
			i++
		case "-artifacts":
			artifactsDir = args[i+1]
			i++
		case "-unreleased":
			unreleasedDir = args[i+1]
			i++
		case "-out":
			outDir = args[i+1]
			i++
		}
	}
	if version == "" || artifactsDir == "" || unreleasedDir == "" || outDir == "" {
		return fmt.Errorf("usage: magus-utils cut -version v0.2.0 -artifacts ./dist -unreleased ./changes/unreleased -out ./releases")
	}

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", outDir, err)
	}
	outPath := filepath.Join(outDir, version+".yaml")

	artifacts, err := scanReleaseArtifacts(artifactsDir, version)
	if err != nil {
		return err
	}
	frags, err := readFragments(unreleasedDir)
	if err != nil {
		return fmt.Errorf("read fragments: %w", err)
	}

	// Ordering matters: the artifacts are hashed before the fragments are judged, so an
	// already-cut version is recognised without consuming anything. On a rerun the
	// fragments are gone (this call deleted them), and the check below would otherwise
	// fail first, reporting no notes when the work is simply already done.
	//
	// A rerun whose manifest names exactly these artifacts converges, because a publish
	// job can fail at any later step and must be resumable. DIFFERENT bytes under a
	// shipped tag stay a conflict.
	if cut, err := os.ReadFile(outPath); err == nil {
		var prev ReleaseManifest
		if err := yaml.Unmarshal(cut, &prev); err != nil {
			// Unreadable is not absent: convergence has to compare what is there, and a
			// file that will not parse is refused rather than overwritten.
			return fmt.Errorf("%s already exists but does not parse as a manifest, so this cut cannot "+
				"tell a rerun from a rebuild: %w", outPath, err)
		}
		if diff := artifactDiff(prev.Artifacts, artifacts); diff != "" {
			return fmt.Errorf("%s already exists and names different artifacts; release manifests are "+
				"immutable once committed, so this is a rebuild under a shipped tag rather than a rerun:\n%s",
				outPath, diff)
		}
		// A fragment the manifest already carries is one an interrupted cut folded but
		// did not delete.
		var folded []fragment
		for _, f := range frags {
			if strings.Contains(prev.Body, f.entry) {
				folded = append(folded, f)
			}
		}
		if err := removeFragments(folded); err != nil {
			return err
		}
		fmt.Printf("%s already names these %d artifact(s); nothing to cut\n", outPath, len(artifacts))
		return nil
	}

	if len(frags) == 0 {
		return fmt.Errorf("%s holds no changelog fragments, and %s does not exist. "+
			"An earlier run consumed the fragments without leaving the manifest behind; recover them from that "+
			"run's checkout or from git history and cut again", unreleasedDir, outPath)
	}
	body := renderUnreleased(frags)
	m := ReleaseManifest{
		Version:   version,
		Date:      time.Now().UTC().Format("2006-01-02"),
		Notes:     notesFromBodyString(body),
		Body:      body,
		Artifacts: artifacts,
	}

	out, err := yaml.Marshal(m)
	if err != nil {
		return fmt.Errorf("marshal manifest: %w", err)
	}

	// The manifest lands before any fragment is deleted, so no failure destroys notes
	// a manifest never got, and a rerun finishes an interrupted deletion (above).
	tmpPath := outPath + ".tmp"
	if err := os.WriteFile(tmpPath, out, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", tmpPath, err)
	}
	if err := os.Rename(tmpPath, outPath); err != nil {
		return fmt.Errorf("write %s: %w", outPath, err)
	}
	fmt.Printf("wrote %s from %d fragment(s)\n", outPath, len(frags))
	return removeFragments(frags)
}

// removeFragments deletes folded fragments, reporting every failure.
func removeFragments(frags []fragment) error {
	var errs []error
	for _, f := range frags {
		if err := os.Remove(f.path); err != nil {
			errs = append(errs, err)
		}
	}
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("delete folded fragments; rerun cut to finish: %w", err)
	}
	return nil
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
// docs/gen/public/release/; the site render copies that directory out verbatim
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

// runGenerateChangelog writes a changelog from releases/*.yaml. [Unreleased] is
// empty unless -unreleased names the fragment directory to render into it: the
// committed CHANGELOG.md is written without, so no pull request changes it, and the
// docs page with, so readers still see what is coming. `-changelog -` writes to stdout.
//
// Without -unreleased it refuses to overwrite a changelog whose [Unreleased] holds
// entries, since regenerating would delete them; they belong in fragments.
//
// Usage:
//
//	magus-utils generate-changelog -releases ./releases -changelog ./CHANGELOG.md [-unreleased ./changes/unreleased]
func runGenerateChangelog(args []string) error {
	var releasesDir, changelogPath, unreleasedDir string
	for i := 0; i < len(args)-1; i++ {
		switch args[i] {
		case "-releases":
			releasesDir = args[i+1]
			i++
		case "-changelog":
			changelogPath = args[i+1]
			i++
		case "-unreleased":
			unreleasedDir = args[i+1]
			i++
		}
	}
	if releasesDir == "" || changelogPath == "" {
		return fmt.Errorf("usage: magus-utils generate-changelog -releases ./releases -changelog ./CHANGELOG.md [-unreleased ./changes/unreleased]")
	}

	var unreleased string
	if unreleasedDir != "" {
		frags, err := readFragments(unreleasedDir)
		if err != nil {
			return fmt.Errorf("read fragments: %w", err)
		}
		unreleased = renderUnreleased(frags)
	} else if changelogPath != "-" {
		written, err := readUnreleasedSection(changelogPath)
		if err != nil {
			return fmt.Errorf("read unreleased: %w", err)
		}
		if strings.TrimSpace(written) != "" {
			return fmt.Errorf("%s has entries under [Unreleased]; move each into its own fragment under "+
				"changes/unreleased/ (see changes/README.md), since regenerating would delete them", changelogPath)
		}
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
	b.WriteString("Entries for the next release wait as one file each under `changes/unreleased/`.\n")
	b.WriteString("\n")
	b.WriteString("## [Unreleased]\n")
	if unreleased != "" {
		b.WriteString("\n")
		b.WriteString(unreleased)
		b.WriteString("\n")
	}
	// Released sections: generated from manifests (newest first).
	// Format: blank line + `"## [version] - date"` + blank line + body.
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

	if changelogPath == "-" {
		_, err := os.Stdout.WriteString(b.String())
		return err
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

// readUnreleasedSection returns the body of the [Unreleased] section (everything
// after the `## [Unreleased]` heading, up to the next `## ` heading), with a
// leading newline if non-empty. generate-changelog reads it to refuse deleting
// hand-written entries.
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
		case "deprecated":
			notes.Deprecated = append(notes.Deprecated, items...)
		case "removed":
			notes.Removed = append(notes.Removed, items...)
		case "fixed":
			notes.Fixed = append(notes.Fixed, items...)
		case "security":
			notes.Security = append(notes.Security, items...)
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

// scanReleaseArtifacts hashes every release asset in dir, in name order so two scans of
// one directory produce the same list and can be compared.
func scanReleaseArtifacts(dir, version string) ([]ReleaseArtifact, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read artifacts dir %s: %w", dir, err)
	}
	var artifacts []ReleaseArtifact
	for _, e := range entries {
		if e.IsDir() || !isReleaseAsset(e.Name()) {
			continue
		}
		size, digest, err := fileSizeAndSHA256(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, fmt.Errorf("hash %s: %w", e.Name(), err)
		}
		artifacts = append(artifacts, ReleaseArtifact{
			Name:     e.Name(),
			Platform: platformFromName(e.Name(), version),
			Size:     fmt.Sprintf("%d", size),
			SHA256:   digest,
		})
	}
	// An empty artifacts list means the directory contained no release assets, which is
	// almost certainly a path mistake rather than a valid hollow release.
	//
	// magus-release.pem used to be appended here, sizeless and hashless. No release
	// since v0.1.0 has published such an asset (release.yaml uploads the tarballs and
	// the SHA256SUMS pair, nothing else), so the entry named a download that 404s, in a
	// file whose whole purpose is telling a client what it may fetch.
	if len(artifacts) == 0 {
		return nil, fmt.Errorf("no release artifacts found in %s (expected *.tar.gz or SHA256SUMS)", dir)
	}
	slices.SortFunc(artifacts, func(a, b ReleaseArtifact) int { return strings.Compare(a.Name, b.Name) })
	return artifacts, nil
}

// artifactDiff describes how a committed manifest's artifacts differ from a fresh scan,
// or "" when they name the same bytes. It reports every disagreement rather than the
// first, because one mismatched digest and all of them mean different things.
//
// Only the identity fields are compared: Date moves on a rerun and says nothing about the
// bytes, and the notes cannot be recomputed once the fragments are deleted.
func artifactDiff(committed, scanned []ReleaseArtifact) string {
	key := func(xs []ReleaseArtifact) map[string]ReleaseArtifact {
		m := make(map[string]ReleaseArtifact, len(xs))
		for _, a := range xs {
			m[a.Name] = a
		}
		return m
	}
	was, now := key(committed), key(scanned)
	var diffs []string
	for name, a := range was {
		b, ok := now[name]
		if !ok {
			diffs = append(diffs, fmt.Sprintf("  %s: in the manifest, absent from the build", name))
			continue
		}
		if a.SHA256 != b.SHA256 {
			diffs = append(diffs, fmt.Sprintf("  %s: sha256 %s -> %s", name, a.SHA256, b.SHA256))
		}
		if a.Size != b.Size {
			diffs = append(diffs, fmt.Sprintf("  %s: size %s -> %s", name, a.Size, b.Size))
		}
	}
	for name := range now {
		if _, ok := was[name]; !ok {
			diffs = append(diffs, fmt.Sprintf("  %s: built now, absent from the manifest", name))
		}
	}
	slices.Sort(diffs)
	return strings.Join(diffs, "\n")
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
