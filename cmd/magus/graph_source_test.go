package main

import "testing"

func TestForgeBlobBase(t *testing.T) {
	cases := []struct {
		name, remote, branch, want string
	}{
		{"github ssh, empty branch -> main", "git@github.com:egladman/magus.git", "", "https://github.com/egladman/magus/blob/main"},
		{"github https", "https://github.com/egladman/magus.git", "main", "https://github.com/egladman/magus/blob/main"},
		{"github https, no .git, branch", "https://github.com/egladman/magus", "dev", "https://github.com/egladman/magus/blob/dev"},
		{"github ssh:// scheme", "ssh://git@github.com/egladman/magus.git", "main", "https://github.com/egladman/magus/blob/main"},
		{"detached HEAD -> main", "git@github.com:egladman/magus.git", "HEAD", "https://github.com/egladman/magus/blob/main"},
		{"github enterprise host", "git@github.example.com:team/repo.git", "trunk", "https://github.example.com/team/repo/blob/trunk"},
		{"gitlab not handled", "git@gitlab.com:group/repo.git", "main", ""},
		{"bitbucket not handled", "https://bitbucket.org/team/repo.git", "main", ""},
		{"unknown host", "git@example.com:o/r.git", "main", ""},
		{"missing repo", "git@github.com:owneronly", "main", ""},
		{"empty", "", "main", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := forgeBlobBase(c.remote, c.branch); got != c.want {
				t.Errorf("forgeBlobBase(%q, %q) = %q, want %q", c.remote, c.branch, got, c.want)
			}
		})
	}
}
