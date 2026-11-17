package guard

import (
	"context"
	"io"

	flaggen "github.com/egladman/magus/cmd/magus/gen"
	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/libs/clikit/output"
	"github.com/egladman/magus/types"
	typesagent "github.com/egladman/magus/types/agent"
)

// HookEnv is what the hook entry takes from the binary that runs it: the facts and the
// openers only a command line holds, because this package cannot import the packages that
// open a workspace. A zero member is a capability the caller lacks, and the code that needs
// it answers as if that lookup found nothing.
type HookEnv struct {
	// Version and Binary are the running build's version and the path of its binary.
	Version    string
	BinaryPath func() (string, error)
	// HookBinaryPath is the path a delegation compares a checkout's binary with. It is
	// BinaryPath outside tests, which place the running binary inside a checkout.
	HookBinaryPath func() (string, error)
	// Config is the loaded configuration.
	Config config.Config
	// FindRoot finds the workspace whose rules the guard enforces, searching from its
	// argument, or from the cwd when that is "".
	FindRoot func(rootOverride string) (string, error)
	// Inspect opens the workspace at root, or the one found from rootOverride when root is "".
	Inspect func(ctx context.Context, rootOverride, root string) (types.WorkspaceRepository, error)
	// CacheDir resolves a workspace's cache directory the way every workspace the command
	// opens does: root's own, or for "" the workspace found from rootOverride.
	CacheDir func(rootOverride string) func(root string) (string, error)
	// LoadedCacheDir resolves root's cache directory from the loaded configuration.
	LoadedCacheDir func(root string) (string, error)
	// LazyWorkspace stands in for the workspace at root, opened through open only when asked.
	LazyWorkspace func(root string, open func(context.Context) (types.WorkspaceRepository, error)) types.Inspector
	// TierRule is the spawn rule every workspace the command opens is given.
	TierRule func(parent, child typesagent.ModelTier) error
	// Trace starts the named phase of the command's startup trace and returns its end.
	Trace func(ctx context.Context, phase string) func()
	// OpenJobs opens the job store of the session checkout at root without writing.
	OpenJobs func(ctx context.Context, root string) (*job.Store, error)
	// StaleIndexes lists the symbol indexes the workspace found from rootOverride holds stale.
	StaleIndexes func(ctx context.Context, rootOverride string) []types.KnowledgeStaleIndex
	// IndexCause diagnoses why the checkout at root has an index missing or behind.
	IndexCause func(ctx context.Context, root string) types.KnowledgeIndexCause
	// Emit renders a verdict in a structured format, or under -o name as its bare decision,
	// to out and wherever the command mirrors its output.
	Emit func(out io.Writer, opts output.Options, decision string, verdict any) error
}

// HookCall is one invocation of the hook as the command line parsed it.
type HookCall struct {
	Flags    flaggen.ShellFlags
	Output   output.Options
	Operands []string
	// Quiet is -q or -s: the exit code is the answer and nothing prints.
	Quiet bool
	// Window names the terminal window a person typed this in, "" for a host's hook.
	Window string
}

// phase starts the named phase of the command's startup trace, or traces nothing.
func (e HookEnv) phase(ctx context.Context, name string) func() {
	if e.Trace == nil {
		return func() {}
	}
	return e.Trace(ctx, name)
}
