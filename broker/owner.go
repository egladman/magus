package broker

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
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
	if h.Claim != "" {
		h.Lease = sess.hello.Lease
		id := h.Claim + "/pgid " + strconv.Itoa(h.PGID)
		sess.holds[id] = &heldWork{hold: h, work: Work{
			Kind: WorkRun, Lease: h.Lease, PID: sess.hello.PID, Dir: sess.hello.Dir, Command: h.Command, Since: time.Now(),
		}}
		return id
	}
	slots := max(h.Slots, 1)
	id := s.budget.Assert(types.MachineClaim{
		Target: "job exec", Slots: slots, PID: sess.hello.PID, Dir: sess.hello.Dir, Command: h.Command,
	})
	hw := &heldWork{hold: h, claim: id, work: Work{
		Kind: WorkExec, Lease: h.Lease, Spawner: h.Spawner, PID: sess.hello.PID,
		Dir: cmp.Or(h.Root, sess.hello.Dir), Command: h.Command, Since: time.Now(), Slots: slots, Deadline: h.Deadline,
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
func (s *server) stopGroup(hw *heldWork, why string) {
	pgid := hw.hold.PGID
	if err := SignalGroup(pgid, false); err != nil {
		s.opts.log.DebugContext(s.ctx, "signal a held command's group", slog.Int("pgid", pgid), attr.Error(err))
	}
	time.AfterFunc(killGrace, func() {
		if groupAlive(pgid) {
			_ = SignalGroup(pgid, true)
		}
	})
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
// returns, once the group's leader has exited. Like a hold, it is not re-asserted on a
// broker that replaces this one.
func (c *Client) HoldGroup(ctx context.Context, claim string, pgid int, command string) (string, error) {
	c.mu.Lock()
	h, ok := c.held[claim]
	c.mu.Unlock()
	if !ok || h.remote == "" {
		return "", fmt.Errorf("broker: hold process group %d: this client holds no claim %q", pgid, claim)
	}
	return c.Hold(ctx, Hold{Claim: h.remote, PGID: pgid, Command: command})
}

// ReleaseHold says the held command ended on its own. A broker that is gone already
// released it with the connection.
func (c *Client) ReleaseHold(ctx context.Context, id string) {
	cn := c.live()
	if cn == nil || id == "" {
		return
	}
	_, _ = cn.call(ctx, c.next(), typeHoldRelease, holdReleaseRequest{ID: id})
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
