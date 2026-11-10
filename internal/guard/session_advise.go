package guard

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/egladman/magus/internal/agent"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/log/attr"
	"github.com/egladman/magus/internal/sessions"
	"github.com/egladman/magus/types"
)

// denyRulePersonAdvisory names a stop the guard holds to show a person's
// directive. A host glue reads it to drop the guard's own label from the
// text, because the words are the person's, not the guard's.
const denyRulePersonAdvisory denyRuleName = "person-advisory"

// showDirectives returns the directive for the open advisories a root
// session has not been shown, recording the showing. note names a store it
// could not use, so an unreadable store never reads as nothing open.
func showDirectives(ctx context.Context, at location, who hookAttribution) (directive, note string) {
	if who.Agent != "" || who.Session == "" || at.workspace == "" {
		return "", ""
	}
	store, err := sessions.OpenDirectiveStore(at.workspace)
	if err != nil {
		return "", directiveNote(err)
	}
	showing := sessions.Recipient{Session: who.Session, Host: who.Host, Event: who.Event}
	got, err := store.Deliver(ctx, showing, time.Now())
	if err != nil {
		return "", directiveNote(err)
	}
	return sessions.DirectiveText(got), ""
}

func directiveNote(err error) string {
	return fmt.Sprintf("magus workspace: the person's advisories could not be read, so none was shown: %s", err)
}

// adviseMessage answers a person's prompt. The prompt carries an open
// advisory only where the wiring renders context; elsewhere the stop does.
func adviseMessage(ctx context.Context, at location, who hookAttribution, rendersContext bool) types.GuardJudgment {
	v := types.GuardJudgment{SchemaVersion: agent.GuardSchemaVersion, Decision: decisionPass}
	if !rendersContext {
		return v
	}
	directive, note := showDirectives(ctx, at, who)
	if text := strings.TrimSpace(strings.Join([]string{directive, note}, "\n\n")); text != "" {
		v.Decision, v.Context = decisionAdvise, text
	}
	return v
}

// endsRootTurn reports a root session ending its turn. Cursor names no agent
// on a subagent's stop, so the event name decides, not the agent id alone.
func endsRootTurn(raw string, who hookAttribution) bool {
	var env struct {
		Event string `json:"hook_event_name"`
		Agent string `json:"agent_id"`
	}
	if _, ok := decodeStop(raw); !ok || json.Unmarshal([]byte(raw), &env) != nil {
		return false
	}
	return env.Event == "Stop" && env.Agent == "" && who.Agent == ""
}

// adviseStop holds a root stop that v lets through, to show the session the
// advisories it has not seen. A stop v already holds shows them next time.
func adviseStop(ctx context.Context, at location, who hookAttribution, raw string, v types.GuardJudgment) types.GuardJudgment {
	if v.Decision == decisionDeny || !endsRootTurn(raw, who) {
		return v
	}
	directive, note := showDirectives(ctx, at, who)
	if note != "" {
		v.Context = strings.TrimPrefix(strings.Join([]string{v.Context, note}, "\n"), "\n")
	}
	if directive == "" {
		return v
	}
	v.Decision, v.Reason, v.Rule = decisionDeny, directive, string(denyRulePersonAdvisory)
	return v
}

// recordDirectiveAct counts command toward each directive whose --expect it
// matches. A subagent's command counts for its root's session. It is a
// measure, never a verdict, so a failure is only logged.
func recordDirectiveAct(ctx context.Context, at location, who hookAttribution, command string) {
	if who.Session == "" || at.workspace == "" {
		return
	}
	store, err := sessions.OpenDirectiveStore(at.workspace)
	if err == nil {
		showing := sessions.Recipient{Session: who.Session, Host: who.Host, Event: who.Event}
		err = store.RecordActed(ctx, showing, command, time.Now())
	}
	if err != nil {
		slog.WarnContext(ctx, "an advisory's action was not counted", attr.Error(err), attr.Component("magus shell"))
	}
}
