package gitmeta

import (
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

const config = `[core]
	bare = false
[remote "upstream"]
	url = https://github.com/other/fork.git
[remote "origin"]
	fetch = +refs/heads/*:refs/remotes/origin/*
	url = "git@github.com:Flex/Stage.git"
`

func TestOriginRepoPlainCheckout(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, ".git", "config"), config)
	if got := OriginRepo(dir); got != "github.com/Flex/Stage" {
		t.Fatalf("got %q", got)
	}
}

func TestOriginRepoLinkedWorktree(t *testing.T) {
	root := t.TempDir()
	main := filepath.Join(root, "repo")
	write(t, filepath.Join(main, ".git", "config"), config)
	write(t, filepath.Join(main, ".git", "worktrees", "wt", "commondir"), "../..\n")
	wt := filepath.Join(root, "wt")
	write(t, filepath.Join(wt, ".git"), "gitdir: ../repo/.git/worktrees/wt\n")
	if got := OriginRepo(wt); got != "github.com/Flex/Stage" {
		t.Fatalf("got %q", got)
	}
}

func TestOriginRepoAbsentOrUnusable(t *testing.T) {
	none := t.TempDir()
	noOrigin := t.TempDir()
	write(t, filepath.Join(noOrigin, ".git", "config"), "[remote \"upstream\"]\n\turl = https://h/o/r\n")
	local := t.TempDir()
	write(t, filepath.Join(local, ".git", "config"), "[remote \"origin\"]\n\turl = /srv/git/repo.git\n")
	file := t.TempDir()
	write(t, filepath.Join(file, ".git", "config"), "[remote \"origin\"]\n\turl = file:///srv/git/repo.git\n")
	for _, dir := range []string{none, noOrigin, local, file, filepath.Join(none, "missing")} {
		if got := OriginRepo(dir); got != "" {
			t.Errorf("%s: got %q", dir, got)
		}
	}
}

func TestRemoteIdentity(t *testing.T) {
	for raw, want := range map[string]string{
		"git@host:owner/repo.git":               "host/owner/repo",
		"https://user:secret@Host.com/o/r.git/": "host.com/o/r",
		"ssh://git@host:22/o/r":                 "host:22/o/r",
		"/abs/path":                             "",
		"file:///abs/path":                      "",
		"":                                      "",
	} {
		if got := RemoteIdentity(raw); got != want {
			t.Errorf("%q: got %q want %q", raw, got, want)
		}
	}
	if RemoteIdentity("git@host:owner/repo.git") != RemoteIdentity("https://host/owner/repo") {
		t.Fatal("ssh and https forms differ")
	}
}
