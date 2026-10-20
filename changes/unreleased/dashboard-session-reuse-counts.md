### Fixed

- **The dashboard's Buzz session reuse counts only reused sessions.** It summed every checkout, cold ones included, while `session_pool_reuse` promises acquires served from an idle session.
