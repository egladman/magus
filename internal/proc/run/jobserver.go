package run

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// A step seated with N slots hands its processes a GNU make jobserver holding N-1
// tokens, so `make`, cargo, and any other client of the protocol run at most N jobs
// between them: the process magus starts holds the implicit Nth.
//
// The POSIX pipe form, never the FIFO form. GNU make 4.4 reads both, but 4.2 and 4.3
// (Debian 12, Ubuntu 22.04 and 24.04) die on `fifo:PATH` ("internal error: invalid
// --jobserver-auth string"), and 3.81 (macOS's /usr/bin/make) knows only
// --jobserver-fds. A pipe also has no path: nothing for a landlock policy to deny and
// nothing left in a temp dir when magus is killed. The cost is ninja 1.13, whose client
// reads only the FIFO form; it warns and schedules as it does today.
//
// See the GNU make manual, "Sharing Job Slots with GNU make" (POSIX Jobserver
// Interaction), https://www.gnu.org/software/make/manual/html_node/POSIX-Jobserver.html

// jobserverToken is the byte GNU make itself writes. A client writes back the byte it
// read, so any byte make treats as an ordinary token works.
const jobserverToken = '+'

// maxJobserverTokens keeps the preload to one write that cannot block: every POSIX pipe
// holds at least PIPE_BUF bytes, and 512 is the smallest PIPE_BUF POSIX allows.
const maxJobserverTokens = 512

// firstExtraFD is the descriptor exec.Cmd.ExtraFiles[0] becomes in the child; entry i
// becomes firstExtraFD+i.
const firstExtraFD = 3

// Jobserver is the token pool one step's processes share.
type Jobserver struct {
	r, w   *os.File
	tokens int
}

// OpenJobserver returns a pool for a step granted slots, preloaded with slots-1 tokens
// (at most 512). slots must be at least 2: a one-slot pool holds no tokens, and cargo
// would read it as "run one rustc at a time".
//
// errors.ErrUnsupported on windows and wasm, where exec.Cmd cannot hand a child extra
// descriptors. The caller owns Close.
func OpenJobserver(slots int) (*Jobserver, error) {
	if slots < 2 {
		return nil, fmt.Errorf("jobserver: %d slots leave no tokens to share", slots)
	}
	r, w, err := openJobserverPipe()
	if err != nil {
		return nil, fmt.Errorf("jobserver: %w", err)
	}
	tokens := min(slots-1, maxJobserverTokens)
	if _, err := w.Write(bytes.Repeat([]byte{jobserverToken}, tokens)); err != nil {
		return nil, errors.Join(fmt.Errorf("jobserver: preloading %d tokens: %w", tokens, err), r.Close(), w.Close())
	}
	return &Jobserver{r: r, w: w, tokens: tokens}, nil
}

// Close releases magus's ends of the pipe. Tokens a child still holds, or took and died
// holding, go with it: no later step reuses the pipe, so a lost token cannot shrink
// anyone's grant. Safe on a nil Jobserver.
func (j *Jobserver) Close() error {
	if j == nil {
		return nil
	}
	return errors.Join(j.r.Close(), j.w.Close())
}

// files is the pipe's two ends, read then write, for the child's ExtraFiles.
func (j *Jobserver) files() []*os.File { return []*os.File{j.r, j.w} }

// environ is the jobserver's half of a child's environment, given the environment the
// child would otherwise get and readFD, the descriptor files()[0] lands on in the child.
//
// CARGO_MAKEFLAGS is set as well because the jobserver crate reads it before MAKEFLAGS,
// so a stale one inherited from a cargo build script would otherwise win.
func (j *Jobserver) environ(env []string, readFD int) []string {
	return []string{
		"MAKEFLAGS=" + j.makeflags(lookupEnv(env, "MAKEFLAGS"), readFD),
		"CARGO_MAKEFLAGS=" + strings.Join(j.flags(readFD), " "),
	}
}

// flags names the pool at readFD and readFD+1 in every spelling a GNU make release
// reads. A bare -j because 3.81 takes -jN in MAKEFLAGS as a count forced on a submake
// and ignores the pipe; 4.x reads the last of --jobserver-fds and --jobserver-auth, and
// both name the same pipe.
func (j *Jobserver) flags(readFD int) []string {
	fds := strconv.Itoa(readFD) + "," + strconv.Itoa(readFD+1)
	return []string{"-j", "--jobserver-fds=" + fds, "--jobserver-auth=" + fds}
}

// makeflags replaces the job count and any jobserver in an inherited MAKEFLAGS with this
// pool at readFD, keeping every other flag. Words after a lone "--" are variable
// assignments ("-- CC=clang"), so the pool goes before them.
func (j *Jobserver) makeflags(inherited string, readFD int) string {
	words := strings.Fields(inherited)
	vars := len(words)
	for i, w := range words {
		if w == "--" {
			vars = i
			break
		}
	}
	kept := make([]string, 0, len(words)+3)
	for _, w := range words[:vars] {
		if !isJobsFlag(w) {
			kept = append(kept, w)
		}
	}
	kept = append(kept, j.flags(readFD)...)
	kept = append(kept, words[vars:]...)
	return strings.Join(kept, " ")
}

// isJobsFlag reports a MAKEFLAGS word that sets the job count or names a jobserver.
func isJobsFlag(w string) bool {
	for _, prefix := range []string{"-j", "--jobs", "--jobserver-auth=", "--jobserver-fds=", "--jobserver-style="} {
		if strings.HasPrefix(w, prefix) {
			return true
		}
	}
	return false
}

// lookupEnv returns name's value in env as exec.Cmd will resolve it: the last entry wins.
func lookupEnv(env []string, name string) string {
	prefix := name + "="
	for i := len(env) - 1; i >= 0; i-- {
		if v, ok := strings.CutPrefix(env[i], prefix); ok {
			return v
		}
	}
	return ""
}

type jobserverKey struct{}

// WithJobserver makes every process Exec starts under ctx a client of j. A nil j
// withdraws the pool ctx carried, for work that no longer holds the slots it was sized
// to.
func WithJobserver(ctx context.Context, j *Jobserver) context.Context {
	return context.WithValue(ctx, jobserverKey{}, j)
}

func jobserverFrom(ctx context.Context) *Jobserver {
	j, _ := ctx.Value(jobserverKey{}).(*Jobserver)
	return j
}

// SeatJobserver returns ctx carrying a pool sized to slots, and the function that closes
// it once the work holding those slots is done.
//
// Below two slots ctx carries no pool, and one it inherited is withdrawn. A one-slot
// step is every step that declares nothing, and a zero-token pool would serialize every
// cargo build those steps run today; declaring `slots` is what opts a step in. On a
// platform without the pipe form (see OpenJobserver) ctx carries none either, and
// children schedule themselves as they would with no magus above them.
func SeatJobserver(ctx context.Context, slots int) (context.Context, func(), error) {
	if slots < 2 {
		return WithJobserver(ctx, nil), func() {}, nil
	}
	j, err := OpenJobserver(slots)
	if errors.Is(err, errors.ErrUnsupported) {
		return WithJobserver(ctx, nil), func() {}, nil
	}
	if err != nil {
		return ctx, nil, err
	}
	return WithJobserver(ctx, j), func() { _ = j.Close() }, nil
}
