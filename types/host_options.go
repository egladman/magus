package types

// The option records the Buzz host doors take. Each crosses as one typed object whose
// mirror and strict decoder are generated from the struct, so a misspelled option is a
// check error in the script and an unknown member raises at the door.

// ExecOptions are proc\exec's options.
type ExecOptions struct {
	// Stdin is fed to the process as its standard input.
	Stdin string
	// Quiet captures the output without echoing it to the console.
	Quiet bool
	// TTY runs the command on a pseudo-terminal, merging stderr into stdout. Unix only.
	TTY bool
	// AllowFailure returns the result of a non-zero exit instead of raising.
	AllowFailure bool
	// TimeoutMs kills the process group once it has run this long and raises, whatever
	// AllowFailure says: a call cut off has no result to hand back. Zero is no bound.
	TimeoutMs float64
	// Detach starts the process in a session of its own with no stdio and returns before
	// it exits, so it outlives the caller. Nothing waits on it, so it takes no TimeoutMs:
	// the child bounds its own work.
	Detach bool
}

// VCSCommandOptions are vcs\cmd's options: proc\exec's, plus Dir, which runs the
// version control binary elsewhere relative to the target's directory.
type VCSCommandOptions struct {
	Dir          string
	Stdin        string
	Quiet        bool
	TTY          bool
	AllowFailure bool
	// TimeoutMs is proc\exec's: past it the binary is killed and the call raises.
	TimeoutMs float64
}

// MagusCommandOptions are the options of every door that runs a nested magus: magus\cmd,
// run, doctor, clean, attention, impact and the forking magus\describe members.
type MagusCommandOptions struct {
	// Root is the global --root workspace the child loads.
	Root string
	// Dir runs the child in another directory, relative to the target's.
	Dir   string
	Stdin string
	// Quiet is forced on by the doors that decode the child's JSON report.
	Quiet        bool
	AllowFailure bool
}

// MagusDiffOptions are magus\diff's options. types.DiffOptions is the in-process
// review's, which takes a parsed baseline; this one names files and a range for the
// nested `magus diff` to read.
type MagusDiffOptions struct {
	MagusCommandOptions
	// From reads a review an earlier `magus diff -o json` saved instead of computing one.
	From string
	// Baseline is the path of a `magus graph export --symbols -o json` of the base.
	Baseline string
	// Rev reviews a committed range written base...head instead of the working tree.
	Rev string
	// Patch reviews a unified diff given as text, fed to the child on stdin.
	Patch string
	// MinCohort and MinShare override the conformance silence gates; zero keeps the
	// configured gate.
	MinCohort int
	MinShare  float64
}

// RaiseOptions are magus\raise's options.
type RaiseOptions struct {
	// Cause is the error being wrapped, usually the value an inner catch bound.
	Cause any
	// URL is the page documenting the code.
	URL string
}

// SkillsOptions are magus\skills' options.
type SkillsOptions struct {
	// Name selects one skill; empty lists every one.
	Name string
	// Form narrows the shipped entries; none is both.
	Form SkillForm
}

// HTTPOptions are the curl-style options of http\get, post, request and download.
type HTTPOptions struct {
	// Fail turns a status of 400 or more into an error, like curl --fail.
	Fail bool
	// FailWithBody is Fail with the body kept in the error.
	FailWithBody bool
	// FailEarly never retries an HTTP error status.
	FailEarly bool
	// TimeoutSeconds bounds the request including retries, like curl --max-time.
	// Zero is the 30 second default.
	TimeoutSeconds float64
}

// HTTPServerOptions are http\server's options. Exactly one of Dir and Mounts is set.
type HTTPServerOptions struct {
	// Dir serves one directory.
	Dir string
	// Mounts maps URL prefixes to directories; the longest matching prefix serves a
	// request, with the prefix stripped.
	Mounts map[string]string
	// Port binds that port; zero scans upward from 8080.
	Port int
}

// ArchiveOptions are the archive module's options. Each method reads the knobs that apply
// to it.
type ArchiveOptions struct {
	// Format overrides detection from the destination's extension (compress).
	Format string
	// Strip drops leading path components (uncompress).
	Strip int
	// MaxSize caps the bytes written or read; zero is 10 GiB.
	MaxSize int
	// Threads is the parallel worker count; zero picks one.
	Threads int
	// Level is the compression level; null is the format's default (compress).
	Level *int
	// FollowSymlinks archives what a symlink points at (compress).
	FollowSymlinks bool
}

// RetryOptions are os\retry's options, in milliseconds. Null takes the default.
type RetryOptions struct {
	// BackoffMs is the first pause; the default is 500.
	BackoffMs *float64
	// MaxBackoffMs caps the pause; the default is 30000.
	MaxBackoffMs *float64
}
