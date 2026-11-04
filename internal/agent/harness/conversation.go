//go:build e2e

package harness

import (
	"crypto/rand"
	"fmt"
	"strconv"
)

// conversation is one host session. A host that resumes keys later turns
// on id.
type conversation struct {
	id    string
	turns int
}

func newConversation() *conversation {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return &conversation{id: fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])}
}

func turnLabel(conv *conversation) string { return "turn" + strconv.Itoa(conv.turns+1) }

// conversationName picks one of the scripted sessions below.
type conversationName string

const (
	convTools    conversationName = "tools"
	convSpawn    conversationName = "spawn"
	convBrief    conversationName = "brief"
	convContinue conversationName = "continue"
)

// The child runs continueProbe with the answer appended, so the record of
// that call proves the answer reached the child.
const (
	continueProbe  = "e2e-continue-probe"
	continueAnswer = "teal"
)

// prompts is what the conversation sends h.
func (c conversationName) prompts(h *hostDriver) []string {
	if c == convBrief && h.briefTurns != nil {
		return h.briefTurns
	}
	return conversations[c]
}

// The prompts are scripts, not tasks. Each step names the tool to use, so
// the hook it should fire is not left to the model's choice.
var conversations = map[conversationName][]string{
	convTools: {`This is an automated test of tool hooks. Use your tools for each numbered step, in order. If a step is refused, or a hook adds a note, do not retry it; go on to the next step.
1. Run the shell command: echo e2e-deny-probe
2. Run the shell command: echo e2e-advise-probe
3. Read the file probe.txt with your file-read tool; use the shell only if you have no such tool.
4. Create the file gen/out.txt containing the line "hand edit", with your file-editing tool (write, edit or patch), not the shell.
5. Create the file blocked.txt containing the line "blocked", with your file-editing tool (write, edit or patch), not the shell.
6. Call the status tool of the magus MCP server once.
Finally reply with one line listing every code word (shaped like E2E-WORD-XXXXXXXX) that any refusal, error or hook note showed you. Context: we are checking this workspace's module boundary.`},
	convSpawn: {`This is an automated test of subagent hooks.
1. Load the skill named magus-multi-agent with your skill tool. With no skill tool, read its SKILL.md under .claude/skills, .agents/skills, .cursor/skills or .opencode/skills.
2. Start exactly one subagent with your subagent tool. Its description (title) is: e2e/probe ` + jobID + `
Its prompt is exactly these two lines:
lease: ` + jobID + `
Reply with the single word OK and use no tools.
3. Wait for the subagent to finish. If the spawn is refused, do not retry it.
Finally reply with one line listing every code word (shaped like E2E-WORD-XXXXXXXX) that any refusal, error or hook note showed you.`},
	convBrief: {briefQuestion},
	convContinue: {`This is an automated test of continuing a subagent.
1. Start exactly one subagent with your subagent tool. Its description (title) is: e2e/continue ` + jobID + `
Its prompt is exactly these lines:
lease: ` + jobID + `
Use no tools yet. Reply with only the question "Which colour?" and stop. When the answer arrives, run the shell command: echo ` + continueProbe + `-<the answer>
Then reply with the single word OK.
2. Wait for the subagent to stop with its question. Continue that same subagent with your tool for continuing one, and answer: ` + continueAnswer + `
Do not start a second subagent, and do not run the command yourself.
3. Wait for the subagent to finish.
Finally reply with the single word OK.`},
}

const briefQuestion = `Use no tools. Your context may hold a magus brief that lists the leases live in this checkout. Reply with the code word shaped like E2E-BRIEF-XXXXXXXX from that list, or NONE if you have no such brief.`
