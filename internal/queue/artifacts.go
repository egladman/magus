package queue

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/egladman/magus/internal/queue/types"
	magustypes "github.com/egladman/magus/types"
)

// The artifact names a validation run uploads under. The plan artifact carries
// [PlanFile] at its root.
const (
	PlanArtifact          = "mergequeue-plan"
	VerdictArtifactPrefix = "mergequeue-verdict-"
)

// Bounds on what an [ArtifactFollower] downloads and unpacks. A verdict artifact holds
// one small JSON document, so anything near these is not one.
const (
	maxArtifactBytes = 32 << 20
	maxUnpackedBytes = 64 << 20
	maxArtifactFiles = 64
	maxRedirects     = 10
)

// ArtifactFollower follows one validation run through an [types.ArtifactLister] and unpacks its
// artifacts into the verdict directory Path as the run uploads them: [PlanArtifact]
// becomes Path/[PlanFile] and each [VerdictArtifactPrefix]<id> becomes the entry for
// change <id>. It is a [types.VerdictSource], so an Applier merges a green change while slower
// candidates of the same run are still going. An artifact it cannot fetch or unpack is
// rejected for its change alone.
//
// Every listing must say the run is Definition, started by an event that runs Branch's
// own copy of it, on Branch of the run's own repository; any other run is refused
// ([magustypes.QueueRunUntrusted]) before anything of it is downloaded, since a run a
// change started uploads whatever that change's author wrote.
//
// It reports done only after a listing that said the run was complete. Methods must
// not be called concurrently, and an ArtifactFollower must not be copied after first
// use.
type ArtifactFollower struct {
	Lister types.ArtifactLister
	Source string // the run, as Lister names it
	Path   string
	// Branch is the base branch the run must have run on, and Definition what it must
	// have run (github: the workflow file's path). Both are required.
	Branch     string
	Definition string
	// Follow keeps reading until the run completes. Without it, one listing is taken as
	// everything the run will upload.
	Follow bool
	// Interval is how long [ArtifactFollower.Plan] waits between listings while
	// following.
	Interval time.Duration
	// Client makes every download; nil uses one with a 60 second timeout. Its
	// CheckRedirect is replaced: a redirect to another host, such as the storage host a
	// download redirects to, carries none of the listing's headers, and a redirect away
	// from https is refused.
	Client *http.Client
	Events *Events

	dir      VerdictDir
	rejected []types.RejectedVerdict // not yet handed out by Poll
	failed   map[string]bool         // artifact names that could not be unpacked
}

var _ types.VerdictSource = (*ArtifactFollower)(nil)

var defaultClient = &http.Client{Timeout: 60 * time.Second}

// Plan returns the plan the run's [PlanArtifact] carries, waiting for it while following.
// It returns false, and no error, when the run completed without uploading one or,
// without Follow, has not uploaded one yet.
func (f *ArtifactFollower) Plan(ctx context.Context) (types.Plan, bool, error) {
	return awaitPlan(ctx, filepath.Join(f.Path, PlanFile), f.Interval, f.sync)
}

// Poll unpacks what the run uploaded since the last call and returns the verdicts among
// it; done once the run is complete, or at once without Follow.
func (f *ArtifactFollower) Poll(ctx context.Context) (types.VerdictBatch, error) {
	complete, err := f.sync(ctx)
	if err != nil {
		return types.VerdictBatch{}, err
	}
	if complete {
		if err := f.dir.MarkDone(); err != nil {
			return types.VerdictBatch{}, err
		}
	}
	batch, err := f.dir.Poll(ctx)
	if err != nil {
		return types.VerdictBatch{}, err
	}
	batch.Rejected = append(f.rejected, batch.Rejected...)
	f.rejected = nil
	return batch, nil
}

// sync unpacks every artifact not yet on disk and reports whether the listing is the
// last this source reads.
func (f *ArtifactFollower) sync(ctx context.Context) (bool, error) {
	if f.Lister == nil || f.Source == "" || f.Path == "" || f.Branch == "" || f.Definition == "" {
		return false, errors.New("artifact follower needs a lister, a source, a path, a branch and a definition")
	}
	f.dir.Path, f.dir.Follow = f.Path, true
	list, err := f.Lister.ListArtifacts(ctx, f.Source)
	if err != nil {
		return false, fmt.Errorf("run %s: %w", f.Source, err)
	}
	if err := f.trust(list.Run); err != nil {
		return false, err
	}
	if err := os.MkdirAll(f.Path, 0o755); err != nil {
		return false, err
	}
	for _, a := range list.Artifacts {
		switch {
		case a.Name == PlanArtifact:
			if err := f.unpackPlan(ctx, a, list.Headers); err != nil {
				return false, err
			}
		case strings.HasPrefix(a.Name, VerdictArtifactPrefix) && !f.failed[a.Name]:
			change := strings.TrimPrefix(a.Name, VerdictArtifactPrefix)
			if err := f.unpackVerdict(ctx, a, list.Headers, change); err != nil {
				// Not tried again: its change already waits.
				if f.failed == nil {
					f.failed = map[string]bool{}
				}
				f.failed[a.Name] = true
				f.rejected = append(f.rejected, types.RejectedVerdict{Change: change, Reason: err.Error()})
				f.notice("rejected " + a.Name + ": " + err.Error())
			}
		}
	}
	return list.Complete || !f.Follow, nil
}

// trust refuses a run other than Definition run by its own repository's Branch.
func (f *ArtifactFollower) trust(o types.RunOrigin) error {
	var why string
	switch {
	case o.Repo == "":
		why = "the provider named no repository for it"
	case o.HeadRepo != o.Repo:
		why = fmt.Sprintf("it ran a commit of %q, not of %q", o.HeadRepo, o.Repo)
	case !o.BranchEvent:
		why = fmt.Sprintf("%q started it, and that event runs a definition a change supplied", o.Event)
	case o.HeadBranch != f.Branch:
		why = fmt.Sprintf("it ran on %q, not %q", o.HeadBranch, f.Branch)
	case o.Definition != f.Definition:
		why = fmt.Sprintf("it ran %q, not %q", o.Definition, f.Definition)
	default:
		return nil
	}
	return magustypes.DiagnosticErrorf(magustypes.QueueRunUntrusted,
		"run %s is not %s run by %s's own %s: %s; apply reads nothing it uploaded", f.Source, f.Definition, o.Repo, f.Branch, why)
}

func (f *ArtifactFollower) unpackPlan(ctx context.Context, a types.Artifact, headers map[string]string) error {
	final := filepath.Join(f.Path, PlanFile)
	if exists(final) {
		return nil
	}
	tmp, err := f.download(ctx, a, headers)
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	if err := os.Rename(filepath.Join(tmp, PlanFile), final); err != nil {
		return fmt.Errorf("artifact %s: %w", a.Name, err)
	}
	f.notice("unpacked " + a.Name)
	return nil
}

func (f *ArtifactFollower) unpackVerdict(ctx context.Context, a types.Artifact, headers map[string]string, change string) error {
	if err := types.CheckID(change); err != nil {
		return err
	}
	final := filepath.Join(f.Path, change)
	if exists(final) {
		return nil
	}
	tmp, err := f.download(ctx, a, headers)
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp) // a no-op once renamed into place
	if err := os.Rename(tmp, final); err != nil {
		return fmt.Errorf("artifact %s: %w", a.Name, err)
	}
	f.notice("unpacked " + a.Name)
	return nil
}

// download extracts a into a new directory under Path whose name starts with ".", which
// [VerdictDir.Poll] skips until it is renamed into place. The caller owns it.
func (f *ArtifactFollower) download(ctx context.Context, a types.Artifact, headers map[string]string) (string, error) {
	archive, err := os.CreateTemp(f.Path, ".zip-")
	if err != nil {
		return "", err
	}
	defer os.Remove(archive.Name())
	defer archive.Close()
	if err := f.get(ctx, a.URL, headers, archive); err != nil {
		return "", fmt.Errorf("artifact %s: %w", a.Name, err)
	}
	tmp, err := os.MkdirTemp(f.Path, ".dl-")
	if err != nil {
		return "", err
	}
	if err := extract(archive.Name(), tmp); err != nil {
		_ = os.RemoveAll(tmp)
		return "", fmt.Errorf("artifact %s: %w", a.Name, err)
	}
	return tmp, nil
}

func (f *ArtifactFollower) get(ctx context.Context, raw string, headers map[string]string, w io.Writer) error {
	u, err := url.Parse(raw)
	if err != nil {
		return err
	}
	if u.Scheme != "https" {
		return fmt.Errorf("%q is not an https URL", raw)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	client := *defaultClient
	if f.Client != nil {
		client = *f.Client
	}
	// Go's client forwards every header but Authorization and Cookie across hosts, and a
	// provider may carry its credential in any of them.
	client.CheckRedirect = func(next *http.Request, via []*http.Request) error {
		switch {
		case len(via) >= maxRedirects:
			return fmt.Errorf("stopped after %d redirects", maxRedirects)
		case next.URL.Scheme != "https":
			return fmt.Errorf("refused a redirect to %s, which is not https", next.URL.Redacted())
		case next.URL.Host != via[0].URL.Host:
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
	n, err := io.Copy(w, io.LimitReader(res.Body, maxArtifactBytes+1))
	if err != nil {
		return err
	}
	if n > maxArtifactBytes {
		return fmt.Errorf("GET %s: larger than %d bytes", u.Path, maxArtifactBytes)
	}
	return nil
}

// extract unzips file into dir, refusing any entry that is not a regular file or
// directory inside dir, and more than the bounds allow.
func extract(file, dir string) error {
	zr, err := zip.OpenReader(file)
	if err != nil {
		return err
	}
	defer zr.Close()
	if len(zr.File) > maxArtifactFiles {
		return fmt.Errorf("%d entries, more than %d", len(zr.File), maxArtifactFiles)
	}
	budget := int64(maxUnpackedBytes)
	for _, e := range zr.File {
		if !filepath.IsLocal(e.Name) {
			return fmt.Errorf("entry %q escapes the artifact", e.Name)
		}
		dst := filepath.Join(dir, e.Name) //nolint:gosec // G305: filepath.IsLocal above refuses every entry that would leave dir
		mode := e.Mode()
		switch {
		case mode.IsDir():
			if err := os.MkdirAll(dst, 0o755); err != nil {
				return err
			}
		case mode.IsRegular():
			n, err := writeEntry(e, dst, budget)
			if err != nil {
				return err
			}
			budget -= n
		default:
			return fmt.Errorf("entry %q is not a regular file", e.Name)
		}
	}
	return nil
}

// writeEntry writes e to dst, at most budget bytes of it.
func writeEntry(e *zip.File, dst string, budget int64) (int64, error) {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return 0, err
	}
	src, err := e.Open()
	if err != nil {
		return 0, err
	}
	defer src.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return 0, err
	}
	n, err := io.Copy(out, io.LimitReader(src, budget+1))
	if err != nil {
		_ = out.Close()
		return n, err
	}
	if n > budget {
		_ = out.Close()
		return n, fmt.Errorf("unpacks to more than %d bytes", maxUnpackedBytes)
	}
	return n, out.Close()
}

func (f *ArtifactFollower) notice(reason string) {
	f.Events.Emit(Event{Kind: EventNotice, Reason: reason})
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
