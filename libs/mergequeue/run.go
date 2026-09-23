package mergequeue

import (
	"archive/zip"
	"context"
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

// RunReader is the CI system's side of the queue: what one validation run has uploaded.
type RunReader interface {
	// RunArtifacts lists the artifacts run has uploaded so far. Completed must be read
	// before the listing, so a listing that says completed holds everything.
	RunArtifacts(ctx context.Context, run string) (RunArtifacts, error)
}

// RunArtifacts is one listing of a run's artifacts.
type RunArtifacts struct {
	Completed bool
	// Headers are what a download needs, its credential included. They are sent to each
	// artifact's URL and dropped on a redirect to another host.
	Headers   map[string]string
	Artifacts []Artifact
}

// Artifact is one uploaded zip archive.
type Artifact struct {
	Name string
	URL  string // http or https
}

// RunFollower follows one validation run through a [RunReader] and unpacks its artifacts
// into the verdict directory Path as the run uploads them: [PlanArtifact] becomes
// Path/[PlanFile] and each [VerdictArtifactPrefix]<id> becomes the entry for change
// <id>. It is a [VerdictSource], so an Applier merges a green change while slower
// candidates of the same run are still going.
//
// It reports done only after a listing that said the run had completed. Methods must
// not be called concurrently, and a RunFollower must not be copied after first use.
type RunFollower struct {
	Reader RunReader
	Run    string // the run, as Reader names it
	Path   string
	// Follow keeps reading until the run completes. Without it, one listing is taken as
	// everything the run will upload.
	Follow bool
	// Interval is how long [RunFollower.Plan] waits between listings while following.
	Interval time.Duration
	// Client makes every download; nil uses one with a 60 second timeout. Its
	// CheckRedirect is replaced: a redirect to another host, such as the storage host a
	// download redirects to, carries none of the listing's headers.
	Client *http.Client
	Events *Events

	dir VerdictDir
}

var _ VerdictSource = (*RunFollower)(nil)

var defaultClient = &http.Client{Timeout: 60 * time.Second}

// Plan returns the plan the run's [PlanArtifact] carries, waiting for it while following.
// It returns false, and no error, when the run completed without uploading one or,
// without Follow, has not uploaded one yet.
func (r *RunFollower) Plan(ctx context.Context) (Plan, bool, error) {
	return awaitPlan(ctx, filepath.Join(r.Path, PlanFile), r.Interval, r.sync)
}

// Poll unpacks what the run uploaded since the last call and returns the verdicts among
// it; done once the run has completed, or at once without Follow.
func (r *RunFollower) Poll(ctx context.Context) ([]Verdict, bool, error) {
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
// last this source reads.
func (r *RunFollower) sync(ctx context.Context) (bool, error) {
	if r.Reader == nil || r.Run == "" || r.Path == "" {
		return false, errors.New("a RunFollower needs a Reader, a Run and a Path")
	}
	r.dir.Path, r.dir.Follow = r.Path, true
	list, err := r.Reader.RunArtifacts(ctx, r.Run)
	if err != nil {
		return false, fmt.Errorf("run %s: %w", r.Run, err)
	}
	if err := os.MkdirAll(r.Path, 0o755); err != nil {
		return false, err
	}
	for _, a := range list.Artifacts {
		switch {
		case a.Name == PlanArtifact:
			if err := r.unpackPlan(ctx, a, list.Headers); err != nil {
				return false, err
			}
		case strings.HasPrefix(a.Name, VerdictArtifactPrefix):
			if err := r.unpackVerdict(ctx, a, list.Headers, strings.TrimPrefix(a.Name, VerdictArtifactPrefix)); err != nil {
				return false, err
			}
		}
	}
	return list.Completed || !r.Follow, nil
}

func (r *RunFollower) unpackPlan(ctx context.Context, a Artifact, headers map[string]string) error {
	final := filepath.Join(r.Path, PlanFile)
	if exists(final) {
		return nil
	}
	tmp, err := r.download(ctx, a, headers)
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	if err := os.Rename(filepath.Join(tmp, PlanFile), final); err != nil {
		return fmt.Errorf("artifact %s: %w", a.Name, err)
	}
	r.notice("unpacked " + a.Name)
	return nil
}

func (r *RunFollower) unpackVerdict(ctx context.Context, a Artifact, headers map[string]string, change string) error {
	if err := CheckID(change); err != nil {
		return fmt.Errorf("artifact %s: %w", a.Name, err)
	}
	final := filepath.Join(r.Path, change)
	if exists(final) {
		return nil
	}
	tmp, err := r.download(ctx, a, headers)
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp) // a no-op once renamed into place
	if err := os.Rename(tmp, final); err != nil {
		return fmt.Errorf("artifact %s: %w", a.Name, err)
	}
	r.notice("unpacked " + a.Name)
	return nil
}

// download extracts a into a new directory under Path whose name starts with ".", which
// [VerdictDir.Poll] skips until it is renamed into place. The caller owns it.
func (r *RunFollower) download(ctx context.Context, a Artifact, headers map[string]string) (string, error) {
	archive, err := os.CreateTemp(r.Path, ".zip-")
	if err != nil {
		return "", err
	}
	defer os.Remove(archive.Name())
	defer archive.Close()
	if err := r.get(ctx, a.URL, headers, archive); err != nil {
		return "", fmt.Errorf("artifact %s: %w", a.Name, err)
	}
	tmp, err := os.MkdirTemp(r.Path, ".dl-")
	if err != nil {
		return "", err
	}
	if err := extract(archive.Name(), tmp); err != nil {
		_ = os.RemoveAll(tmp)
		return "", fmt.Errorf("artifact %s: %w", a.Name, err)
	}
	return tmp, nil
}

func (r *RunFollower) get(ctx context.Context, raw string, headers map[string]string, w io.Writer) error {
	u, err := url.Parse(raw)
	if err != nil {
		return err
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return fmt.Errorf("%q is not an http or https URL", raw)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	client := *defaultClient
	if r.Client != nil {
		client = *r.Client
	}
	// Go's client forwards every header but Authorization and Cookie across hosts, and a
	// provider may carry its credential in any of them.
	client.CheckRedirect = func(next *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return errors.New("stopped after 10 redirects")
		}
		if next.URL.Host != via[0].URL.Host {
			for k := range headers {
				next.Header.Del(k)
			}
		}
		return nil
	}
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(res.Body, 512))
		return fmt.Errorf("GET %s: %s: %s", u.Path, res.Status, strings.TrimSpace(string(msg)))
	}
	_, err = io.Copy(w, res.Body)
	return err
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

func (r *RunFollower) notice(reason string) {
	r.Events.Emit(Event{Kind: EventNotice, Reason: reason})
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
