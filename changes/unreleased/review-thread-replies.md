### Changed

- **Review comments are grouped into threads.** `ReviewComment` (formerly `ReviewThread`) gains
  `root` (the thread id a reply belongs to), `outdated` and `diff_hunk`, which the GitHub review
  spell fills. Replies show under their thread in the terminal and the console, and Reply
  answers a thread. A spell that omits the new fields still decodes.
