-- Type declarations for the vcs host module. Backed by vcs.Resolve at
-- runtime; active VCS is auto-detected from the working directory.

global record VCSMeta
   short_hash:  string
   hash:        string
   branch:      string
   commit_date: string
   is_dirty:    boolean
end

-- VCSPerson identifies who authored a revision.
global record VCSPerson
   name:  string
   email: string
end

-- VCSCommit is the normalized, VCS-agnostic record vcs.commit / vcs.history
-- return. Every field is meaningful for every VCS: id is the content/revision id
-- (git SHA, hg node, jj commit_id); date is RFC3339, when the revision was
-- recorded.
global record VCSCommit
   id:       string
   short:    string
   author:   VCSPerson
   date:     string
   subject:  string
   body:     string
   parents:  {string}
end

global record VCSModule
   -- name is the active VCS identifier: "git", "hg", "jj", or "" when
   -- VCS is disabled.
   name:        string
   -- base is the resolved base ref (e.g. "origin/main", "trunk()").
   base:        string
   root:        function(): string
   diff:        function(base: string): {string}
   short_hash:  function(): string
   hash:        function(): string
   branch:      function(): string
   commit_date: function(): string
   is_dirty:    function(): boolean
   metadata:    function(): VCSMeta
   -- commit resolves a revision (omit for the current revision) to its record;
   -- history returns up to limit recent commits, newest first.
   commit:      function(rev?: string): VCSCommit
   history:     function(limit: integer): {VCSCommit}
   -- exe is the absolute path to the active VCS executable, or "".
   exe:         function(): string
end

-- Access as: local vcs = require("magus.extra.vcs")
