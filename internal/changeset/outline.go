package changeset

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/egladman/magus/types"
)

// ErrOutline marks an outline the person's reply cannot be built from. Its message says why and
// that the reply is theirs to type, because the agent that reads it is the one who tried to
// write more than a pointer.
var ErrOutline = errors.New("an outline is not a reply")

// CheckOutline validates and normalizes an agent's topics.
//
// The bounds are the contract. At most [types.DiffOutlineTopics] topics of at most
// [types.DiffOutlineTopicRunes] characters, one line each, because a topic able to hold a
// paragraph is a reply with extra steps, and the person is the one who types the reply.
// Surrounding space is trimmed; an empty topic, a control character (a newline included) or a
// bidirectional override is refused rather than stripped, so the agent learns the rule instead
// of having its text quietly rewritten into something it did not write.
func CheckOutline(topics []string) ([]string, error) {
	const typed = "the person types the reply, so an outline is only a few short pointers that they read"
	if len(topics) == 0 {
		return nil, fmt.Errorf("%w: it has no topics; %s", ErrOutline, typed)
	}
	if len(topics) > types.DiffOutlineTopics {
		return nil, fmt.Errorf("%w: %d topics is more than %d; %s",
			ErrOutline, len(topics), types.DiffOutlineTopics, typed)
	}
	out := make([]string, 0, len(topics))
	for i, topic := range topics {
		topic = strings.TrimSpace(topic)
		switch {
		case topic == "":
			return nil, fmt.Errorf("%w: topic %d is empty; %s", ErrOutline, i+1, typed)
		case utf8.RuneCountInString(topic) > types.DiffOutlineTopicRunes:
			return nil, fmt.Errorf("%w: topic %d is %d characters, more than %d; %s",
				ErrOutline, i+1, utf8.RuneCountInString(topic), types.DiffOutlineTopicRunes, typed)
		case strings.IndexFunc(topic, unicode.IsControl) >= 0:
			return nil, fmt.Errorf("%w: topic %d spans lines or holds a control character; %s", ErrOutline, i+1, typed)
		}
		if _, escaped := SanitizeBidi(topic); escaped {
			return nil, fmt.Errorf("%w: topic %d holds a bidirectional override; %s", ErrOutline, i+1, typed)
		}
		out = append(out, topic)
	}
	return out, nil
}

// SetOutline records an agent's outline of the conversation rooted at o.Thread, replacing the
// one it had, and returns the session, or nil when none is attached.
//
// The outline is held in memory with the session and never written to disk, as agent comments
// are. It is validated here as well as by callers, so no transport can store what
// [CheckOutline] refuses.
//
// The slice is rebuilt rather than appended to: clone shares it with every copy handed out
// earlier, and writing into a shared backing array would change an outline under a reader.
func (s *Store) SetOutline(root string, o types.DiffOutline) (*types.DiffReview, error) {
	topics, err := CheckOutline(o.Topics)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(o.Thread) == "" {
		return nil, fmt.Errorf("%w: it names no conversation", ErrOutline)
	}
	o.Topics = topics
	return s.mutate(root, func(sess *types.DiffReview) {
		next := make([]types.DiffOutline, 0, len(sess.Outlines)+1)
		for _, have := range sess.Outlines {
			if have.Thread != o.Thread {
				next = append(next, have)
			}
		}
		sess.Outlines = append(next, o)
	}), nil
}
