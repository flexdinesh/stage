package main

// Tests of scripts/install-dev.sh and scripts/uninstall-dev.sh
// (docs/development.md). A fixture origin (bare repo) holds an old commit
// without the build interface, a supported main, a feature commit and a
// broken commit; the user's checkout is a clone with local main behind
// origin and uncommitted work. A stub mise builds a fake dist of the
// worktree it runs in, whose stub stage CLI writes the install record, so
// these tests cover resolution, gating, the isolated build, cleanup,
// repeat and refusals, never a real build or install.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// devCLI is the dist's stage-dev stub: install writes install.json from
// the asset dir's manifest; uninstall removes the install root.
const devCLI = `#!/bin/sh
root="$HOME/.local/lib/stage-dev"
printf '%s\n' "$*" >>"$CLI_LOG"
case "$1" in
  install)
    from=$3 mode=desktop name=stage-dev
    shift 3
    while [ $# -gt 0 ]; do
      case "$1" in
        --headless) mode=headless ;;
        --cli-name) name=$2; shift ;;
      esac
      shift
    done
    v=$(sed -n 's/^  "version": "\(.*\)",$/\1/p' "$from/stage-release.json")
    c=$(sed -n 's/^  "commit": "\(.*\)",$/\1/p' "$from/stage-release.json")
    mkdir -p "$root/app/bin"
    cp "$0" "$root/app/bin/stage"
    printf '{\n  "schema": 1,\n  "identity": "stage-dev",\n  "mode": "%s",\n  "version": "%s",\n  "commit": "%s",\n  "cliName": "%s",\n  "cli": "%s",\n  "state": "active"\n}\n' \
      "$mode" "$v" "$c" "$name" "$root/app/bin/stage" >"$root/install.json"
    ;;
  uninstall) rm -rf "$root" ;;
esac
`

// stubMise logs "<cwd> <args>"; `run dist` builds a fake dist of the git
// tree it runs in (fails when the tree has BREAK).
const stubMise = `#!/bin/sh
printf '%s %s\n' "$PWD" "$*" >>"$MISE_LOG"
[ "$1" = run ] && [ "$2" = dist ] || exit 0
[ ! -f BREAK ] || { echo 'stub: build failed' >&2; exit 1; }
sha=$(git rev-parse HEAD)
[ -z "$(git status --porcelain)" ] || sha="$sha-dirty"
v=0.0.0-dev.g$(printf '%s' "$sha" | cut -c1-12)
case "$(uname -s)" in Darwin) os=darwin ;; *) os=linux ;; esac
case "$(uname -m)" in aarch64 | arm64) arch=arm64 ;; *) arch=amd64 ;; esac
base="stage-dev-$v-$os-$arch"
mkdir -p "$STAGE_DIST_OUT/$base"
cp "$STUB_CLI" "$STAGE_DIST_OUT/$base/stage"
(cd "$STAGE_DIST_OUT" && tar -czf "$base.tar.gz" "$base" && rm -rf "$base")
printf '{\n  "schema": 1,\n  "version": "%s",\n  "commit": "%s",\n  "date": "2026-01-01T00:00:00Z"\n}\n' "$v" "$sha" >"$STAGE_DIST_OUT/stage-release.json"
`

type devFixture struct {
	t                                   *testing.T
	origin, checkout, home, cache, logs string
	bin                                 string
	old, base, main, feature, broken    string
	pr, localOnly                       string
}

var gitEnv = []string{"GIT_CONFIG_NOSYSTEM=1", "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.invalid",
	"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.invalid"}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), gitEnv...)
	cmd.Env = append(cmd.Env, "HOME="+t.TempDir())
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func commitAll(t *testing.T, dir, msg string) string {
	t.Helper()
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", msg)
	return git(t, dir, "rev-parse", "HEAD")
}

func newDevFixture(t *testing.T) *devFixture {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	script, err := os.ReadFile(filepath.Join(root, "scripts", "install-dev.sh"))
	if err != nil {
		t.Fatal(err)
	}
	uscript, err := os.ReadFile(filepath.Join(root, "scripts", "uninstall-dev.sh"))
	if err != nil {
		t.Fatal(err)
	}
	f := &devFixture{t: t, home: t.TempDir(), logs: t.TempDir(), bin: t.TempDir()}
	f.cache = filepath.Join(f.home, "cache")
	src := t.TempDir()
	git(t, src, "init", "-q", "-b", "main")
	writeFile(t, filepath.Join(src, "README.md"), "stage\n")
	writeFile(t, filepath.Join(src, "scripts", "install-dev.sh"), string(script))
	writeFile(t, filepath.Join(src, "scripts", "uninstall-dev.sh"), string(uscript))
	f.old = commitAll(t, src, "old: no build interface")
	writeFile(t, filepath.Join(src, "mise.toml"), "[tasks.dist]\nrun = \"scripts/release.sh dist\"\n")
	writeFile(t, filepath.Join(src, "scripts", "release.sh"), "OUT=${STAGE_DIST_OUT:-x}\n")
	writeFile(t, filepath.Join(src, "server", "internal", "install", "install.go"), "package install\n")
	f.base = commitAll(t, src, "build interface")
	writeFile(t, filepath.Join(src, "main.txt"), "main\n")
	f.main = commitAll(t, src, "main head")
	git(t, src, "switch", "-q", "-c", "feature", f.base)
	writeFile(t, filepath.Join(src, "feature.txt"), "feature\n")
	f.feature = commitAll(t, src, "feature")
	git(t, src, "switch", "-q", "-c", "broken", f.base)
	writeFile(t, filepath.Join(src, "BREAK"), "x\n")
	f.broken = commitAll(t, src, "broken build")
	git(t, src, "switch", "-q", "-c", "pr", f.base)
	writeFile(t, filepath.Join(src, "pr.txt"), "pr\n")
	f.pr = commitAll(t, src, "pull request head")

	f.origin = filepath.Join(t.TempDir(), "origin.git")
	git(t, src, "clone", "-q", "--bare", src, f.origin)
	git(t, f.origin, "config", "uploadpack.allowAnySHA1InWant", "true")
	// Like a GitHub pull request: on origin, but on no branch.
	git(t, f.origin, "update-ref", "refs/pull/1/head", f.pr)
	git(t, f.origin, "update-ref", "-d", "refs/heads/pr")
	f.checkout = filepath.Join(t.TempDir(), "checkout")
	git(t, src, "clone", "-q", f.origin, f.checkout)
	// Local main behind origin, uncommitted and untracked work, and a
	// commit origin has never seen.
	git(t, f.checkout, "reset", "-q", "--hard", f.base)
	tree := git(t, f.checkout, "rev-parse", "HEAD^{tree}")
	f.localOnly = git(t, f.checkout, "commit-tree", "-p", f.base, "-m", "local only", tree)
	git(t, f.checkout, "branch", "local-only", f.localOnly)
	writeFile(t, filepath.Join(f.checkout, "README.md"), "stage, edited\n")
	writeFile(t, filepath.Join(f.checkout, "notes.txt"), "untracked\n")

	if err := os.WriteFile(filepath.Join(f.bin, "mise"), []byte(stubMise), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.logs, "stage"), []byte(devCLI), 0o755); err != nil {
		t.Fatal(err)
	}
	// Never the developer's real gh login: not logged in here.
	if err := os.WriteFile(filepath.Join(f.bin, "gh"), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return f
}

type devRun struct {
	code     int
	out      string
	mise     []string
	cli      []string
	recorded string // install.json commit
}

func (f *devFixture) env(extra ...string) []string {
	env := append([]string{"PATH=" + f.bin + ":" + os.Getenv("PATH"), "HOME=" + f.home, "XDG_CACHE_HOME=" + f.cache,
		"TMPDIR=" + f.t.TempDir(), "MISE_LOG=" + filepath.Join(f.logs, "mise.log"),
		"CLI_LOG=" + filepath.Join(f.logs, "cli.log"), "STUB_CLI=" + filepath.Join(f.logs, "stage")}, gitEnv...)
	return append(env, extra...)
}

func lines(path string) []string {
	b, err := os.ReadFile(path)
	if err != nil || len(b) == 0 {
		return nil
	}
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

// run runs the checkout's install-dev.sh from the checkout and returns
// what the stubs logged during this run.
func (f *devFixture) run(script string, extraEnv []string, args ...string) devRun {
	f.t.Helper()
	for _, l := range []string{"mise.log", "cli.log"} {
		_ = os.Remove(filepath.Join(f.logs, l))
	}
	cmd := exec.Command("sh", append([]string{filepath.Join(f.checkout, "scripts", script)}, args...)...)
	cmd.Dir = f.checkout
	cmd.Env = f.env(extraEnv...)
	out, err := cmd.CombinedOutput()
	code := 0
	if xe, ok := err.(*exec.ExitError); ok {
		code = xe.ExitCode()
	} else if err != nil {
		f.t.Fatal(err)
	}
	return devRun{code: code, out: string(out), mise: lines(filepath.Join(f.logs, "mise.log")),
		cli: lines(filepath.Join(f.logs, "cli.log")), recorded: f.recorded()}
}

// runPiped runs the script as `curl ... | sh -s -- args` does: the script
// on stdin, from dir (a temp dir outside any repository when ""), with
// STAGE_DEV_REPO at the fixture origin and a gh that is not logged in.
func (f *devFixture) runPiped(script, dir string, extraEnv []string, args ...string) devRun {
	f.t.Helper()
	for _, l := range []string{"mise.log", "cli.log"} {
		_ = os.Remove(filepath.Join(f.logs, l))
	}
	body, err := os.ReadFile(filepath.Join(f.checkout, "scripts", script))
	if err != nil {
		f.t.Fatal(err)
	}
	if dir == "" {
		dir = f.t.TempDir()
	}
	cmd := exec.Command("sh", append([]string{"-s", "--"}, args...)...)
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(string(body))
	cmd.Env = f.env(append([]string{"STAGE_DEV_REPO=" + f.origin}, extraEnv...)...)
	out, err := cmd.CombinedOutput()
	code := 0
	if xe, ok := err.(*exec.ExitError); ok {
		code = xe.ExitCode()
	} else if err != nil {
		f.t.Fatal(err)
	}
	return devRun{code: code, out: string(out), mise: lines(filepath.Join(f.logs, "mise.log")),
		cli: lines(filepath.Join(f.logs, "cli.log")), recorded: f.recorded()}
}

// cacheRepo is the piped mode's bare clone.
func (f *devFixture) cacheRepo() string {
	return filepath.Join(f.cache, "stage-install-dev", "repo.git")
}

// cleanPiped asserts a piped run left only the cache clone: no temp build
// dir, no registered worktree, and the user's checkout unchanged.
func (f *devFixture) cleanPiped(before string) {
	f.t.Helper()
	if after := f.state(); after != before {
		f.t.Fatalf("checkout changed:\n%s\nwas:\n%s", after, before)
	}
	entries, err := os.ReadDir(filepath.Join(f.cache, "stage-install-dev"))
	if err != nil {
		f.t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "repo.git" {
		f.t.Fatalf("cache holds %v, want only repo.git", entries)
	}
	if wts := git(f.t, f.cacheRepo(), "worktree", "list", "--porcelain"); strings.Count(wts, "worktree ") != 1 {
		f.t.Fatalf("worktrees left in the cache clone:\n%s", wts)
	}
}

func (f *devFixture) recorded() string {
	b, err := os.ReadFile(filepath.Join(f.home, ".local", "lib", "stage-dev", "install.json"))
	if err != nil {
		return ""
	}
	for _, l := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(l, `  "commit": "`); ok {
			return strings.TrimSuffix(v, `",`)
		}
	}
	return ""
}

// state is everything the script must leave alone in the user's checkout.
func (f *devFixture) state() string {
	f.t.Helper()
	readme, err := os.ReadFile(filepath.Join(f.checkout, "README.md"))
	if err != nil {
		f.t.Fatal(err)
	}
	return strings.Join([]string{
		git(f.t, f.checkout, "rev-parse", "HEAD"),
		git(f.t, f.checkout, "symbolic-ref", "HEAD"),
		git(f.t, f.checkout, "status", "--porcelain"),
		git(f.t, f.checkout, "for-each-ref", "refs/heads"),
		git(f.t, f.checkout, "worktree", "list", "--porcelain"),
		string(readme),
	}, "\n---\n")
}

// clean asserts the run left no temp worktree, build dir or checkout change.
func (f *devFixture) clean(before string) {
	f.t.Helper()
	if after := f.state(); after != before {
		f.t.Fatalf("checkout changed:\n%s\nwas:\n%s", after, before)
	}
	entries, err := os.ReadDir(filepath.Join(f.cache, "stage-install-dev"))
	if err == nil && len(entries) > 0 {
		f.t.Fatalf("temp builds left: %v", entries)
	}
}

func mustContain(t *testing.T, r devRun, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(r.out, w) {
			t.Fatalf("exit %d, output lacks %q:\n%s", r.code, w, r.out)
		}
	}
}

func TestInstallDevDefaultIsOriginMain(t *testing.T) {
	f := newDevFixture(t)
	before := f.state()
	r := f.run("install-dev.sh", nil)
	if r.code != 0 || r.recorded != f.main {
		t.Fatalf("exit %d, installed %q, want origin/main %s:\n%s", r.code, r.recorded, f.main, r.out)
	}
	mustContain(t, r, "commit "+f.main, "Stage Dev 0.0.0-dev.g"+f.main[:12]+" ("+f.main+") installed")
	// Pinned setup and the build ran in a temp worktree, not the checkout.
	if len(r.mise) != 2 || !strings.HasSuffix(r.mise[0], " install") || !strings.HasSuffix(r.mise[1], " run dist") {
		t.Fatalf("mise calls %v", r.mise)
	}
	wt := strings.TrimSuffix(r.mise[1], " run dist")
	if !strings.HasPrefix(wt, filepath.Join(f.cache, "stage-install-dev")+"/build.") || !strings.HasSuffix(wt, "/src") {
		t.Fatalf("built in %s", wt)
	}
	if len(r.cli) != 1 || !strings.HasPrefix(r.cli[0], "install --from ") || !strings.HasSuffix(r.cli[0], " --desktop") {
		t.Fatalf("cli calls %v", r.cli)
	}
	f.clean(before)
}

func TestInstallDevExplicitSHARepeatAndUpdate(t *testing.T) {
	f := newDevFixture(t)
	before := f.state()
	// Uppercase is accepted; any commit origin has, also off main.
	if r := f.run("install-dev.sh", nil, strings.ToUpper(f.feature)); r.code != 0 || r.recorded != f.feature {
		t.Fatalf("exit %d, installed %q:\n%s", r.code, r.recorded, r.out)
	}
	f.clean(before)
	if r := f.run("install-dev.sh", nil, f.pr); r.code != 0 || r.recorded != f.pr {
		t.Fatalf("pull request commit: exit %d, installed %q:\n%s", r.code, r.recorded, r.out)
	}
	f.clean(before)
	if r := f.run("install-dev.sh", nil, f.feature); r.code != 0 || r.recorded != f.feature {
		t.Fatalf("exit %d, installed %q:\n%s", r.code, r.recorded, r.out)
	}
	r := f.run("install-dev.sh", nil, f.feature)
	if r.code != 0 || len(r.mise) != 0 || len(r.cli) != 0 {
		t.Fatalf("repeat rebuilt: exit %d, mise %v, cli %v:\n%s", r.code, r.mise, r.cli, r.out)
	}
	mustContain(t, r, "already installed", "--force")
	r = f.run("install-dev.sh", nil, "--force", f.feature)
	if r.code != 0 || len(r.mise) != 2 || len(r.cli) != 1 || r.recorded != f.feature {
		t.Fatalf("--force: exit %d, mise %v, cli %v:\n%s", r.code, r.mise, r.cli, r.out)
	}
	// Another --cli-name is not the same install: rebuild.
	if r := f.run("install-dev.sh", nil, "--cli-name", "sd", f.feature); r.code != 0 || len(r.cli) != 1 || !strings.HasSuffix(r.cli[0], "--desktop --cli-name sd") {
		t.Fatalf("cli name: exit %d, cli %v:\n%s", r.code, r.cli, r.out)
	}
	if r := f.run("install-dev.sh", nil, "--cli-name", "sd", f.main); r.code != 0 || r.recorded != f.main {
		t.Fatalf("update: exit %d, installed %q:\n%s", r.code, r.recorded, r.out)
	}
	// A mode switch is refused before building.
	r = f.run("install-dev.sh", nil, "--headless", f.feature)
	if r.code != 7 || len(r.mise) != 0 || r.recorded != f.main {
		t.Fatalf("mode switch: exit %d, mise %v:\n%s", r.code, r.mise, r.out)
	}
	mustContain(t, r, "uninstall-dev.sh")
	f.clean(before)
}

func TestInstallDevRefusals(t *testing.T) {
	f := newDevFixture(t)
	if r := f.run("install-dev.sh", nil, f.main); r.code != 0 {
		t.Fatalf("prior install: exit %d:\n%s", r.code, r.out)
	}
	before := f.state()
	pwned := filepath.Join(f.checkout, "PWNED")
	cases := []struct {
		name string
		args []string
		code int
		msg  string
		mise bool
	}{
		{"short sha", []string{f.feature[:12]}, 2, "full 40-character", false},
		{"ref", []string{"origin/main"}, 2, "full 40-character", false},
		{"injection", []string{"$(touch " + pwned + ")"}, 2, "full 40-character", false},
		{"option", []string{"--bogus"}, 2, "unknown option", false},
		{"two shas", []string{f.main, f.feature}, 2, "only one SHA", false},
		{"working tree and sha", []string{"--working-tree", f.main}, 2, "takes no SHA", false},
		{"missing commit", []string{strings.Repeat("0123456789", 4)}, 10, "origin has no commit", false},
		{"local only", []string{f.localOnly}, 10, "origin has no commit", false},
		{"unsupported", []string{f.old}, 7, "predates the dev-install build interface", false},
		{"build fails", []string{f.broken}, 1, "build failed; Stage Dev was not changed", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := f.run("install-dev.sh", nil, c.args...)
			if r.code != c.code || (len(r.mise) > 0) != c.mise || len(r.cli) != 0 {
				t.Fatalf("exit %d (want %d), mise %v, cli %v:\n%s", r.code, c.code, r.mise, r.cli, r.out)
			}
			mustContain(t, r, c.msg)
			// The prior install is untouched and usable.
			if r.recorded != f.main {
				t.Fatalf("install record now names %q", r.recorded)
			}
			if _, err := os.Stat(pwned); err == nil {
				t.Fatal("argument was executed")
			}
			f.clean(before)
		})
	}
}

func TestInstallDevFetchFailureNeverFallsBack(t *testing.T) {
	f := newDevFixture(t)
	if r := f.run("install-dev.sh", nil); r.code != 0 {
		t.Fatalf("prior install: exit %d:\n%s", r.code, r.out)
	}
	before := f.state()
	if err := os.RemoveAll(f.origin); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{nil, {f.main}, {"--force"}} {
		r := f.run("install-dev.sh", nil, args...)
		if r.code != 10 || len(r.mise) != 0 || r.recorded != f.main {
			t.Fatalf("%v: exit %d, mise %v:\n%s", args, r.code, r.mise, r.out)
		}
		mustContain(t, r, "git fetch origin failed; nothing was built")
		f.clean(before)
	}
}

func TestInstallDevCheckAndWorkingTree(t *testing.T) {
	f := newDevFixture(t)
	before := f.state()
	r := f.run("install-dev.sh", nil, "--check", "--headless", f.feature)
	if r.code != 0 || len(r.mise) != 3 || !strings.HasSuffix(r.mise[1], " run check") || !strings.HasSuffix(r.cli[0], " --headless") {
		t.Fatalf("--check: exit %d, mise %v, cli %v:\n%s", r.code, r.mise, r.cli, r.out)
	}
	f.clean(before)
	// --working-tree builds the checkout itself, dirty marked, no fetch.
	if err := os.RemoveAll(f.origin); err != nil {
		t.Fatal(err)
	}
	r = f.run("install-dev.sh", nil, "--working-tree", "--headless")
	if r.code != 0 || r.recorded != f.base+"-dirty" || !strings.HasPrefix(r.mise[1], f.checkout+" run dist") {
		t.Fatalf("--working-tree: exit %d, installed %q, mise %v:\n%s", r.code, r.recorded, r.mise, r.out)
	}
	f.clean(before)
}

func TestInstallDevPiped(t *testing.T) {
	f := newDevFixture(t)
	before := f.state()
	// First run clones into the cache and installs origin/main.
	r := f.runPiped("install-dev.sh", "", nil)
	if r.code != 0 || r.recorded != f.main {
		t.Fatalf("exit %d, installed %q, want origin/main %s:\n%s", r.code, r.recorded, f.main, r.out)
	}
	mustContain(t, r, "commit "+f.main)
	if git(t, f.cacheRepo(), "rev-parse", "--is-bare-repository") != "true" ||
		git(t, f.cacheRepo(), "remote", "get-url", "origin") != f.origin {
		t.Fatal("cache clone is not a bare clone of STAGE_DEV_REPO")
	}
	wt := strings.TrimSuffix(r.mise[len(r.mise)-1], " run dist")
	if !strings.HasPrefix(wt, filepath.Join(f.cache, "stage-install-dev")+"/build.") {
		t.Fatalf("built in %s", wt)
	}
	f.cleanPiped(before)
	marker := filepath.Join(f.cacheRepo(), "reused")
	writeFile(t, marker, "x")

	// Rerun: already installed, cache reused (not recloned).
	r = f.runPiped("install-dev.sh", "", nil)
	if r.code != 0 || len(r.mise) != 0 || len(r.cli) != 0 {
		t.Fatalf("repeat: exit %d, mise %v, cli %v:\n%s", r.code, r.mise, r.cli, r.out)
	}
	mustContain(t, r, "already installed")
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("cache clone was recreated")
	}

	// Explicit SHAs: off main, and a pull request head on no branch.
	for _, sha := range []string{f.feature, f.pr} {
		if r := f.runPiped("install-dev.sh", "", nil, sha); r.code != 0 || r.recorded != sha {
			t.Fatalf("%s: exit %d, installed %q:\n%s", sha, r.code, r.recorded, r.out)
		}
		f.cleanPiped(before)
	}

	// A commit origin does not have; --working-tree has no checkout.
	if r := f.runPiped("install-dev.sh", "", nil, f.localOnly); r.code != 10 || r.recorded != f.pr {
		t.Fatalf("local only: exit %d:\n%s", r.code, r.out)
	}
	r = f.runPiped("install-dev.sh", "", nil, "--working-tree")
	if r.code != 2 || len(r.mise) != 0 {
		t.Fatalf("--working-tree: exit %d:\n%s", r.code, r.out)
	}
	mustContain(t, r, "--working-tree needs a checkout")
	f.cleanPiped(before)

	// --help needs no script file.
	r = f.runPiped("install-dev.sh", "", nil, "--help")
	if r.code != 0 {
		t.Fatalf("--help: exit %d:\n%s", r.code, r.out)
	}
	mustContain(t, r, "sh -s --", "--working-tree")

	// Unreachable origin: refused, the prior install kept.
	r = f.runPiped("install-dev.sh", "", []string{"STAGE_DEV_REPO=" + filepath.Join(t.TempDir(), "gone.git")}, "--force")
	if r.code != 10 || len(r.mise) != 0 || r.recorded != f.pr {
		t.Fatalf("unreachable: exit %d, mise %v:\n%s", r.code, r.mise, r.out)
	}
	mustContain(t, r, "git fetch origin failed; nothing was built")
	f.cleanPiped(before)
}

// Piped from inside a Stage checkout (where dirname "sh"/.. is the
// checkout), the checkout is still not used: the cache clone is.
func TestInstallDevPipedIgnoresCurrentRepository(t *testing.T) {
	f := newDevFixture(t)
	before := f.state()
	r := f.runPiped("install-dev.sh", filepath.Join(f.checkout, "scripts"), nil, "--working-tree")
	if r.code != 2 {
		t.Fatalf("--working-tree from a checkout dir, piped: exit %d:\n%s", r.code, r.out)
	}
	r = f.runPiped("install-dev.sh", filepath.Join(f.checkout, "scripts"), nil, f.feature)
	if r.code != 0 || r.recorded != f.feature {
		t.Fatalf("exit %d, installed %q:\n%s", r.code, r.recorded, r.out)
	}
	if _, err := os.Stat(f.cacheRepo()); err != nil {
		t.Fatal("did not use the cache clone")
	}
	f.cleanPiped(before)
}

// uninstall-dev.sh

func TestUninstallDevPiped(t *testing.T) {
	f := newDevFixture(t)
	if r := f.runPiped("install-dev.sh", "", nil); r.code != 0 {
		t.Fatalf("install: exit %d:\n%s", r.code, r.out)
	}
	f.dirs(t, devDirs...)
	f.dirs(t, keptDirs...)
	if r := f.runPiped("uninstall-dev.sh", "", nil, "--help"); r.code != 0 || !strings.Contains(r.out, "--purge") {
		t.Fatalf("--help: exit %d:\n%s", r.code, r.out)
	}
	r := f.runPiped("uninstall-dev.sh", "", nil)
	if r.code != 0 || len(r.cli) != 1 || r.cli[0] != "uninstall" || r.recorded != "" || !f.exists(devDirs[0]) {
		t.Fatalf("uninstall: exit %d, cli %v:\n%s", r.code, r.cli, r.out)
	}
	r = f.runPiped("uninstall-dev.sh", "", nil, "--purge", "--yes")
	if r.code != 0 {
		t.Fatalf("purge: exit %d:\n%s", r.code, r.out)
	}
	for _, p := range devDirs {
		if f.exists(p) {
			t.Fatalf("%s kept by --purge", p)
		}
	}
	for _, p := range keptDirs {
		if !f.exists(p) {
			t.Fatalf("--purge removed %s", p)
		}
	}
}

func (f *devFixture) dirs(t *testing.T, paths ...string) {
	t.Helper()
	for _, p := range paths {
		writeFile(t, filepath.Join(f.home, p, "keep"), "x")
	}
}

func (f *devFixture) exists(p string) bool {
	_, err := os.Stat(filepath.Join(f.home, p))
	return err == nil
}

var (
	devDirs    = []string{".local/share/stage-dev", ".config/stage-dev", ".config/io.github.flexdinesh.stage-dev.desktop", "cache/io.github.flexdinesh.stage-dev.desktop"}
	keptDirs   = []string{".local/share/stage", ".config/stage", ".config/io.github.flexdinesh.stage.desktop", ".config/io.github.flexdinesh.stage-dev.desktop.dev", ".local/lib/stage"}
	macDevDirs = []string{"Library/Application Support/stage-dev", "Library/Application Support/io.github.flexdinesh.stage-dev.desktop",
		"Library/Logs/stage-dev", "Library/Logs/io.github.flexdinesh.stage-dev.desktop", "Library/Caches/io.github.flexdinesh.stage-dev.desktop"}
	macKeptDirs = []string{"Library/Application Support/stage", "Library/Logs/stage", "Library/Application Support/io.github.flexdinesh.stage.desktop"}
)

func TestUninstallDev(t *testing.T) {
	f := newDevFixture(t)
	install := func() {
		t.Helper()
		if r := f.run("install-dev.sh", []string{"XDG_CACHE_HOME=" + filepath.Join(f.home, "buildcache")}, "--force"); r.code != 0 {
			t.Fatalf("install: exit %d:\n%s", r.code, r.out)
		}
	}
	install()
	f.dirs(t, devDirs...)
	f.dirs(t, keptDirs...)

	// Plain uninstall keeps data.
	r := f.run("uninstall-dev.sh", nil)
	if r.code != 0 || len(r.cli) != 1 || r.cli[0] != "uninstall" || r.recorded != "" {
		t.Fatalf("uninstall: exit %d, cli %v:\n%s", r.code, r.cli, r.out)
	}
	for _, p := range append(devDirs, keptDirs...) {
		if !f.exists(p) {
			t.Fatalf("%s removed without --purge", p)
		}
	}

	// --purge needs a confirmation (stdin is not a terminal here).
	install()
	r = f.run("uninstall-dev.sh", nil, "--purge")
	if r.code != 2 || len(r.cli) != 0 || r.recorded == "" {
		t.Fatalf("unconfirmed purge: exit %d, cli %v:\n%s", r.code, r.cli, r.out)
	}
	if r := f.run("uninstall-dev.sh", []string{"STAGE_DATA_DIR=/x"}, "--purge", "--yes"); r.code != 2 || len(r.cli) != 0 {
		t.Fatalf("purge with STAGE_DATA_DIR: exit %d:\n%s", r.code, r.out)
	}
	r = f.run("uninstall-dev.sh", []string{"XDG_CACHE_HOME=" + filepath.Join(f.home, "cache")}, "--purge", "--yes")
	if r.code != 0 || len(r.cli) != 1 || r.recorded != "" {
		t.Fatalf("purge: exit %d, cli %v:\n%s", r.code, r.cli, r.out)
	}
	for _, p := range devDirs {
		if f.exists(p) {
			t.Fatalf("%s kept by --purge", p)
		}
	}
	for _, p := range keptDirs {
		if !f.exists(p) {
			t.Fatalf("--purge removed %s", p)
		}
	}

	// Nothing installed: says so; --purge still removes leftovers.
	f.dirs(t, devDirs[0])
	r = f.run("uninstall-dev.sh", nil, "--purge", "--yes")
	if r.code != 0 || len(r.cli) != 0 || f.exists(devDirs[0]) {
		t.Fatalf("not installed: exit %d:\n%s", r.code, r.out)
	}
	mustContain(t, r, "not installed")
}

func TestUninstallDevPurgeMacPaths(t *testing.T) {
	f := newDevFixture(t)
	f.dirs(t, macDevDirs...)
	f.dirs(t, macKeptDirs...)
	uname := filepath.Join(f.bin, "uname")
	if err := os.WriteFile(uname, []byte("#!/bin/sh\necho Darwin\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	r := f.run("uninstall-dev.sh", nil, "--purge", "--yes")
	if r.code != 0 {
		t.Fatalf("exit %d:\n%s", r.code, r.out)
	}
	for _, p := range macDevDirs {
		if f.exists(p) {
			t.Fatalf("%s kept", p)
		}
	}
	for _, p := range macKeptDirs {
		if !f.exists(p) {
			t.Fatalf("%s removed", p)
		}
	}
}
