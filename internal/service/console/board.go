package console

import (
	"errors"

	"github.com/egladman/magus/types"
)

// ErrNoWorkspace is what a service built without a *magus.Magus answers for anything that
// reads the workspace (the matching seams unset too). The server builds one to serve status
// while its workspace fails to load; a route that reads it answers 503, since "no workspace
// yet" is a state the console renders rather than a fault it reports.
var ErrNoWorkspace = errors.New("console: no workspace available")

// Board is the console's one-poll assembly of history metrics for the dashboard JSON
// and Connect response. It is not a types/ umbrella: each field is a domain metric.
// Pointer fields stay nil when their source was never recorded or no workspace is held.
type Board struct {
	Hotspots    types.Hotspots     `json:"hotspots"    yaml:"hotspots"`
	Affinity    types.Affinity     `json:"affinity"    yaml:"affinity"`
	Ownership   types.Ownership    `json:"ownership"   yaml:"ownership"`
	Trend       types.Trend        `json:"trend"       yaml:"trend"`
	Volatility  *types.Volatility  `json:"volatility"  yaml:"volatility"`
	ModelTiers  *types.ModelTiers  `json:"model_tiers" yaml:"model_tiers"`
	Conformance *types.Conformance `json:"conformance" yaml:"conformance"`
}
