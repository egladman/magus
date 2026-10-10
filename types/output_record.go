package types

import "context"

// OutputRecord is one target run's captured log, the value magus\output returns.
type OutputRecord struct {
	Ref        string `json:"ref"         yaml:"ref"`
	Project    string `json:"project"     yaml:"project"`
	Target     string `json:"target"      yaml:"target"`
	Failed     bool   `json:"failed"      yaml:"failed"`
	DurationMs int64  `json:"duration_ms" yaml:"duration_ms"`
	Output     string `json:"output"      yaml:"output"`
}

type outputCacheDirKey struct{}

// WithOutputCacheDir pins the cache directory whose output store magus\output reads when
// ctx carries no workspace. The guard pins the checkout it judges, so a guard rule can
// look up the runs a command cites without gaining any other workspace member.
func WithOutputCacheDir(ctx context.Context, cacheDir string) context.Context {
	return context.WithValue(ctx, outputCacheDirKey{}, cacheDir)
}

// OutputCacheDirFromContext returns the cache directory WithOutputCacheDir pinned, "" when
// none is.
func OutputCacheDirFromContext(ctx context.Context) string {
	dir, _ := ctx.Value(outputCacheDirKey{}).(string)
	return dir
}

// StoredOutputSchemaVersion is the newest [StoredOutput] schema this build writes and
// reads; a higher one is refused.
const StoredOutputSchemaVersion = 1

// StoredOutput is one stored run as `magus query output <ref> -o json|jsonl` writes it;
// the output rides in Output when it is valid UTF-8 and in OutputBase64 otherwise.
type StoredOutput struct {
	Schema
	OutputDescriptor
	// ClassDigests are the run's per-class cache key digests, as `--identity` prints them.
	ClassDigests []ClassDigest `json:"class_digests,omitempty"`
	Output       string        `json:"output"`
	OutputBase64 []byte        `json:"output_base64,omitempty"`
}

// Bytes returns the captured output exactly as the record carries it.
func (r StoredOutput) Bytes() []byte {
	if r.OutputBase64 != nil {
		return r.OutputBase64
	}
	return []byte(r.Output)
}

// OutputDescriptor is a stored target execution's identity and outcome, the metadata
// behind a target-output ref. Its json names match cache.OutputDescriptor's.
type OutputDescriptor struct {
	Ref         string `json:"ref"`
	Project     string `json:"project"`
	Target      string `json:"target,omitempty"`
	Inv         string `json:"inv,omitempty"` // invocation id of the run that produced this output
	Failed      bool   `json:"failed"`
	ErrMsg      string `json:"error,omitempty"` // failure message; empty on success
	TimestampMs int64  `json:"timestamp_ms"`    // unix milliseconds
	DurationMs  int64  `json:"duration_ms"`

	Key          string `json:"key,omitempty"`         // full cache key hash (64 hex)
	KeyVersion   int    `json:"key_version,omitempty"` // key recipe version that produced Key
	Attempt      string `json:"attempt,omitempty"`     // execution-unique id
	MagusVersion string `json:"magus_version,omitempty"`

	Revision string `json:"revision,omitempty"` // full VCS revision inputs were read at; "" when unknown
	Dirty    bool   `json:"dirty,omitempty"`    // working tree had uncommitted changes at capture time

	Spell     string   `json:"spell,omitempty"`      // spell::op filter that selected the definition
	ExtraArgs []string `json:"extra_args,omitempty"` // trailing args forwarded after --
	VCSName   string   `json:"vcs,omitempty"`        // provider Revision came from: git, hg, sl, jj
	Platform  string   `json:"platform,omitempty"`   // GOOS/GOARCH the run executed on
}

// ClassDigest summarizes one component class of a cache key: every key input shares a
// label prefix ("src", "env", "tool", ...), and the digest hashes the class's lines in
// key order. Comparing digests names WHICH class disagrees without shipping the lines.
type ClassDigest struct {
	Class  string `json:"class"`
	Digest string `json:"digest"` // sha256 over the class's lines, truncated to 12 hex
	Count  int    `json:"count"`  // how many key inputs the class contributes
}
