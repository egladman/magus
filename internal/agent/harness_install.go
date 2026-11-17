package agent

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"slices"

	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/log/attr"
	"github.com/egladman/magus/internal/sessions"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/types"
)

// HarnessInstallOutput is the record `magus agent harness install` emits under -o.
type HarnessInstallOutput struct {
	DryRun    bool                   `json:"dry_run"`
	Harnesses []HarnessInstallResult `json:"harnesses"`
}

// HarnessInstallResult is what installing one harness wrote or pruned.
type HarnessInstallResult struct {
	ID              string   `json:"id"`
	Skills          []string `json:"skills"`
	Removed         []string `json:"removed"`
	Tools           []string `json:"tools"`
	Files           []string `json:"files"`
	RestartRequired bool     `json:"restart_required"`
}

// HarnessInstallOptions carries what the caller knows and the installer does not.
type HarnessInstallOptions struct {
	DryRun bool
	// Text prints each skill path and removal on stdout as it goes.
	Text bool
	// Version is the binary version the write is recorded under.
	Version string
}

// HarnessLinksOutput is the record `magus agent harness link` emits under -o.
type HarnessLinksOutput struct {
	Files []string `json:"files"`
}

const harnessLinksRequestLimit = 1 << 20

type harnessLinksRequest struct {
	Links map[string]string `json:"links"`
}

// ReadHarnessLinksRequest decodes the destination-to-source map `harness link` reads from stdin.
func ReadHarnessLinksRequest(r io.Reader) (map[string]string, error) {
	data, err := io.ReadAll(io.LimitReader(r, harnessLinksRequestLimit+1))
	if err != nil {
		return nil, fmt.Errorf("read stdin: %w", err)
	}
	if len(data) > harnessLinksRequestLimit {
		return nil, fmt.Errorf("request exceeds %d bytes", harnessLinksRequestLimit)
	}
	var request harnessLinksRequest
	if err := json.UnmarshalStrict(data, &request); err != nil {
		return nil, fmt.Errorf("decode request: %w", err)
	}
	if request.Links == nil {
		return nil, fmt.Errorf("request must contain a links object")
	}
	return request.Links, nil
}

// InstallHarness writes one harness's skills, tool rows and planned files.
func (c *Catalog) InstallHarness(ctx context.Context, root, id string, plan types.HarnessInstallation, opts HarnessInstallOptions) (HarnessInstallResult, error) {
	result := HarnessInstallResult{ID: id, Skills: []string{}, Removed: []string{}}
	if err := c.installHarnessSkills(ctx, root, &result, opts); err != nil {
		return result, err
	}
	changed, err := InstallHarnessTools(ctx, root, id, opts.DryRun)
	if err != nil {
		return result, fmt.Errorf("magus agent harness install: %w", err)
	}
	result.Tools = NonNil(changed)
	installed, err := ApplyHarnessInstallation(ctx, root, plan, opts.DryRun)
	if err != nil {
		return result, fmt.Errorf("magus agent harness install: %w", err)
	}
	result.Files = NonNil(installed)
	result.RestartRequired = plan.Guard == "plugin" && len(installed) > 0
	if !opts.DryRun {
		paths := slices.Concat(result.Skills, result.Removed, result.Tools, result.Files)
		RecordHarnessWrite(ctx, root, "agent harness install", opts.Version, paths)
	}
	return result, nil
}

// RecordHarnessWrite files the paths a harness command wrote. A store error
// is logged, since the write itself succeeded.
func RecordHarnessWrite(ctx context.Context, root, command, version string, paths []string) {
	if len(paths) == 0 {
		return
	}
	dir, err := sessions.Dir(root)
	if err == nil {
		err = sessions.RecordHarnessWrite(dir, sessions.HarnessWrite{Paths: paths},
			sessions.InvocationStart{Origin: trail.LocalOrigin(ctx), Workspace: root, Command: command, Version: version})
	}
	if err != nil {
		slog.With(attr.Component(command)).WarnContext(ctx, "harness write not recorded", attr.Error(err))
	}
}

func (c *Catalog) installHarnessSkills(ctx context.Context, root string, result *HarnessInstallResult, opts HarnessInstallOptions) error {
	skills, err := LoadHarnessSkills(ctx, root, result.ID)
	if err != nil {
		return fmt.Errorf("magus agent harness install: %w", err)
	}
	for _, path := range skills.Paths {
		written, removed, err := c.installHarnessSkillPath(ctx, root, path, skills.Form, opts)
		if err != nil {
			return err
		}
		result.Skills = append(result.Skills, written...)
		result.Removed = append(result.Removed, removed...)
	}
	return nil
}

// installHarnessSkillPath writes one skill tree and prunes what this binary no
// longer ships, reporting both. It overwrites without asking and prunes without
// asking, so what it writes and what it deletes is the only thing standing
// between a person and a skill that left without being named. Same wording as
// `magus agent install`, which reports the same two lists.
func (c *Catalog) installHarnessSkillPath(ctx context.Context, root, path string, form types.SkillForm, opts HarnessInstallOptions) (written, removed []string, err error) {
	if opts.DryRun {
		planned, err := c.PlanSkillTree(root, path, form)
		if err != nil {
			return nil, nil, err
		}
		stale, err := c.StaleSkillDirs(root, path, form)
		if err != nil {
			return nil, nil, err
		}
		if opts.Text {
			for _, file := range planned {
				fmt.Fprintln(os.Stdout, file)
			}
			ReportRemovedSkills(os.Stdout, stale, true)
		}
		return planned, stale, nil
	}
	written, changed, err := c.WriteSkillTree(root, path, true, form)
	if err != nil {
		return nil, nil, err
	}
	// Logged per file, so the line says whether this install moved that file's bytes.
	// A reinstall that rewrites thirty unchanged files and one real update read the same
	// thirty-one ways before.
	altered := make(map[string]bool, len(changed))
	for _, file := range changed {
		altered[file] = true
	}
	for _, file := range written {
		if !altered[file] {
			slog.With(attr.Component("agent harness install")).DebugContext(ctx, "already current", slog.String("path", file))
			continue
		}
		slog.With(attr.Component("agent harness install")).InfoContext(ctx, "wrote", slog.String("path", file))
	}
	removed, err = c.PruneSkillTree(root, path, form)
	if err != nil {
		return nil, nil, err
	}
	if opts.Text {
		ReportRemovedSkills(os.Stdout, removed, false)
	}
	return written, removed, nil
}

// PrintHarnessInstall says on stdout what installing one harness wrote.
func PrintHarnessInstall(w io.Writer, result HarnessInstallResult, dryRun bool) {
	installVerb, verb := "installed", "wrote"
	if dryRun {
		installVerb, verb = "would install", "would write"
	}
	for _, path := range result.Files {
		fmt.Fprintf(w, "%s %s: %s\n", installVerb, result.ID, path)
	}
	if result.RestartRequired {
		fmt.Fprintf(w, "restart %s to load the installed runtime\n", result.ID)
	}
	for _, p := range result.Tools {
		fmt.Fprintf(w, "%s %s tool rows: %s\n", verb, result.ID, p)
	}
}
