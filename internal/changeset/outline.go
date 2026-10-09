package changeset

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/egladman/magus/types"
)

// ErrOutline marks an outline refused as more than a few pointers. The contract is on
// [types.DiffOutline]; a message built on it says why and that the reply is the person's to type,
// because the agent that reads it is the one who tried to write more.
var ErrOutline = errors.New("an outline is not a reply")

// ErrNoSession reports that no review session is attached to the workspace, so there is nothing
// for an outline to be held with.
var ErrNoSession = errors.New("no review session is attached")

// NormalizeOutline returns o with its text trimmed, or an error wrapping [ErrOutline] when it
// breaks the bounds in [types.DiffOutline].
//
// Text is refused rather than stripped, so the agent learns the rule instead of having its words
// quietly rewritten into something it did not write. A topic or an agent name is one line of
// visible characters: control characters (a newline included), U+2028, U+2029, bidirectional
// overrides and every other format (Cf) rune are refused, since a renderer obeys them and a
// reader cannot see them.
func NormalizeOutline(o types.DiffOutline) (types.DiffOutline, error) {
	const typed = "the person types the reply, so an outline is only a few short pointers that they read"
	o.Thread = strings.TrimSpace(o.Thread)
	if o.Thread == "" {
		return types.DiffOutline{}, fmt.Errorf("%w: it names no thread; %s", ErrOutline, typed)
	}
	if len(o.Topics) == 0 {
		return types.DiffOutline{}, fmt.Errorf("%w: it has no topics; %s", ErrOutline, typed)
	}
	if len(o.Topics) > types.DiffOutlineMaxTopics {
		return types.DiffOutline{}, fmt.Errorf("%w: %d topics is more than %d; %s",
			ErrOutline, len(o.Topics), types.DiffOutlineMaxTopics, typed)
	}
	topics := make([]string, 0, len(o.Topics))
	for i, topic := range o.Topics {
		topic = strings.TrimSpace(topic)
		switch n := utf8.RuneCountInString(topic); {
		case topic == "":
			return types.DiffOutline{}, fmt.Errorf("%w: topic %d is empty; %s", ErrOutline, i+1, typed)
		case n > types.DiffOutlineMaxTopicRunes:
			return types.DiffOutline{}, fmt.Errorf("%w: topic %d is %d characters, more than %d; %s",
				ErrOutline, i+1, n, types.DiffOutlineMaxTopicRunes, typed)
		case !visibleLine(topic):
			return types.DiffOutline{}, fmt.Errorf("%w: topic %d spans lines or holds a character a reader cannot see; %s",
				ErrOutline, i+1, typed)
		}
		topics = append(topics, topic)
	}
	o.Topics = topics

	o.AgentName = strings.TrimSpace(o.AgentName)
	switch n := utf8.RuneCountInString(o.AgentName); {
	case n > types.DiffOutlineMaxAgentNameRunes:
		return types.DiffOutline{}, fmt.Errorf("%w: agent_name is %d characters, more than %d; %s",
			ErrOutline, n, types.DiffOutlineMaxAgentNameRunes, typed)
	case !visibleLine(o.AgentName):
		return types.DiffOutline{}, fmt.Errorf("%w: agent_name spans lines or holds a character a reader cannot see; %s",
			ErrOutline, typed)
	}
	return o, nil
}

// visibleLine reports whether s is one line with nothing in it a renderer would obey unseen.
func visibleLine(s string) bool {
	hidden := func(r rune) bool {
		return unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || r == ' ' || r == ' '
	}
	if strings.IndexFunc(s, hidden) >= 0 {
		return false
	}
	_, escaped := SanitizeBidi(s)
	return !escaped
}

// SetOutline records an agent's outline of the thread named by o.Thread, replacing the one it
// had, and returns the session. It returns [ErrNoSession] when none is attached, so a caller
// never reports an outline held that nobody will see.
//
// The outline is held in memory with the session and never written to disk, as agent comments
// are. It is normalized here, once, so no transport can store what [NormalizeOutline] refuses.
//
// The slice is rebuilt rather than appended to: clone shares it with every copy handed out
// earlier, and writing into a shared backing array would change an outline under a reader.
func (s *Store) SetOutline(workspaceRoot string, o types.DiffOutline) (*types.DiffReview, error) {
	o, err := NormalizeOutline(o)
	if err != nil {
		return nil, err
	}
	sess := s.mutate(workspaceRoot, func(sess *types.DiffReview) {
		next := make([]types.DiffOutline, 0, len(sess.Outlines)+1)
		for _, have := range sess.Outlines {
			if have.Thread != o.Thread {
				next = append(next, have)
			}
		}
		sess.Outlines = append(next, o)
	})
	if sess == nil {
		return nil, ErrNoSession
	}
	return sess, nil
}
