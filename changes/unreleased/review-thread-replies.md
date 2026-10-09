### Changed

- **A review comment now says which conversation it belongs to.** `ReviewThread` gains
  `root` (the id of the conversation's first comment, empty on that comment), `outdated`
  (the commented line no longer exists in the head) and `diff_hunk` (the host's hunk text
  the comment was made on). The GitHub review spell fills them from `in_reply_to_id`, a
  null `line` and `diff_hunk`. Comments stay one record each, so a new reply to an old
  conversation still counts as unseen. A review spell that omits the three fields keeps
  decoding.
