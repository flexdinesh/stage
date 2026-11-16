package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// smokeTest runs what this machine can run natively: the headless CLI
// (version, then `serve` on a temp socket with isolated dirs: health and
// the embedded web UI) and the desktop app's selfcheck, which resolves its
// bundled CLI the way the launcher does. Other targets are cross-built
// and reported as not run.
func smokeTest(dir string, m *Manifest) ([]string, error) {
	b := build{Version: m.Version, Commit: m.Commit, Identity: m.Identity, Channel: m.Channel}
	hostOS, hostArch := runtime.GOOS, runtime.GOARCH
	var lines []string
	var errs []error
	ran := map[string]bool{}
	for _, a := range m.Artifacts {
		if a.Format != FormatTarGz {
			continue
		}
		native := a.OS == hostOS && (a.Arch == hostArch || a.Arch == "universal")
		if !native {
			continue
		}
		var err error
		switch a.Kind {
		case KindCLI:
			err = smokeCLI(filepath.Join(dir, a.Name), a, b, m.Compatibility.APIVersion)
		case KindDesktop:
			err = smokeDesktop(filepath.Join(dir, a.Name), a, b)
		default:
			continue
		}
		ran[a.Name] = true
		if err != nil {
			errs = append(errs, fmt.Errorf("smoke %s: %w", a.Name, err))
			lines = append(lines, "smoke FAIL "+a.Name)
		} else {
			lines = append(lines, "smoke ok   "+a.Name)
		}
	}
	for _, a := range m.Artifacts {
		switch {
		case ran[a.Name] || (a.Kind != KindCLI && a.Kind != KindDesktop) || a.Format == FormatPNG:
		case a.Format == FormatTarGz:
			lines = append(lines, "not run    "+a.Name+" (cross-built: not native to this machine)")
		default:
			lines = append(lines, "not run    "+a.Name+" (system package: install it to test)")
		}
	}
	return lines, errors.Join(errs...)
}

// extract unpacks a verified tar.gz into dst (regular files, dirs and
// symlinks only; readTarGz already refused unsafe names).
func extract(archivePath, dst string) error {
	ar, err := openTar(archivePath, func(string) bool { return true })
	if err != nil {
		return err
	}
	for name, typ := range ar.types {
		if typ == '5' {
			if err := os.MkdirAll(filepath.Join(dst, filepath.FromSlash(name)), 0o755); err != nil {
				return err
			}
		}
	}
	for name, data := range ar.files {
		p := filepath.Join(dst, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(p, data, 0o755); err != nil {
			return err
		}
	}
	return nil
}

// isolatedEnv is a temp HOME, XDG dirs and data dir with fake T3, so the
// smoke server never sees real Stage state, sockets or services.
func isolatedEnv(root, run string) []string {
	return []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + filepath.Join(root, "home"),
		"XDG_DATA_HOME=" + filepath.Join(root, "data-home"),
		"XDG_CONFIG_HOME=" + filepath.Join(root, "config"),
		"XDG_CACHE_HOME=" + filepath.Join(root, "cache"),
		"XDG_STATE_HOME=" + filepath.Join(root, "state"),
		"XDG_RUNTIME_DIR=" + run,
		"STAGE_DATA_DIR=" + filepath.Join(root, "stage"),
		"T3_FAKE=1",
	}
}

func smokeCLI(archivePath string, a Artifact, b build, api int) error {
	root, err := os.MkdirTemp("", "stage-smoke-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(root)
	if err := extract(archivePath, root); err != nil {
		return err
	}
	bin := filepath.Join(root, filepath.FromSlash(a.CLI.Path))
	v, err := runCLIVersion(bin)
	if err != nil {
		return err
	}
	if err := v.check(b); err != nil {
		return err
	}
	if v.APIVersion != api {
		return fmt.Errorf("apiVersion %d, manifest says %d", v.APIVersion, api)
	}
	// Unix socket paths are short: a separate short temp dir.
	run, err := os.MkdirTemp("", "ss")
	if err != nil {
		return err
	}
	defer os.RemoveAll(run)
	sock := filepath.Join(run, "s.sock")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "serve", "--listen", "unix:"+sock)
	cmd.Env = isolatedEnv(root, run)
	var out strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Start(); err != nil {
		return err
	}
	defer func() {
		_ = cmd.Process.Signal(os.Interrupt)
		done := make(chan struct{})
		go func() { _ = cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(15 * time.Second):
			_ = cmd.Process.Kill()
			<-done
		}
	}()
	client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", sock)
		},
	}}
	var health struct {
		Service    string `json:"service"`
		Identity   string `json:"identity"`
		Version    string `json:"version"`
		Commit     string `json:"commit"`
		APIVersion int    `json:"apiVersion"`
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		res, err := client.Get("http://stage.invalid/api/health")
		if err == nil {
			err = json.NewDecoder(res.Body).Decode(&health)
			res.Body.Close()
			if err == nil && res.StatusCode == http.StatusOK {
				break
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("no healthy server on %s: %v\n%s", sock, err, out.String())
		}
		time.Sleep(200 * time.Millisecond)
	}
	if health.Service != "stage" || health.Identity != b.Identity || health.Version != b.Version || health.Commit != b.Commit || health.APIVersion != api {
		return fmt.Errorf("health reports %+v", health)
	}
	res, err := client.Get("http://stage.invalid/")
	if err != nil {
		return err
	}
	defer res.Body.Close()
	page, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return err
	}
	if res.StatusCode != http.StatusOK || !strings.Contains(string(page), `<div id="root"`) {
		return fmt.Errorf("GET / is %d without the web app (web assets not embedded?)", res.StatusCode)
	}
	return nil
}

// selfcheck is the desktop app's STAGE_DESKTOP_SELFCHECK report.
type selfcheck struct {
	Identity   string     `json:"identity"`
	Version    string     `json:"version"`
	Build      string     `json:"build"`
	CLI        string     `json:"cli"`
	CLIVersion cliVersion `json:"cliVersion"`
}

func smokeDesktop(archivePath string, a Artifact, b build) error {
	root, err := os.MkdirTemp("", "stage-smoke-app-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(root)
	if err := extract(archivePath, root); err != nil {
		return err
	}
	exe := filepath.Join(root, b.slug())
	if a.OS == "darwin" {
		exe = filepath.Join(root, b.appName()+".app", "Contents", "MacOS", b.appName())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe)
	cmd.Env = append(isolatedEnv(root, root), "STAGE_DESKTOP_SELFCHECK=1")
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return fmt.Errorf("selfcheck: %v: %s", err, ee.Stderr)
		}
		return fmt.Errorf("selfcheck: %w", err)
	}
	var r selfcheck
	if err := json.Unmarshal(out, &r); err != nil {
		return fmt.Errorf("selfcheck printed %q: %w", out, err)
	}
	wantCLI := filepath.Join(root, filepath.FromSlash(a.CLI.Path))
	if real, err := filepath.EvalSymlinks(wantCLI); err == nil {
		wantCLI = real
	}
	gotCLI := r.CLI
	if real, err := filepath.EvalSymlinks(gotCLI); err == nil {
		gotCLI = real
	}
	if gotCLI != wantCLI {
		return fmt.Errorf("the app resolves its CLI at %s, want %s", r.CLI, wantCLI)
	}
	if r.Identity != b.Identity || r.Version != b.Version || r.Build != stampFor(b) {
		return fmt.Errorf("the app reports %s %s %q", r.Identity, r.Version, r.Build)
	}
	return r.CLIVersion.check(b)
}
