package types

// The records a provider op takes and returns, mirrored under their Go names in magus/spell
// so a spell declares `export fun <op>(target: Target, req: <Req>) > <Resp>`.
// cmd/magus-utils/provider_ops.go lists which op takes which.

// Empty is the request of an op that needs nothing but its Target.
type Empty struct{}

// Ack is a provider's yes or no. What false means is the op's to say: a cache miss for
// get_artifact, an artifact already stored for put_artifact.
type Ack struct {
	OK bool
}

// SecretRef is what resolve_secret is asked for: a reference in the provider's own
// notation, passed through verbatim.
type SecretRef struct {
	Ref string
}

// GreenRun is what last_green_run answers: the provider's id for the newest run that
// passed, and the commit it ran. Either empty is "no green run".
type GreenRun struct {
	Run    string
	Commit string
}

// Strings is a list of strings returned as a record, so the op returns an object like
// every other op.
type Strings struct {
	Items []string
}

// CIGroup opens a fold in a CI log.
type CIGroup struct {
	ID        string
	Title     string
	Collapsed bool
}

// CIGroupEnd closes the fold the CIGroup with the same ID opened.
type CIGroupEnd struct {
	ID string
}

// CIAnnotation is one notice a CI provider raises against a file and line. Level is
// "error", "warning" or "notice"; a zero position is no position.
type CIAnnotation struct {
	Level   string
	Message string
	Title   string
	Code    string
	File    string
	Line    int
	EndLine int
	Col     int
	EndCol  int
}

// ArtifactRequest names one cache artifact: the project it belongs to and its key. Path is
// the local file the op reads (put_artifact) or writes (get_artifact); has_artifact gets
// none.
type ArtifactRequest struct {
	Project string
	Hash    string
	Path    string
}

// PruneRequest is a remote cache's retention policy: evict what is older than
// OlderThanSecs, keep the newest KeepLast, and change nothing when DryRun is set.
type PruneRequest struct {
	OlderThanSecs int64
	KeepLast      int64
	DryRun        bool
}

// PruneResult reports whether a prune sweep completed.
type PruneResult struct {
	Done bool
}

// LifecycleQuery is every lifecycle key list_lifecycles is asked about, in one call.
type LifecycleQuery struct {
	Keys []string
}
