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

func TestInspectCheckoutFamily(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	main := filepath.Join(root, "repo")
	common := filepath.Join(main, ".git")
	write(t, filepath.Join(common, "config"), config)
	write(t, filepath.Join(common, "worktrees", "wt", "commondir"), "../..\n")
	wt := filepath.Join(root, "wt")
	write(t, filepath.Join(wt, ".git"), "gitdir: ../repo/.git/worktrees/wt\n")
	sub := filepath.Join(wt, "packages", "web")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	const remote = "github.com/Flex/Stage"
	for _, tc := range []struct {
		dir  string
		want Info
	}{
		{main, Info{Top: main, CommonDir: common, Remote: remote, Subpath: "."}},
		{wt, Info{Top: wt, CommonDir: common, Remote: remote, Subpath: "."}},
		{sub, Info{Top: wt, CommonDir: common, Remote: remote, Subpath: "packages/web"}},
		{root, Info{}},
	} {
		if got := Inspect(tc.dir); got != tc.want {
			t.Errorf("Inspect(%s) = %+v, want %+v", tc.dir, got, tc.want)
		}
	}
}

func TestInspectNearestCheckout(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(root, ".git", "config"), config)
	nested := filepath.Join(root, "vendor", "lib")
	modulesDir := filepath.Join(root, ".git", "modules", "lib")
	write(t, filepath.Join(nested, ".git"), "gitdir: ../../.git/modules/lib\n")
	write(t, filepath.Join(modulesDir, "config"), "[remote \"origin\"]\n\turl = https://github.com/o/lib.git\n")
	if got, want := Inspect(nested), (Info{Top: nested, CommonDir: modulesDir, Remote: "github.com/o/lib", Subpath: "."}); got != want {
		t.Fatalf("submodule %+v, want %+v", got, want)
	}
	inner := filepath.Join(root, "inner")
	write(t, filepath.Join(inner, ".git", "config"), "[remote \"upstream\"]\n\turl = https://github.com/o/lib.git\n")
	if got, want := Inspect(inner), (Info{Top: inner, CommonDir: filepath.Join(inner, ".git"), Subpath: "."}); got != want {
		t.Fatalf("checkout with no origin %+v, want %+v", got, want)
	}
	broken := filepath.Join(root, "broken")
	write(t, filepath.Join(broken, ".git"), "gitdir: missing\n")
	if got := Inspect(broken); got != (Info{}) {
		t.Fatalf("invalid nearest checkout borrowed evidence: %+v", got)
	}
}

func TestInspectCanonicalCommonDirectory(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	checkout := filepath.Join(root, "repo")
	common := filepath.Join(root, "metadata")
	write(t, filepath.Join(common, "config"), config)
	if err := os.Mkdir(checkout, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(common, filepath.Join(checkout, ".git")); err != nil {
		t.Fatal(err)
	}
	if got := Inspect(checkout); got.CommonDir != common || got.Top != checkout || got.Remote != "github.com/Flex/Stage" {
		t.Fatalf("symlinked common dir: %+v", got)
	}
}

func TestRemoteIdentity(t *testing.T) {
	for raw, want := range map[string]string{
		" git@Host.:/Owner/Repo.git ":                       "host/Owner/Repo",
		"git.example:Org/Repo.git":                          "git.example/Org/Repo",
		"https://user:secret@Host.com.:443//o/R.git/?q=s#f": "host.com/o/R",
		"ssh://git@host:22/o/r":                             "host/o/r",
		"git+ssh://git@host:3022/o/r":                       "host:3022/o/r",
		"ssh+git://git@host:22/o/r":                         "host/o/r",
		"http://host:80/o/r":                                "host/o/r",
		"git://host:9418/o/r":                               "host/o/r",
		"https://[2001:DB8::1]:443/o/r":                     "[2001:db8::1]/o/r",
		"ssh://git@[2001:db8::1]:2222/o/r":                  "[2001:db8::1]:2222/o/r",
		"git@[2001:db8::1]:o/r":                             "[2001:db8::1]/o/r",
		"https://host/Owner//Repo":                          "host/Owner//Repo",
		"https://host/Owner/Repo%20Name.git":                "host/Owner/Repo Name",
		"https://host/o/%2e%2e/r":                           "",
		"git@host:o/r%20x":                                  "",
		"ssh://2001:db8::1/o/r":                             "",
		"https://host/o/Repo%2FName":                        "",
		"https://host/o/Repo%5cName":                        "",
		"https://host/o/Repo%252FName":                      "",
		"https://host/o/Repo%3fName":                        "",
		"https://host/o/Repo%23Name":                        "",
		"https://host/o/Repo%ffName":                        "",
		"https://host/o/Repo%00Name":                        "",
		"https://host/o/Repo%xx":                            "",
		"https://host/":                                     "",
		"https://host/.git":                                 "",
		"https://host:o/o/r":                                "",
		"https:///o/r":                                      "",
		"http://./o/r":                                      "",
		"host:":                                             "",
		"git@h:r":                                           "",
		"/abs/path":                                         "",
		"./host:path":                                       "",
		"C:/abs/path":                                       "",
		"c:\\abs\\path":                                     "",
		"file:///abs/path":                                  "",
		"ftp://host/o/r":                                    "",
		"":                                                  "",
	} {
		if got := RemoteIdentity(raw); got != want {
			t.Errorf("%q: got %q, want %q", raw, got, want)
		}
	}
}
