// Package evidence reads what the workspace and its knowledge graph say about a job
// row: the graph a command loads once, the boundary a lease derives, the gates a
// verification grades and the diagrams of a plan.
//
// It sits beside internal/job rather than in it, because cache, guard, bindings and
// workspace all import job, and the knowledge and deps tests import it too. What the
// command line owns arrives in an [Env] or a [CallersEnv].
package evidence

import (
	"context"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/internal/job"
	jobtypes "github.com/egladman/magus/libs/safetyscissors/job"
	"github.com/egladman/magus/types"
)

// Env is what the caller supplies: the loaded config, and the loaders that live with the
// command line. A field a read never reaches may stay nil.
type Env struct {
	Config  config.Config
	Version string
	// Options are what every workspace the CLI opens is given.
	Options []magus.Option
	// LoadMagus opens the workspace at root.
	LoadMagus func(ctx context.Context, root string) (*magus.Magus, error)
	// RootStale and FindStale judge the stale symbol indexes of the workspace at root and
	// of an opened one.
	RootStale func(ctx context.Context, root string) []types.KnowledgeStaleIndex
	FindStale func(ctx context.Context, m *magus.Magus) []types.KnowledgeStaleIndex
	// RootRefsGraph and RootDomainGraph load the symbol graph and the domain graph of the
	// workspace at root; RefsGraph and DomainGraph do the same for an opened one.
	RootRefsGraph   func(ctx context.Context, root string) (*knowledge.Graph, error)
	RootDomainGraph func(ctx context.Context, root string) (*knowledge.Graph, error)
	RefsGraph       func(ctx context.Context, m *magus.Magus) (*knowledge.Graph, error)
	DomainGraph     func(ctx context.Context, m *magus.Magus) (*knowledge.Graph, error)
	// Notes are the regenerated outputs and unrun gates of row in m.
	Notes func(ctx context.Context, m *magus.Magus, row jobtypes.Job) ([]job.RegeneratedOutput, []job.UnrunGate)
	// HolderCell names a row's holder in a plan diagram.
	HolderCell func(row jobtypes.Job) string
	// GateMergeScanLimit is how many commits the green-gate scan reads for a merge.
	GateMergeScanLimit int
}
