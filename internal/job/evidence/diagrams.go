package evidence

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/interp/bindings"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/workspace"
	"github.com/egladman/magus/libs/diagram"
	buzz "github.com/egladman/magus/libs/gopherbuzz"
	buzzstd "github.com/egladman/magus/libs/gopherbuzz/std"
	jobtypes "github.com/egladman/magus/libs/safetyscissors/job"
)

// PlanSVGDir is dir, else the job-graph folder of the cache.
func PlanSVGDir(root, dir string, cfg config.Config) (string, error) {
	if dir == "" {
		cacheDir, err := magus.ResolveCacheDir(root, workspace.WithLoadedConfig(cfg))
		if err != nil {
			return "", err
		}
		dir = filepath.Join(cacheDir, "job-graph")
	}
	return filepath.Abs(dir)
}

// drawPlanDiagram paints rec in a session of its own, as Draw needs.
func drawPlanDiagram(ctx context.Context, rec diagram.Diagram) (string, error) {
	sess := buzz.NewSession(ctx, buzz.WithEmbedded())
	defer sess.Close()
	buzzstd.RegisterWithOutput(sess, io.Discard)
	if err := bindings.RegisterMagusRecordTypes(sess); err != nil {
		return "", err
	}
	diagram.DeclareModules(sess)
	svg, err := diagram.Draw(ctx, sess, rec, "")
	if err != nil {
		return "", err
	}
	return diagram.Standalone(svg), nil
}

// WritePlanSVGs writes each diagram to dir as <id>.svg, naming each holder with holder. It
// draws every diagram before writing any, so a refusal writes nothing.
func WritePlanSVGs(ctx context.Context, p *job.Plan, figs []*job.PlanDiagram, dir string, holder func(jobtypes.Job) string) ([]string, error) {
	svgs := make([]string, len(figs))
	for i, f := range figs {
		svg, err := drawPlanDiagram(ctx, p.DiagramRecord(f, holder))
		if err != nil {
			return nil, fmt.Errorf("magus job graph: draw %s: %w", f.ID, err)
		}
		svgs[i] = svg
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("magus job graph: %w", err)
	}
	paths := make([]string, len(figs))
	for i, f := range figs {
		paths[i] = filepath.Join(dir, f.ID+".svg")
		if err := os.WriteFile(paths[i], []byte(svgs[i]), 0o644); err != nil {
			return nil, fmt.Errorf("magus job graph: %w", err)
		}
	}
	return paths, nil
}
