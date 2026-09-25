//go:build !wasm

package std

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/report"
	"github.com/egladman/magus/types"
)

//go:generate go run ../cmd/magus-utils bindings -module pipe -lang buzz -out ../internal/interp/bindings/gen/pipe.go

func init() { Register(Pipe) }

// Pipe is the "pipe" host module: a `magus buzz` script as a stage of a shell pipe of
// magus processes, between a stage that writes records and one that reads them.
//
// A magus stage whose stdout another magus reads writes the records -o jsonl writes
// there, so a script reads the run before it as typed values rather than scraping its
// prose, and whatever it emits is read the same way by the stage after it. A run that
// names no projects runs on the projects the run.scope records it reads name.
var Pipe = Module{
	Name: "pipe",
	// One short line with no ". ": cmd/magus-docs derives the page description from it.
	Doc: "Read the records a magus stage upstream in a pipe writes, and write records for the stage downstream.",
	Methods: []Method{
		{
			Name: "more",
			Doc: "Report whether another record is coming from the magus stage writing this script's stdin, " +
				"waiting for it or for that stage to end. Errors when no magus stage writing records feeds this script.",
			Returns: []Ret{{Type: TypeBool}},
			Raises:  true,
			Impl:    PipeMore,
		},
		{
			Name: "next",
			Doc: "Return the next record from the magus stage writing this script's stdin, waiting for it. " +
				"Errors past the last record, and when no magus stage writing records feeds this script.",
			Returns: []Ret{{Type: TypeAnyMap, Object: "PipeRecord"}},
			Raises:  true,
			Impl:    PipeNext,
		},
		{
			Name: "all",
			Doc: "Return every record not yet read, once the magus stage writing this script's stdin has ended. " +
				"Errors when no magus stage writing records feeds this script.",
			Returns: []Ret{{Type: TypeAny, Object: "[PipeRecord]"}},
			Raises:  true,
			Impl:    PipeAll,
		},
		{
			Name: "emit",
			Doc: "Write record to stdout for the stage downstream. A record read from upstream passes through " +
				"byte for byte; one built here is written from its fields, and needs a type. " +
				"A run.scope record's projects are what a run downstream that names none runs on.",
			Args:   []Arg{{Name: "record", Type: TypeAnyMap, Object: "PipeRecord"}},
			Raises: true,
			Impl:   PipeEmit,
		},
		{
			Name: "outputs",
			Doc: "Return the files the target of record declared as outputs and that exist on disk now, " +
				"sorted by workspace-relative path. record names a project and a target, like a run.target.result.",
			Args:    []Arg{{Name: "record", Type: TypeAnyMap, Object: "PipeRecord"}},
			Returns: []Ret{{Type: TypeAny, Object: "[Artifact]"}},
			Raises:  true,
			Impl:    PipeOutputs,
		},
		{
			// Not `export`, which Buzz reserves.
			Name: "export_to",
			Doc: "Copy artifact to dest, keeping its mode, and return dest. It writes a temporary file " +
				"beside dest and renames it into place, so a symlink at dest is replaced rather than " +
				"written through and an artifact exported onto itself survives.",
			Args: []Arg{
				{Name: "artifact", Type: TypeAnyMap, Object: "Artifact"},
				{Name: "dest", Type: TypeString},
			},
			Returns: []Ret{{Type: TypeString}},
			Raises:  true,
			Impl:    PipeExport,
		},
		{
			Name: "history",
			Doc: "Return every version of artifact the cache stored, newest first, with identical " +
				"consecutive content collapsed: when its bytes changed, which its VCS history cannot say.",
			Args:    []Arg{{Name: "artifact", Type: TypeAnyMap, Object: "Artifact"}},
			Returns: []Ret{{Type: TypeAny, Object: "[ArtifactVersion]"}},
			Raises:  true,
			Impl:    PipeHistory,
		},
		{
			Name: "diff",
			Doc: "Compare artifact on disk against its most recent different cached version with your " +
				"difftool: $MAGUS_DIFFTOOL, else $DIFFTOOL, else `git diff --no-index`. It renders nothing itself.",
			Args:   []Arg{{Name: "artifact", Type: TypeAnyMap, Object: "Artifact"}},
			Raises: true,
			Impl:   PipeDiff,
		},
		{
			Name: "value",
			Doc: "Return what a target returned, a str or a [str], from its run.target.value record. " +
				"Errors for any other record.",
			Args:    []Arg{{Name: "record", Type: TypeAnyMap, Object: "PipeRecord"}},
			Returns: []Ret{{Type: TypeAny}},
			Raises:  true,
			Impl:    PipeValue,
		},
	},
}

// PipeIO is the record stream a `magus buzz` stage reads and writes. In is nil when no
// magus stage writing records feeds the script; Out receives what it emits, and Prose
// what it shows a person, which is stderr while a magus reads Out.
type PipeIO struct {
	In       *report.Reader
	Upstream int
	Out      io.Writer
	Prose    io.Writer
}

type pipeIOKey struct{}

// WithPipe carries the stage's record stream on ctx for the pipe module.
func WithPipe(ctx context.Context, p PipeIO) context.Context {
	return context.WithValue(ctx, pipeIOKey{}, p)
}

// errNoRecordUpstream is what reading from a script no record stage feeds raises: a
// script written to filter a run's records has nothing to filter, and reading nothing
// would pass for "no failures".
var errNoRecordUpstream = errors.New("no magus stage writing records feeds this script's stdin; " +
	"pipe a run into it, like `magus run test . | magus buzz <script>`")

func pipeIn(ctx context.Context, member string) (*report.Reader, error) {
	p, _ := ctx.Value(pipeIOKey{}).(PipeIO)
	if p.In == nil {
		return nil, fmt.Errorf("pipe.%s: %w", member, errNoRecordUpstream)
	}
	return p.In, nil
}

// PipeMore implements pipe.more.
func PipeMore(ctx context.Context) (bool, error) {
	in, err := pipeIn(ctx, "more")
	if err != nil {
		return false, err
	}
	more, err := in.Peek(ctx)
	if err != nil {
		return false, fmt.Errorf("pipe.more: %w", err)
	}
	return more, nil
}

// PipeNext implements pipe.next.
func PipeNext(ctx context.Context) (types.PipeRecord, error) {
	in, err := pipeIn(ctx, "next")
	if err != nil {
		return types.PipeRecord{}, err
	}
	line, ok, err := in.Next(ctx)
	if err != nil {
		return types.PipeRecord{}, fmt.Errorf("pipe.next: %w", err)
	}
	if !ok {
		return types.PipeRecord{}, errors.New("pipe.next: the upstream stage ended and every record is read; ask pipe.more first")
	}
	return pipeRecordOf(line)
}

// PipeAll implements pipe.all.
func PipeAll(ctx context.Context) ([]types.PipeRecord, error) {
	in, err := pipeIn(ctx, "all")
	if err != nil {
		return nil, err
	}
	lines, err := in.Rest(ctx)
	if err != nil {
		return nil, fmt.Errorf("pipe.all: %w", err)
	}
	out := make([]types.PipeRecord, 0, len(lines))
	for _, line := range lines {
		rec, err := pipeRecordOf(line)
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, nil
}

// PipeEmit implements pipe.emit.
func PipeEmit(ctx context.Context, record map[string]any) error {
	p, _ := ctx.Value(pipeIOKey{}).(PipeIO)
	if p.Out == nil {
		return errors.New("pipe.emit: this script is not a pipe stage; run it with `magus buzz <script>`")
	}
	line, err := pipeLineOf(record)
	if err != nil {
		return fmt.Errorf("pipe.emit: %w", err)
	}
	return report.NewLineEncoder(p.Out).EncodeLine(line)
}

// pipeWorkspace is what the artifact members read off the workspace on ctx; *magus.Magus
// provides it.
type pipeWorkspace interface {
	Root() string
	Get(path string) *types.Project
	ResolveTargetOutputs(ctx context.Context, projects []*types.Project, target string) ([]types.TargetArtifact, error)
	ListArtifacts(ctx context.Context, projectPath, wsPath string) ([]cache.ArtifactVersion, error)
	GetArtifact(ctx context.Context, v cache.ArtifactVersion, dst string) error
}

func pipeWorkspaceOf(ctx context.Context, member string) (pipeWorkspace, error) {
	ws, ok := types.WorkspaceFromContext(ctx).(pipeWorkspace)
	if !ok {
		return nil, fmt.Errorf("pipe.%s reads the workspace's outputs and cache, and no workspace is attached; run the script inside one", member)
	}
	return ws, nil
}

// PipeOutputs implements pipe.outputs.
func PipeOutputs(ctx context.Context, record map[string]any) ([]types.TargetArtifact, error) {
	project, _ := record["project"].(string)
	target, _ := record["target"].(string)
	if target == "" {
		return nil, errors.New("pipe.outputs: the record names no target; pass a run.target.result")
	}
	ws, err := pipeWorkspaceOf(ctx, "outputs")
	if err != nil {
		return nil, err
	}
	if project == "" {
		project = "."
	}
	p := ws.Get(project)
	if p == nil && project == "." {
		p = ws.Get("")
	}
	if p == nil {
		return nil, fmt.Errorf("pipe.outputs: no project %q in this workspace", project)
	}
	artifacts, err := ws.ResolveTargetOutputs(ctx, []*types.Project{p}, target)
	if err != nil {
		return nil, fmt.Errorf("pipe.outputs: %w", err)
	}
	if artifacts == nil {
		artifacts = []types.TargetArtifact{}
	}
	return artifacts, nil
}

// artifactOf reads an Artifact argument, which must name a path.
func artifactOf(member string, artifact map[string]any) (types.TargetArtifact, error) {
	a := types.TargetArtifact{}
	a.Path, _ = artifact["path"].(string)
	a.Glob, _ = artifact["glob"].(string)
	a.ProjectPath, _ = artifact["project"].(string)
	if a.Path == "" {
		return a, fmt.Errorf("pipe.%s: the artifact names no path; take one from pipe.outputs", member)
	}
	return a, nil
}

// PipeExport implements pipe.export_to.
func PipeExport(ctx context.Context, artifact map[string]any, dest string) (string, error) {
	a, err := artifactOf("export_to", artifact)
	if err != nil {
		return "", err
	}
	if dest == "" {
		return "", errors.New("pipe.export_to: dest is empty")
	}
	ws, err := pipeWorkspaceOf(ctx, "export_to")
	if err != nil {
		return "", err
	}
	src := filepath.Join(ws.Root(), filepath.FromSlash(a.Path))
	dst := resolvePath(ctx, dest)
	if err := checkRead(ctx, src); err != nil {
		return "", err
	}
	if err := checkWrite(ctx, dst); err != nil {
		return "", err
	}
	if err := exportArtifact(src, dst); err != nil {
		return "", fmt.Errorf("pipe.export_to %s: %w", a.Path, err)
	}
	return dest, nil
}

// exportArtifact copies src to dst through a temporary file beside dst renamed into
// place. Writing dst in place destroyed data three ways: dst == src truncated the source
// before the read, a failed read left half a file where a valid artifact had been, and
// a symlink planted at dst was written through. The mode is set on the temp file because
// O_CREATE applies one only when it creates, and an exported binary lost +x.
func exportArtifact(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".magus-export-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if _, err := io.Copy(tmp, in); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(info.Mode().Perm()); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, dst)
}

// PipeHistory implements pipe.history.
func PipeHistory(ctx context.Context, artifact map[string]any) ([]types.ArtifactVersion, error) {
	a, err := artifactOf("history", artifact)
	if err != nil {
		return nil, err
	}
	ws, err := pipeWorkspaceOf(ctx, "history")
	if err != nil {
		return nil, err
	}
	versions, err := ws.ListArtifacts(ctx, a.ProjectPath, a.Path)
	if err != nil {
		return nil, fmt.Errorf("pipe.history %s: %w", a.Path, err)
	}
	out := make([]types.ArtifactVersion, 0, len(versions))
	for _, v := range versions {
		out = append(out, types.ArtifactVersion{
			Blob: v.Output.Blob, Short: v.ShortBlob(), Size: v.Output.Size, Target: v.Target,
			Created: v.CreatedAt.UTC().Format(time.RFC3339), Entry: v.EntryHash,
		})
	}
	return out, nil
}

// PipeDiff implements pipe.diff. A symlink version, or one matching the file on disk,
// leaves nothing to compare, and it says so rather than running a differ over nothing.
func PipeDiff(ctx context.Context, artifact map[string]any) error {
	a, err := artifactOf("diff", artifact)
	if err != nil {
		return err
	}
	ws, err := pipeWorkspaceOf(ctx, "diff")
	if err != nil {
		return err
	}
	p, _ := ctx.Value(pipeIOKey{}).(PipeIO)
	prose := p.Prose
	if prose == nil {
		prose = os.Stdout
	}
	versions, err := ws.ListArtifacts(ctx, a.ProjectPath, a.Path)
	if err != nil {
		return fmt.Errorf("pipe.diff %s: %w", a.Path, err)
	}
	workingCopy := filepath.Join(ws.Root(), filepath.FromSlash(a.Path))
	current, err := os.ReadFile(workingCopy)
	if err != nil {
		return fmt.Errorf("pipe.diff: %w", err)
	}
	sum := sha256.Sum256(current)
	currentBlob := hex.EncodeToString(sum[:])
	var prev *cache.ArtifactVersion
	for i := range versions {
		if versions[i].Output.Symlink != "" {
			fmt.Fprintf(prose, "pipe.diff: %s is a symlink to %q; diff compares content, not link targets\n", a.Path, versions[i].Output.Symlink)
			return nil
		}
		if versions[i].Output.Blob != currentBlob {
			prev = &versions[i]
			break
		}
	}
	if prev == nil {
		fmt.Fprintf(prose, "pipe.diff: %s matches every cached version; nothing to diff\n", a.Path)
		return nil
	}
	dir, err := os.MkdirTemp("", "magus-diff-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	// Named after the artifact, so the difftool's header says which file this is.
	staged := filepath.Join(dir, filepath.Base(a.Path))
	if err := ws.GetArtifact(ctx, *prev, staged); err != nil {
		return fmt.Errorf("pipe.diff %s: %w", a.Path, err)
	}
	name, args := difftool()
	args = append(args, staged, workingCopy)
	fmt.Fprintf(prose, "pipe.diff: %s %s (cached %s) vs the working tree\n", a.Path, prev.ShortBlob(), prev.CreatedAt.UTC().Format(time.RFC3339))
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdout, cmd.Stderr = prose, os.Stderr
	// An interactive difftool needs the terminal; a stdin carrying records is not one.
	if p.In == nil {
		cmd.Stdin = os.Stdin
	}
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		// A differ exits non-zero to mean "they differ", the normal answer here.
		if errors.As(err, &exit) {
			return nil
		}
		return fmt.Errorf("pipe.diff: could not run %q (set MAGUS_DIFFTOOL to a command that takes two paths): %w", name, err)
	}
	return nil
}

// difftool resolves the command to compare two paths with. MAGUS_DIFFTOOL wins so a
// magus-specific choice is possible, DIFFTOOL is the conventional variable, and `git
// diff --no-index` is the differ a magus workspace is guaranteed to have. The value is
// split on spaces so `delta --side-by-side` works without a shell.
func difftool() (string, []string) {
	for _, env := range []string{"MAGUS_DIFFTOOL", "DIFFTOOL"} {
		if v := strings.TrimSpace(os.Getenv(env)); v != "" {
			parts := strings.Fields(v)
			return parts[0], parts[1:]
		}
	}
	return "git", []string{"diff", "--no-index"}
}

// PipeValue implements pipe.value.
func PipeValue(_ context.Context, record map[string]any) (any, error) {
	typ, _ := record["type"].(string)
	body, _ := record["body"].(string)
	if typ != report.TypeTargetValue || body == "" {
		return nil, fmt.Errorf("pipe.value: a %s record carries no value; pass a %s record", typ, report.TypeTargetValue)
	}
	var v report.TargetValue
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		return nil, fmt.Errorf("pipe.value: %w", err)
	}
	return v.Value, nil
}

// pipeFields are the record fields PipeRecord lifts out of a body.
type pipeFields struct {
	Project  string   `json:"project"`
	Target   string   `json:"target"`
	Status   string   `json:"status"`
	Error    string   `json:"error"`
	Ref      string   `json:"ref"`
	Projects []string `json:"projects"`
}

func pipeRecordOf(line report.Line) (types.PipeRecord, error) {
	var f pipeFields
	if err := line.Decode(&f); err != nil {
		return types.PipeRecord{}, fmt.Errorf("pipe: %s record: %w", line.Type, err)
	}
	return types.PipeRecord{
		Schema: line.Schema, Type: line.Type,
		Project: f.Project, Target: f.Target, Status: f.Status, Error: f.Error, Ref: f.Ref,
		Projects: f.Projects, Body: string(line.Raw),
	}, nil
}

// pipeLineOf is the line a record crosses back out as: its body when it has one, so a
// record passed through loses nothing, else the envelope written from its fields.
func pipeLineOf(record map[string]any) (report.Line, error) {
	if body, _ := record["body"].(string); body != "" {
		return report.ParseLine([]byte(body))
	}
	typ, _ := record["type"].(string)
	if typ == "" {
		return report.Line{}, errors.New("a record needs a type, like run.scope")
	}
	// The envelope's schema and type open the line, the way every record magus writes
	// does, which is what a reader recognizes a record by.
	fields := map[string]any{}
	for _, k := range []string{"project", "target", "status", "error", "ref"} {
		if s, _ := record[k].(string); s != "" {
			fields[k] = s
		}
	}
	if projects := pipeStrings(record["projects"]); len(projects) > 0 {
		fields["projects"] = projects
	}
	body, err := json.Marshal(fields)
	if err != nil {
		return report.Line{}, err
	}
	head := fmt.Sprintf(`{"schema":%d,"type":%q`, report.Schema, typ)
	if len(body) > 2 {
		head += "," + string(body[1:len(body)-1])
	}
	return report.ParseLine([]byte(head + "}"))
}

func pipeStrings(v any) []string {
	items, _ := v.([]any)
	out := make([]string, 0, len(items))
	for _, item := range items {
		if s, ok := item.(string); ok && s != "" {
			out = append(out, s)
		}
	}
	return out
}
