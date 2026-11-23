package main

// Tests of `scripts/release.sh next` (docs/release.md#versions): a copy of
// the script in a temp repo with a VERSION file and tags, run with the
// pinned svu on PATH (mise run check:release).

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type nextRepo struct {
	t   *testing.T
	dir string
	n   int // commits so far: distinct messages, so no two commits share a hash
}

func newNextRepo(t *testing.T, version string) *nextRepo {
	t.Helper()
	if _, err := exec.LookPath("svu"); err != nil {
		t.Fatal("svu not on PATH; run through mise run check:release")
	}
	script, err := os.ReadFile(filepath.Join("..", "..", "scripts", "release.sh"))
	if err != nil {
		t.Fatal(err)
	}
	r := &nextRepo{t: t, dir: t.TempDir()}
	writeFile(t, filepath.Join(r.dir, "scripts", "release.sh"), string(script))
	if version != "" {
		writeFile(t, filepath.Join(r.dir, "VERSION"), version+"\n")
	}
	git(t, r.dir, "init", "-q", "-b", "main")
	r.commit()
	return r
}

func (r *nextRepo) commit() {
	r.t.Helper()
	r.n++
	git(r.t, r.dir, "commit", "-q", "--allow-empty", "-m", fmt.Sprint("c", r.n))
}

// tag makes a lightweight tag, as a GitHub release created through the API does.
func (r *nextRepo) tag(name, rev string) {
	r.t.Helper()
	git(r.t, r.dir, "update-ref", "refs/tags/"+name, rev)
}

func (r *nextRepo) next() (string, error) {
	r.t.Helper()
	cmd := exec.Command("bash", filepath.Join(r.dir, "scripts", "release.sh"), "next")
	cmd.Dir = r.dir
	cmd.Env = append(os.Environ(), gitEnv...)
	cmd.Env = append(cmd.Env, "HOME="+r.t.TempDir())
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return "", &nextErr{string(ee.Stderr)}
		}
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

type nextErr struct{ stderr string }

func (e *nextErr) Error() string { return e.stderr }

func (r *nextRepo) want(v string) {
	r.t.Helper()
	got, err := r.next()
	if err != nil || got != v {
		r.t.Fatalf("next = %q, %v; want %q", got, err, v)
	}
}

func (r *nextRepo) refused(substr string) {
	r.t.Helper()
	got, err := r.next()
	if err == nil {
		r.t.Fatalf("next = %q; want refusal containing %q", got, substr)
	}
	if !strings.Contains(err.Error(), substr) {
		r.t.Fatalf("next refused with %q; want %q", err, substr)
	}
}

func TestNextFirstReleaseOfMinor(t *testing.T) {
	newNextRepo(t, "0.1").want("0.1.0")

	r := newNextRepo(t, "0.2")
	r.tag("v0.1.5", "HEAD")
	r.commit()
	r.want("0.2.0")
}

func TestNextPatch(t *testing.T) {
	r := newNextRepo(t, "0.4")
	r.tag("v0.4.0", "HEAD")
	r.commit()
	r.tag("v0.4.1", "HEAD")
	r.commit()
	r.want("0.4.2")
}

func TestNextUsesHighestTagOnAnyBranch(t *testing.T) {
	r := newNextRepo(t, "0.4")
	r.tag("v0.4.0", "HEAD")
	git(t, r.dir, "switch", "-q", "-c", "side")
	r.commit()
	r.tag("v0.4.7", "HEAD")
	git(t, r.dir, "switch", "-q", "main")
	r.commit()
	r.want("0.4.8")
}

func TestNextIgnoresOtherTags(t *testing.T) {
	r := newNextRepo(t, "0.1")
	for _, tag := range []string{"v0.1.x", "0.1.4", "release", "v0.1"} {
		r.tag(tag, "HEAD")
	}
	r.commit()
	r.want("0.1.0")
}

func TestNextRefusesVersionBelowLatestRelease(t *testing.T) {
	r := newNextRepo(t, "0.1")
	r.tag("v0.2.0", "HEAD")
	r.commit()
	r.refused("below the latest release v0.2.0")

	r = newNextRepo(t, "0.9")
	r.tag("v1.0.0", "HEAD")
	r.commit()
	r.refused("below the latest release v1.0.0")
}

func TestNextRefusesReleasedHead(t *testing.T) {
	r := newNextRepo(t, "0.1")
	r.tag("v0.1.0", "HEAD")
	r.refused("HEAD is already released as v0.1.0")
}

func TestNextRefusesBadVersionFile(t *testing.T) {
	for _, v := range []string{"0.1.0", "v0.1", "01.2", "1", "1.x"} {
		newNextRepo(t, v).refused("VERSION must be MAJOR.MINOR")
	}
	newNextRepo(t, "").refused("VERSION file is missing")
}
