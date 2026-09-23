package mergequeue

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// The artifact names a validation run uploads under. The plan artifact carries
// [PlanFile] at its root.
const (
	PlanArtifact          = "magus-queue-plan"
	VerdictArtifactPrefix = "magus-queue-verdict-"
)

// DefaultActionsAPI is the GitHub REST API an [ActionsRun] reads when API is empty.
const DefaultActionsAPI = "https://api.github.com"

// artifactPage is the listing's page size, the API's maximum.
const artifactPage = 100

// ActionsRun follows one GitHub Actions workflow run and unpacks its artifacts into the
// verdict directory Path as the run uploads them: [PlanArtifact] becomes Path/[PlanFile]
// and each [VerdictArtifactPrefix]<id> becomes the entry for change <id>. It is a
// [VerdictSource], so an Applier merges a green change while slower stages of
// the same run are still going.
//
// While following, it reads the run's status before listing its artifacts, and reports
// done only after a poll that saw the run completed, so that poll's listing held every
// artifact the run uploaded. Methods must not be called concurrently, and an ActionsRun
// must not be copied after first use.
type ActionsRun struct {
	API   string // REST base URL; empty means DefaultActionsAPI
	Repo  string // owner/name
	RunID string
	Token string
	Path  string
	// Follow keeps reading until the run completes. Without it, one listing is taken as
	// everything the run will upload.
	Follow bool
	// Interval is how long [ActionsRun.Plan] waits between polls while following.
	Interval time.Duration
	// Client makes every request; nil uses one with a 60 second timeout. Go's client
	// drops Authorization on a redirect to another host, which is what keeps the token
	// away from the storage host an artifact download redirects to.
	Client *http.Client
	Events *Events

	dir VerdictDir
}

var _ VerdictSource = (*ActionsRun)(nil)

var defaultClient = &http.Client{Timeout: 60 * time.Second}

// Plan returns the plan the run's [PlanArtifact] carries, waiting for it while following.
// It returns false, and no error, when the run completed without uploading one or,
// without Follow, has not uploaded one yet.
func (r *ActionsRun) Plan(ctx context.Context) (Plan, bool, error) {
	return awaitPlan(ctx, filepath.Join(r.Path, PlanFile), r.Interval, r.sync)
}

// Poll unpacks what the run uploaded since the last call and returns the verdicts among
// it; done once the run has completed, or at once without Follow.
func (r *ActionsRun) Poll(ctx context.Context) ([]Verdict, bool, error) {
	completed, err := r.sync(ctx)
	if err != nil {
		return nil, false, err
	}
	if completed {
		if err := r.dir.MarkDone(); err != nil {
			return nil, false, err
		}
	}
	return r.dir.Poll(ctx)
}

// sync unpacks every artifact not yet on disk and reports whether the listing is the
// last this source reads: the run had completed before it began, or r does not follow.
func (r *ActionsRun) sync(ctx context.Context) (bool, error) {
	if r.Repo == "" || r.RunID == "" || r.Path == "" {
		return false, errors.New("an ActionsRun needs a Repo, a RunID and a Path")
	}
	r.dir.Path, r.dir.Follow = r.Path, true
	var run struct {
		Status string `json:"status"`
	}
	if err := r.getJSON(ctx, r.repoURL("actions", "runs", r.RunID), &run); err != nil {
		return false, fmt.Errorf("run %s: %w", r.RunID, err)
	}
	artifacts, err := r.artifacts(ctx)
	if err != nil {
		return false, err
	}
	if err := os.MkdirAll(r.Path, 0o755); err != nil {
		return false, err
	}
	for _, a := range artifacts {
		switch {
		case a.Name == PlanArtifact:
			if err := r.unpackPlan(ctx, a.ID); err != nil {
				return false, err
			}
		case strings.HasPrefix(a.Name, VerdictArtifactPrefix):
			if err := r.unpackVerdict(ctx, a.ID, strings.TrimPrefix(a.Name, VerdictArtifactPrefix)); err != nil {
				return false, err
			}
		}
	}
	return run.Status == "completed" || !r.Follow, nil
}

type artifact struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

func (r *ActionsRun) artifacts(ctx context.Context) ([]artifact, error) {
	var all []artifact
	for page := 1; ; page++ {
		var body struct {
			TotalCount int        `json:"total_count"`
			Artifacts  []artifact `json:"artifacts"`
		}
		u := r.repoURL("actions", "runs", r.RunID, "artifacts") + fmt.Sprintf("?per_page=%d&page=%d", artifactPage, page)
		if err := r.getJSON(ctx, u, &body); err != nil {
			return nil, fmt.Errorf("run %s artifacts: %w", r.RunID, err)
		}
		all = append(all, body.Artifacts...)
		if len(body.Artifacts) < artifactPage || len(all) >= body.TotalCount {
			return all, nil
		}
	}
}

func (r *ActionsRun) unpackPlan(ctx context.Context, id int64) error {
	final := filepath.Join(r.Path, PlanFile)
	if exists(final) {
		return nil
	}
	tmp, err := r.download(ctx, id, PlanArtifact)
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	if err := os.Rename(filepath.Join(tmp, PlanFile), final); err != nil {
		return fmt.Errorf("artifact %s: %w", PlanArtifact, err)
	}
	r.notice("unpacked " + PlanArtifact)
	return nil
}

func (r *ActionsRun) unpackVerdict(ctx context.Context, id int64, change string) error {
	if err := CheckID(change); err != nil {
		return fmt.Errorf("artifact %s%s: %w", VerdictArtifactPrefix, change, err)
	}
	final := filepath.Join(r.Path, change)
	if exists(final) {
		return nil
	}
	name := VerdictArtifactPrefix + change
	tmp, err := r.download(ctx, id, name)
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp) // a no-op once renamed into place
	if err := os.Rename(tmp, final); err != nil {
		return fmt.Errorf("artifact %s: %w", name, err)
	}
	r.notice("unpacked " + name)
	return nil
}

// download extracts artifact id into a new directory under Path whose name starts with
// ".", which [VerdictDir.Poll] skips until it is renamed into place. The caller owns it.
func (r *ActionsRun) download(ctx context.Context, id int64, name string) (string, error) {
	archive, err := os.CreateTemp(r.Path, ".zip-")
	if err != nil {
		return "", err
	}
	defer os.Remove(archive.Name())
	defer archive.Close()
	u := r.repoURL("actions", "artifacts", fmt.Sprint(id), "zip")
	if err := r.get(ctx, u, func(body io.Reader) error {
		_, err := io.Copy(archive, body)
		return err
	}); err != nil {
		return "", fmt.Errorf("artifact %s: %w", name, err)
	}
	tmp, err := os.MkdirTemp(r.Path, ".dl-")
	if err != nil {
		return "", err
	}
	if err := extract(archive.Name(), tmp); err != nil {
		_ = os.RemoveAll(tmp)
		return "", fmt.Errorf("artifact %s: %w", name, err)
	}
	return tmp, nil
}

// extract unzips file into dir, refusing any entry that is not a regular file or
// directory inside dir.
func extract(file, dir string) error {
	zr, err := zip.OpenReader(file)
	if err != nil {
		return err
	}
	defer zr.Close()
	for _, e := range zr.File {
		name := path.Clean(e.Name)
		if !fs.ValidPath(name) || name == "." {
			return fmt.Errorf("entry %q escapes the artifact", e.Name)
		}
		dst := filepath.Join(dir, filepath.FromSlash(name))
		mode := e.Mode()
		switch {
		case mode.IsDir():
			if err := os.MkdirAll(dst, 0o755); err != nil {
				return err
			}
		case mode.IsRegular():
			if err := writeEntry(e, dst); err != nil {
				return err
			}
		default:
			return fmt.Errorf("entry %q is not a regular file", e.Name)
		}
	}
	return nil
}

func writeEntry(e *zip.File, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	src, err := e.Open()
	if err != nil {
		return err
	}
	defer src.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, src); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// repoURL joins segments, each escaped, under the repository's REST path.
func (r *ActionsRun) repoURL(segments ...string) string {
	base := strings.TrimRight(r.API, "/")
	if base == "" {
		base = DefaultActionsAPI
	}
	u := base + "/repos/" + r.Repo
	for _, s := range segments {
		u += "/" + url.PathEscape(s)
	}
	return u
}

func (r *ActionsRun) getJSON(ctx context.Context, u string, into any) error {
	return r.get(ctx, u, func(body io.Reader) error { return json.NewDecoder(body).Decode(into) })
}

func (r *ActionsRun) get(ctx context.Context, u string, read func(io.Reader) error) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if r.Token != "" {
		req.Header.Set("Authorization", "Bearer "+r.Token)
	}
	client := r.Client
	if client == nil {
		client = defaultClient
	}
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(res.Body, 512))
		return fmt.Errorf("GET %s: %s: %s", req.URL.Path, res.Status, strings.TrimSpace(string(msg)))
	}
	return read(res.Body)
}

func (r *ActionsRun) notice(reason string) {
	r.Events.Emit(Event{Kind: EventNotice, Reason: reason})
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
