package main

import (
	"context"
	"fmt"
	"io"

	"github.com/egladman/magus/internal/interactive/tty"
	"github.com/egladman/magus/libs/cmdline/output"
)

// outputSCIP is graph export's SCIP protobuf index, for code-intelligence tools.
const outputSCIP output.Format = "scip"

// graphExportSCIP writes the workspace's symbol indexes to out as one SCIP index, judging
// out a terminal by probe. --select narrows the knowledge graph, which this export does
// not carry, so it is refused rather than ignored.
func graphExportSCIP(ctx context.Context, root string, out io.Writer, probe tty.Probe, selected bool) error {
	if selected {
		return fmt.Errorf("-o %s exports the whole symbol index, and --select narrows the knowledge graph, which it does not carry: drop --select", outputSCIP)
	}
	if tty.IsTerminalWriter(out, probe) {
		return fmt.Errorf("-o %[1]s writes a binary SCIP index: redirect it to a file, as in magus graph export -o %[1]s > index.scip", outputSCIP)
	}
	m, err := loadMagus(ctx, root)
	if err != nil {
		return err
	}
	return m.ExportSCIP(ctx, out)
}
