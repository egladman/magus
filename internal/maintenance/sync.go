package maintenance

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/egladman/magus/internal/file"
	"github.com/egladman/magus/types"
)

// SyncOutcome is what one `magus job run sync-graph` did with its request.
type SyncOutcome string

const (
	SyncNoServer  SyncOutcome = "no-server" // nothing answered the socket, so nothing was submitted
	SyncSubmitted SyncOutcome = "submitted" // the server started a job
	SyncCoalesced SyncOutcome = "coalesced" // the server already had one running for this workspace
	SyncRefused   SyncOutcome = "refused"   // the server answered and did not take the job
)

// SyncRequest records the last `magus job run sync-graph` made in a checkout. The VCS
// refresh hook discards that command's output, so without this record a later lookup
// cannot tell a hook that ran with no server from one that never ran.
type SyncRequest struct {
	At      time.Time   `json:"at"`
	Outcome SyncOutcome `json:"outcome"`
	Job     string      `json:"job,omitempty"`    // the invocation id, when the server named one
	Detail  string      `json:"detail,omitempty"` // the refusal, for SyncRefused
}

const syncRequestFile = "sync-request.json"

// RecordSyncRequest writes r into dir, replacing the previous record. It is called from a
// checkout hook, so it does one small write and nothing else.
func RecordSyncRequest(dir string, r SyncRequest) error {
	data, err := json.Marshal(r)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return file.ReplaceFile(filepath.Join(dir, syncRequestFile), data, 0o600)
}

// ReadSyncRequest reads the record RecordSyncRequest left in dir, ok=false when there is
// none.
func ReadSyncRequest(dir string) (r SyncRequest, ok bool, err error) {
	data, err := os.ReadFile(filepath.Join(dir, syncRequestFile))
	if errors.Is(err, os.ErrNotExist) {
		return SyncRequest{}, false, nil
	}
	if err != nil {
		return SyncRequest{}, false, err
	}
	if err := json.Unmarshal(data, &r); err != nil {
		return SyncRequest{}, false, fmt.Errorf("maintenance: %s: %w", syncRequestFile, err)
	}
	return r, true, nil
}

// SyncObservation is what a client can see, from outside the server, of why the server's
// sync-graph job did or did not keep a checkout's index current.
type SyncObservation struct {
	ServerLive    bool
	ServerVersion string // the answering server's build, "" when none answered
	ServerPID     int
	Version       string            // this binary's build
	Building      *GraphBuildHolder // a graph build, manual or server, holding this checkout's build lock now
	HookChecked   bool              // the VCS's hooks could be read; the Hook fields mean nothing otherwise
	HookCommand   string            // what the VCS refresh hook runs, "" when none is installed
	HookBinary    string            // the binary HookCommand starts, as the hook spells it
	HookRunnable  bool              // HookBinary exists where the hook's shell would look
	LastRequest   *SyncRequest      // the last recorded `job run sync-graph` in this checkout
	IndexBuilt    time.Time         // when the index was last written, zero when never
}

// Commands are the invocations a diagnosis names, rendered by the caller so they follow
// how the reader invoked magus.
type Commands struct {
	GraphBuild, ServerStart, ServerStop, JobRunSync string
}

// DiagnoseSync names the first cause o shows, most specific first: a build running now
// explains everything after it, and a hook that cannot start its binary explains a silent
// server.
func DiagnoseSync(o SyncObservation, c Commands) types.KnowledgeIndexCause {
	once := "`" + c.GraphBuild + "` indexes it once now"
	switch req := o.LastRequest; {
	case o.Building != nil:
		return types.KnowledgeIndexCause{
			Why: fmt.Sprintf("%s is indexing this checkout now", o.Building),
			Fix: "ask again when it finishes; `" + c.GraphBuild + "` waits for it rather than starting a second build",
		}
	case o.HookChecked && o.HookCommand == "":
		return types.KnowledgeIndexCause{
			Why: "no VCS refresh hook is installed, so no checkout, merge or rebase here asks for a sync",
			Fix: "`" + c.ServerStart + "` from this checkout installs it; " + once,
		}
	case o.HookChecked && !o.HookRunnable:
		return types.KnowledgeIndexCause{
			Why: fmt.Sprintf("the refresh hook runs `%s` and %s does not exist, so no checkout, merge or rebase here has asked for a sync", o.HookCommand, o.HookBinary),
			Fix: fmt.Sprintf("put %s in place for the next checkout; %s", o.HookBinary, once),
		}
	case req != nil && req.Outcome == SyncRefused && req.At.After(o.IndexBuilt):
		return types.KnowledgeIndexCause{
			Why: fmt.Sprintf("the server refused the sync requested at %s: %s", stamp(req.At), req.Detail),
			Fix: "`" + c.ServerStop + "` then `" + c.ServerStart + "` restarts it on this build; " + once,
		}
	case !o.ServerLive:
		why := "no server is running, so the refresh hook's `" + c.JobRunSync + "` does nothing"
		if req != nil && req.Outcome == SyncNoServer && req.At.After(o.IndexBuilt) {
			why = fmt.Sprintf("no server was running at the last sync request (`%s`, from the refresh hook or by hand, at %s), so it did nothing", c.JobRunSync, stamp(req.At))
		}
		return types.KnowledgeIndexCause{Why: why, Fix: "`" + c.ServerStart + "` keeps it current from now on; " + once}
	case o.ServerVersion != "" && o.Version != "" && o.ServerVersion != o.Version:
		return types.KnowledgeIndexCause{
			Why: fmt.Sprintf("the server (pid %d) is %s and this checkout's magus is %s; a server refuses jobs from another build", o.ServerPID, o.ServerVersion, o.Version),
			Fix: "`" + c.ServerStop + "` then `" + c.ServerStart + "` restarts it on this build; " + once,
		}
	case req != nil && (req.Outcome == SyncSubmitted || req.Outcome == SyncCoalesced) && req.At.After(o.IndexBuilt):
		job := ""
		if req.Job != "" {
			job = " (job " + req.Job + ")"
		}
		return types.KnowledgeIndexCause{
			Why: fmt.Sprintf("the server took the sync requested at %s%s and the index is still not current, so that job failed or has not written it", stamp(req.At), job),
			Fix: "`" + c.GraphBuild + "` here builds it and shows any failure",
		}
	default:
		return types.KnowledgeIndexCause{
			Why: "a server is running, but no sync has been asked for since the index was last built",
			Fix: "`" + c.JobRunSync + "` asks the server; " + once,
		}
	}
}

func stamp(t time.Time) string { return t.Local().Format("2006-01-02 15:04:05") }

// HookBinary resolves the binary a hook command starts the way the hook's shell would: a
// path relative to top, the repository's top level where git runs hooks, or a bare name
// on PATH. It returns the binary as the command spells it and whether it exists.
func HookBinary(top, command string) (string, bool) {
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return "", false
	}
	bin := fields[0]
	if !strings.Contains(bin, "/") {
		_, err := exec.LookPath(bin)
		return bin, err == nil
	}
	path := bin
	if !filepath.IsAbs(path) {
		path = filepath.Join(top, path)
	}
	_, err := os.Stat(path)
	return bin, err == nil
}
