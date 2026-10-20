package broker

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/log/attr"
	"github.com/egladman/magus/libs/xdg"
	"github.com/egladman/magus/types"
)

// Hold is a process group a client hands the broker to watch: a command `magus job exec
// --timeout` runs under a deadline, or a group a step started under a budget claim. The
// broker stops the whole group when its deadline passes or its claim is released, or
// when the client's connection closes while the group still runs, since nothing is left
// then to wait on it.
type Hold struct {
	// Lease is the job lease the command was started under, "" for none.
	Lease string `json:"lease,omitempty"`
	// Spawner is the magus.spawner label the client's BAGGAGE carried, for a person to read.
	Spawner string `json:"spawner,omitempty"`
	// PGID is the process group the command leads.
	PGID    int    `json:"pgid"`
	Command string `json:"command"`
	// Root is the workspace the command ran in; an ending is filed in its attention queue.
	Root     string    `json:"root,omitempty"`
	Slots    int       `json:"slots,omitzero"`
	Deadline time.Time `json:"deadline,omitzero"`
	// Claim is the budget claim, on the same connection, of the step that started the
	// group. The group then charges nothing of its own and needs no deadline: it ends
	// with the claim.
	Claim string `json:"claim,omitempty"`
	// Held is when the client first handed the group over, while its leader still ran;
	// zero is when the broker records it. A process that started after it leads another
	// group under a reused id, so the broker never signals it.
	Held time.Time `json:"held,omitzero"`
}

// WorkKind is what kind of thing a [Work] is.
type WorkKind string

const (
	// WorkRun is a magus process holding budget claims: a `magus run` and the steps it seats.
	WorkRun WorkKind = "run"
	// WorkExec is a command `magus job exec --timeout` runs under a deadline.
	WorkExec WorkKind = "exec"
)

// Work is one thing running against the broker and who owns it.
type Work struct {
	Kind     WorkKind  `json:"kind"`
	Lease    string    `json:"lease,omitempty"`
	Spawner  string    `json:"spawner,omitempty"`
	PID      int       `json:"pid"`
	Dir      string    `json:"dir,omitempty"`
	Command  string    `json:"command,omitempty"`
	Since    time.Time `json:"since"`
	Slots    int       `json:"slots,omitzero"`
	Deadline time.Time `json:"deadline,omitzero"`
}

// Owner names who answers for w: its lease, else the spawner its BAGGAGE named, else
// "unleased" (a person, or a root session, which no lease bounds).
func (w Work) Owner() string {
	switch {
	case w.Lease != "":
		return w.Lease
	case w.Spawner != "":
		return "spawned by " + w.Spawner
	default:
		return "unleased"
	}
}

// Ended is work the broker stopped, why, and when.
type Ended struct {
	Work
	Reason string    `json:"reason"`
	At     time.Time `json:"at"`
}

// WorkReport is what runs against a broker, and what it stopped lately.
type WorkReport struct {
	Running []Work  `json:"running,omitempty"`
	Ended   []Ended `json:"ended,omitempty"`
}

// SeatWait is why a run waits: its job tree already has MaxValidations runs going.
type SeatWait struct {
	Tree    string `json:"tree"`
	Max     int    `json:"max"`
	Holders []Work `json:"holders,omitempty"`
}

func (w SeatWait) String() string {
	held := make([]string, 0, len(w.Holders))
	for _, h := range w.Holders {
		held = append(held, fmt.Sprintf("%s (pid %d, %s, in %s)", h.Owner(), h.PID, h.Command, h.Dir))
	}
	return fmt.Sprintf("waiting for job tree %s's validation seat: jobs.max_validations is %d, and it is held by %s",
		w.Tree, w.Max, cmp.Or(strings.Join(held, "; "), "a run that is ending"))
}

// endedKeep bounds the endings a broker remembers for status.
const endedKeep = 32

// killGrace is how long a group asked to stop has before it is killed. A var so a test
// can shorten it.
var killGrace = 10 * time.Second

// heldWork is a hold as a session keeps it.
type heldWork struct {
	hold    Hold
	work    Work
	claim   string
	timer   *time.Timer
	stopped bool
}

// holdLocked records h for sess, charges its slots to the budget unasked (the command
// already runs), and arms its deadline. A group riding a claim is only recorded. Callers
// hold s.mu.
func (s *server) holdLocked(sess *session, h Hold) string {
	if now := time.Now(); h.Held.IsZero() || h.Held.After(now) {
		h.Held = now
	}
	if h.Claim != "" {
		h.Lease = sess.hello.Lease
		id := h.Claim + "/pgid " + strconv.Itoa(h.PGID)
		sess.holds[id] = &heldWork{hold: h, work: Work{
			Kind: WorkRun, Lease: h.Lease, PID: sess.hello.PID, Dir: sess.hello.Dir, Command: h.Command, Since: h.Held,
		}}
		return id
	}
	slots := max(h.Slots, 1)
	id := s.budget.Assert(types.MachineClaim{
		Target: "job exec", Slots: slots, PID: sess.hello.PID, Dir: sess.hello.Dir, Command: h.Command,
	})
	hw := &heldWork{hold: h, claim: id, work: Work{
		Kind: WorkExec, Lease: h.Lease, Spawner: h.Spawner, PID: sess.hello.PID,
		Dir: cmp.Or(h.Root, sess.hello.Dir), Command: h.Command, Since: h.Held, Slots: slots, Deadline: h.Deadline,
	}}
	hw.timer = time.AfterFunc(time.Until(h.Deadline), func() { s.expire(sess, id) })
	sess.holds[id] = hw
	return id
}

// releaseHold drops a hold whose command ended on its own.
func (s *server) releaseHold(sess *session, id string) {
	s.mu.Lock()
	hw, ok := sess.holds[id]
	delete(sess.holds, id)
	s.mu.Unlock()
	if !ok || hw.claim == "" {
		return
	}
	hw.timer.Stop()
	s.budget.Release(hw.claim)
	s.noteRelease()
}

// expire stops a held command whose deadline passed.
func (s *server) expire(sess *session, id string) {
	s.mu.Lock()
	hw, ok := sess.holds[id]
	if !ok || hw.stopped {
		s.mu.Unlock()
		return
	}
	hw.stopped = true
	s.mu.Unlock()
	s.stopGroup(hw, hw.hold.DeadlineReason())
}

// reapOrphan stops a held group its connection left behind, and returns its slots.
func (s *server) reapOrphan(hw *heldWork) {
	if hw.claim == "" {
		s.stopIfAlive(hw, "outlived the magus run whose step started it")
		return
	}
	hw.timer.Stop()
	s.stopIfAlive(hw, "outlived the magus job exec that ran it")
	s.budget.Release(hw.claim)
}

// stopRiders stops the groups riding claim that still run: the step that started them
// handed its claim back, so nothing waits on them any more.
func (s *server) stopRiders(sess *session, claim string) {
	var riders []*heldWork
	s.mu.Lock()
	for id, hw := range sess.holds {
		if hw.hold.Claim == claim {
			riders = append(riders, hw)
			delete(sess.holds, id)
		}
	}
	s.mu.Unlock()
	for _, hw := range riders {
		s.stopIfAlive(hw, "outlived the step whose claim it rode")
	}
}

// stopIfAlive stops hw's group once, if anything in it still runs.
func (s *server) stopIfAlive(hw *heldWork, why string) {
	s.mu.Lock()
	stop := !hw.stopped && groupAlive(hw.hold.PGID)
	hw.stopped = hw.stopped || stop
	s.mu.Unlock()
	if stop {
		s.stopGroup(hw, why)
	}
}

// stopGroup asks hw's process group to stop, kills what is left after killGrace, and
// records the ending once: in this broker's memory and in the workspace's attention queue.
// Each signal goes only to the group hw held (see [checkLeader]).
func (s *server) stopGroup(hw *heldWork, why string) {
	pgid := hw.hold.PGID
	if err := checkLeader(pgid, hw.hold.Held); err != nil {
		s.leaveGroup(hw, why, err)
		return
	}
	if err := SignalGroup(pgid, false); err != nil {
		s.opts.log.DebugContext(s.ctx, "signal a held command's group", slog.Int("pgid", pgid), attr.Error(err))
	}
	time.AfterFunc(killGrace, func() { s.killGroup(hw, why) })
	s.mu.Lock()
	s.ended = append(s.ended, Ended{Work: hw.work, Reason: why, At: time.Now()})
	if over := len(s.ended) - endedKeep; over > 0 {
		s.ended = slices.Delete(s.ended, 0, over)
	}
	s.mu.Unlock()
	s.opts.log.InfoContext(s.ctx, "stopped a held command", slog.Int("pgid", pgid),
		slog.String("owner", hw.work.Owner()), slog.String("why", why))
	job.NoteEnded(s.ctx, hw.hold.Root, hw.hold.Lease, EndedMessage(hw.hold, why))
}

// killGroup kills what is left of hw's group once its grace passed.
func (s *server) killGroup(hw *heldWork, why string) {
	pgid := hw.hold.PGID
	if !groupAlive(pgid) {
		return
	}
	if err := checkLeader(pgid, hw.hold.Held); err != nil {
		s.leaveGroup(hw, why, err)
		return
	}
	if err := SignalGroup(pgid, true); err != nil {
		s.opts.log.DebugContext(s.ctx, "kill a held command's group", slog.Int("pgid", pgid), attr.Error(err))
	}
}

// leaveGroup leaves hw's group running, because err says it may not be the group hw
// held. A group that took a freed id is no business of magus; one magus cannot tell is
// said, in the log and in the workspace's attention queue, unless nothing is left of it.
func (s *server) leaveGroup(hw *heldWork, why string, err error) {
	pgid := hw.hold.PGID
	switch {
	case errors.Is(err, errGroupReused):
		s.opts.log.InfoContext(s.ctx, "left alone a group that took a held group's id", slog.Int("pgid", pgid),
			slog.String("owner", hw.work.Owner()), attr.Error(err))
		return
	case errors.Is(err, errLeaderGone) && !groupAlive(pgid):
		return
	}
	s.opts.log.WarnContext(s.ctx, "left a held command running: magus cannot tell it is still the group it held",
		slog.Int("pgid", pgid), slog.String("owner", hw.work.Owner()), slog.String("why", why), attr.Error(err))
	job.NoteEnded(s.ctx, hw.hold.Root, hw.hold.Lease, fmt.Sprintf(
		"magus left `%s` (pid group %d, owner %s) running: it %s, but %v, so magus cannot tell it is still that group",
		hw.hold.Command, pgid, hw.work.Owner(), why, err))
}

var (
	// errGroupReused is a held group's id naming another group.
	errGroupReused = errors.New("another group took its id")
	// errLeaderGone is a held group whose leader exited, which leaves no process whose
	// start time says whose group it is.
	errLeaderGone = errors.New("its leader has exited")
)

// checkLeader returns nil when process group pgid is still the group a hold made at held
// names: its leader runs, leads it, and started no later than held. An id is free again
// only once its group empties, so the leader of a group that reused it started after
// that. An error wrapping errGroupReused means another group has the id; any other
// means magus cannot tell, and must not signal it.
func checkLeader(pgid int, held time.Time) error {
	started, group, err := inspectProcess(pgid)
	if err != nil {
		return err
	}
	if group != pgid {
		return fmt.Errorf("its leader, process %d, moved to group %d", pgid, group)
	}
	if started.After(held) {
		return fmt.Errorf("%w: process %d started at %s, after the hold at %s", errGroupReused, pgid,
			started.UTC().Format(time.RFC3339), held.UTC().Format(time.RFC3339))
	}
	return nil
}

// inspectTimeout bounds one ps run on darwin.
const inspectTimeout = 5 * time.Second

// inspectProcess is when process pid started, to the second at worst, and the process
// group it is in. The error wraps errLeaderGone when no process pid exists.
func inspectProcess(pid int) (started time.Time, group int, err error) {
	switch runtime.GOOS {
	case "linux":
		return inspectLinuxProcess(pid)
	case "darwin":
		return inspectDarwinProcess(pid)
	default:
		return time.Time{}, 0, fmt.Errorf("magus cannot read when a process started on %s", runtime.GOOS)
	}
}

// inspectLinuxProcess reads /proc/<pid>/stat: the group is its fifth field and the
// start, in clock ticks after boot, its twenty-second.
func inspectLinuxProcess(pid int) (time.Time, int, error) {
	stat, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if errors.Is(err, fs.ErrNotExist) {
		return time.Time{}, 0, errLeaderGone
	}
	if err != nil {
		return time.Time{}, 0, err
	}
	// The second field is the command name in parentheses, which may hold spaces and
	// parentheses of its own, so the rest is counted from the last ')'.
	end := bytes.LastIndexByte(stat, ')')
	fields := strings.Fields(string(stat[end+1:]))
	if end < 0 || len(fields) < 20 {
		return time.Time{}, 0, fmt.Errorf("read process %d: unexpected /proc stat %q", pid, stat)
	}
	group, err := strconv.Atoi(fields[2])
	if err != nil {
		return time.Time{}, 0, fmt.Errorf("read process %d's group: %w", pid, err)
	}
	ticks, err := strconv.ParseInt(fields[19], 10, 64)
	if err != nil {
		return time.Time{}, 0, fmt.Errorf("read process %d's start: %w", pid, err)
	}
	boot, err := linuxBootTime()
	if err != nil {
		return time.Time{}, 0, err
	}
	// The ticks are USER_HZ, which is 100 on every architecture Go builds Linux for.
	return boot.Add(time.Duration(ticks) * (time.Second / 100)), group, nil
}

// linuxBootTime is the btime line of /proc/stat, in whole seconds.
func linuxBootTime() (time.Time, error) {
	stat, err := os.ReadFile("/proc/stat")
	if err != nil {
		return time.Time{}, err
	}
	for line := range strings.Lines(string(stat)) {
		if rest, ok := strings.CutPrefix(line, "btime "); ok {
			secs, err := strconv.ParseInt(strings.TrimSpace(rest), 10, 64)
			if err != nil {
				return time.Time{}, fmt.Errorf("read the boot time: %w", err)
			}
			return time.Unix(secs, 0), nil
		}
	}
	return time.Time{}, errors.New("read the boot time: /proc/stat has no btime line")
}

// inspectDarwinProcess asks ps, whose lstart is the start to the second; UTC and the C
// locale make it parse the same everywhere.
func inspectDarwinProcess(pid int) (time.Time, int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), inspectTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/bin/ps", "-o", "pgid=", "-o", "lstart=", "-p", strconv.Itoa(pid))
	cmd.Env = append(os.Environ(), "LC_ALL=C", "TZ=UTC")
	out, err := cmd.Output()
	fields := strings.Fields(string(out))
	var exit *exec.ExitError
	switch {
	case len(fields) == 0 && errors.As(err, &exit) && exit.ExitCode() == 1:
		return time.Time{}, 0, errLeaderGone
	case err != nil:
		return time.Time{}, 0, fmt.Errorf("ps -p %d: %w", pid, err)
	case len(fields) < 2:
		return time.Time{}, 0, fmt.Errorf("ps -p %d: unexpected output %q", pid, out)
	}
	group, err := strconv.Atoi(fields[0])
	if err != nil {
		return time.Time{}, 0, fmt.Errorf("ps -p %d: read the group: %w", pid, err)
	}
	started, err := time.ParseInLocation("Mon Jan 2 15:04:05 2006", strings.Join(fields[1:], " "), time.UTC)
	if err != nil {
		return time.Time{}, 0, fmt.Errorf("ps -p %d: read the start: %w", pid, err)
	}
	return started, group, nil
}

// DeadlineReason is why a hold stopped at its deadline, as [EndedMessage] takes it.
func (h Hold) DeadlineReason() string {
	return fmt.Sprintf("passed its deadline (%s)", h.Deadline.UTC().Format(time.RFC3339))
}

// EndedMessage is the attention row for a held command magus stopped. One function, so
// the broker and the client stopping the same command at its deadline file one row.
func EndedMessage(h Hold, why string) string {
	return fmt.Sprintf("magus stopped `%s` (pid group %d, owner %s): it %s",
		h.Command, h.PGID, Work{Lease: h.Lease, Spawner: h.Spawner}.Owner(), why)
}

// work is what every connection runs, and what this broker stopped lately.
func (s *server) work() WorkReport {
	s.mu.Lock()
	defer s.mu.Unlock()
	var r WorkReport
	for _, sess := range s.conns {
		if sess == nil {
			continue
		}
		for _, hw := range sess.holds {
			// A group riding a claim is part of the run listed below.
			if hw.hold.Claim == "" {
				r.Running = append(r.Running, hw.work)
			}
		}
		if len(sess.claims) == 0 {
			continue
		}
		slots := 0
		for _, n := range sess.claims {
			slots += n
		}
		r.Running = append(r.Running, s.runLocked(sess, slots))
	}
	slices.SortFunc(r.Running, func(a, b Work) int { return a.Since.Compare(b.Since) })
	r.Ended = slices.Clone(s.ended)
	return r
}

// runLocked is sess as a run. Callers hold s.mu.
func (s *server) runLocked(sess *session, slots int) Work {
	return Work{
		Kind: WorkRun, Lease: sess.hello.Lease, PID: sess.hello.PID, Dir: sess.hello.Dir,
		Command: strings.Join(sess.hello.Argv, " "), Since: sess.since, Slots: slots,
	}
}

// seatLocked seats sess's run in its job tree's validations, or returns why it waits.
// A connection with no tree or no cap, or one already seated, is not counted again;
// force seats it whatever the cap, for a claim re-asserted on a new broker, whose step
// is already running. Callers hold s.mu.
//
// The seat is the RUN's (see [types.MachineClaim.Run]), so a nested magus under a
// seated run shares its seat rather than waiting on its own parent, and it is held until
// the connection closes, so a run between two steps does not lose it to another.
func (s *server) seatLocked(sess *session, c types.MachineClaim, force bool) *SeatWait {
	h := sess.hello
	if h.Tree == "" || h.MaxValidations <= 0 || sess.seat != "" {
		return nil
	}
	run := cmp.Or(c.Run(), "pid "+strconv.Itoa(h.PID))
	held := s.seats[h.Tree]
	others := 0
	for r := range held {
		if r != run {
			others++
		}
	}
	if !force && others >= h.MaxValidations {
		wait := &SeatWait{Tree: h.Tree, Max: h.MaxValidations}
		for _, other := range s.conns {
			if other != nil && other.seat != "" && other.seat != run && other.hello.Tree == h.Tree {
				wait.Holders = append(wait.Holders, s.runLocked(other, 0))
			}
		}
		return wait
	}
	if held == nil {
		held = map[string]int{}
		s.seats[h.Tree] = held
	}
	held[run]++
	sess.seat = run
	return nil
}

// unseatLocked returns sess's validation seat. Callers hold s.mu.
func (s *server) unseatLocked(sess *session) {
	if sess.seat == "" {
		return
	}
	tree := sess.hello.Tree
	if held := s.seats[tree]; held != nil {
		if held[sess.seat]--; held[sess.seat] <= 0 {
			delete(held, sess.seat)
		}
		if len(held) == 0 {
			delete(s.seats, tree)
		}
	}
	sess.seat = ""
}

// Hold hands the broker a command this process started under a deadline (see [Hold]),
// starting a broker when none answers, and returns the id [Client.ReleaseHold] takes.
// The hold rides this connection and is not re-asserted on a broker that replaces this
// one: the caller enforces the deadline itself too.
func (c *Client) Hold(ctx context.Context, h Hold) (string, error) {
	cn, err := c.open(ctx)
	if err != nil {
		return "", err
	}
	var reply holdReply
	if err := cn.roundTrip(ctx, c.next(), typeHold, holdRequest{Hold: h}, typeHoldReply, &reply, nil); err != nil {
		return "", err
	}
	return reply.ID, nil
}

var _ cache.GroupHolder = (*Client)(nil)

// HoldGroup hands the broker process group pgid, which a step holding claim (an id
// [Client.Request] granted) started, so the group ends with the claim: when it is
// released, or when this connection closes. [Client.ReleaseHold] takes the id it
// returns, once the group's leader has exited. Unlike a hold, it is re-asserted with its
// claim on a broker that replaces this one. A group the broker stops is filed in the
// attention queue of the workspace ctx carries (see [types.WithWorkspace]); with none,
// nowhere.
func (c *Client) HoldGroup(ctx context.Context, claim string, pgid int, command string) (string, error) {
	hold := Hold{PGID: pgid, Command: command, Held: time.Now()}
	if ws := types.WorkspaceFromContext(ctx); ws != nil {
		hold.Root = ws.Root()
	}
	id := claim + "/pgid " + strconv.Itoa(pgid)
	r := &heldRider{claim: claim, hold: hold}
	c.mu.Lock()
	h, ok := c.held[claim]
	if ok {
		hold.Claim = h.remote
		// Recorded before it is sent, so a broker that replaces this one mid-request is
		// handed it too; the broker keys it by claim and group, so twice is once.
		c.riders[id] = r
	}
	c.mu.Unlock()
	if hold.Claim == "" {
		return "", fmt.Errorf("broker: hold process group %d: this client holds no claim %q", pgid, claim)
	}
	remote, err := c.Hold(ctx, hold)
	c.mu.Lock()
	defer c.mu.Unlock()
	if err != nil {
		if c.riders[id] == r {
			delete(c.riders, id)
		}
		return "", err
	}
	r.remote = remote
	return id, nil
}

// ReleaseHold says the held command ended on its own. A broker that is gone already
// released it with the connection, so that returns nil. An error is a release the broker
// did not answer: it still holds the group then, and signals it only while it is the
// group it held.
func (c *Client) ReleaseHold(ctx context.Context, id string) error {
	remote := id
	c.mu.Lock()
	if r, ok := c.riders[id]; ok {
		remote = r.remote
		delete(c.riders, id)
	}
	c.mu.Unlock()
	cn := c.live()
	if cn == nil || remote == "" {
		return nil
	}
	if err := cn.roundTrip(ctx, c.next(), typeHoldRelease, holdReleaseRequest{ID: remote}, typeHoldReply, nil, nil); err != nil {
		return fmt.Errorf("release hold %s: %w", id, err)
	}
	return nil
}

// Work is what runs against the broker, by owner, and what it stopped lately.
func (c *Client) Work(ctx context.Context) (WorkReport, error) {
	cn, err := c.connect(ctx)
	if err != nil {
		return WorkReport{}, err
	}
	var r WorkReport
	err = cn.roundTrip(ctx, c.next(), typeWork, nil, typeWorkReply, &r, nil)
	return r, err
}

// QueryWork dials the broker at addr, reads [Client.Work] and hangs up. The error wraps
// ErrUnavailable when none answers.
func QueryWork(ctx context.Context, addr string) (WorkReport, error) {
	c, err := Dial(ctx, addr)
	if err != nil {
		return WorkReport{}, err
	}
	defer func() { _ = c.Close() }()
	return c.Work(ctx)
}

// noteSeat says, once per wait, which runs hold the seat this client waits for.
func (c *Client) noteSeat(ctx context.Context, wait *SeatWait) {
	if wait == nil {
		c.seatWaiting.Store(false)
		return
	}
	if c.seatWaiting.CompareAndSwap(false, true) {
		slog.InfoContext(ctx, wait.String(), attr.Notice("broker"))
	}
}

// resolveScope fills the hello's job tree and validation cap from the workspace the
// client runs in, once, for a client acting under a lease. A failure is the caller's
// misconfiguration and refuses the run with exit 78: guessing a cap would either
// serialize what the workspace allows or run what it forbids.
func (c *Client) resolveScope() error {
	c.scopeOnce.Do(func() {
		if c.hello.Lease == "" {
			return
		}
		c.hello.Tree, c.hello.MaxValidations, c.scopeErr = validationScope(c.hello.Dir, c.hello.Lease)
		if c.scopeErr != nil {
			c.scopeErr = types.ExitError{Code: cache.ExitCodeMachineDeclaration, Err: fmt.Errorf(
				"broker: cannot tell how many runs job %s's tree may have going: %w", c.hello.Lease, c.scopeErr)}
		}
	})
	return c.scopeErr
}

// validationScope is the root of lease's job tree and the jobs.max_validations of the
// workspace holding dir. Workspace config only, as every jobs rule that acts reads it. A
// dir in no workspace caps at the default, per lease.
func validationScope(dir, lease string) (tree string, maxValidations int, err error) {
	root := workspaceRoot(dir)
	cfg, err := config.LoadWorkspaceOnly(root)
	if err != nil {
		return "", 0, err
	}
	if cfg.Jobs.MaxValidations < 0 {
		return "", 0, fmt.Errorf("jobs.max_validations is %d in %s: it counts runs, so it is 0 (no cap) or more",
			cfg.Jobs.MaxValidations, filepath.Join(root, config.Filename))
	}
	if root == "" {
		return lease, cfg.Jobs.MaxValidations, nil
	}
	stateBase, err := xdg.StateDir()
	if err != nil {
		return "", 0, fmt.Errorf("resolve the user state dir that holds the job store: %w", err)
	}
	tree, err = job.NewStore(job.Location{Root: root, StateBase: stateBase}).TreeRoot(lease)
	if err != nil {
		return "", 0, err
	}
	return tree, cfg.Jobs.MaxValidations, nil
}

// workspaceRoot is the nearest directory at or above dir that declares a workspace with
// a magus.yaml, "" when none does.
func workspaceRoot(dir string) string {
	for cur := dir; cur != ""; {
		for _, name := range []string{config.Filename, config.DottedFilename} {
			if _, err := os.Stat(filepath.Join(cur, name)); err == nil {
				return cur
			}
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return ""
		}
		cur = parent
	}
	return ""
}
