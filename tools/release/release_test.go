package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

const testCommit = "0123456789abcdef0123456789abcdef01234567"

func TestDevVersion(t *testing.T) {
	cases := []struct{ branch, commit, want string }{
		{"main", testCommit, "0.0.0-dev.main"},
		{"feat/Big-Thing_2", testCommit + "-dirty", "0.0.0-dev.feat.big.thing.2"},
		{"t3/007", testCommit, "0.0.0-dev.t3.b007"},
		{"HEAD", testCommit, "0.0.0-dev.g0123456789ab"},
		{"///", testCommit, "0.0.0-dev.g0123456789ab"},
		{strings.Repeat("abcdefghij.", 6), testCommit, "0.0.0-dev.abcdefghij.abcdefghij.abcdefghij.abcdefg"},
	}
	for _, c := range cases {
		got, err := devVersion(c.branch, c.commit)
		if err != nil || got != c.want {
			t.Errorf("devVersion(%q) = %q, %v; want %q", c.branch, got, err, c.want)
		}
	}
	if _, err := devVersion("main", "abc"); err == nil {
		t.Error("short commit accepted")
	}
}

func TestReleaseVersionValidation(t *testing.T) {
	for v, ok := range map[string]bool{
		"0.1.0": true, "1.2.3-rc.1": true, "1.2.3-beta": true,
		"v1.2.3": false, "1.2": false, "01.2.3": false, "1.2.3+build": false, "1.2.3-RC1": false, "1.2.3-rc_1": false, "": false,
	} {
		var out bytes.Buffer
		err := cmdVersion([]string{"-mode", "release", "-version", v}, &out)
		if (err == nil) != ok {
			t.Errorf("release version %q: err %v, want ok=%v", v, err, ok)
		}
	}
}

func TestPackageVersions(t *testing.T) {
	if debVersion("1.2.3-rc.1") != "1.2.3~rc.1" || debVersion("1.2.3") != "1.2.3" {
		t.Error("debVersion")
	}
	if archVersion("1.2.3-rc.1") != "1.2.3rc.1" || archVersion("1.2.3-1") != "1.2.3pre1" {
		t.Error("archVersion")
	}
}

// fakeCLI is a "binary" for a target: a shell script that prints
// version --json and carries the build stamp in its text.
func fakeCLI(b build, api int) []byte {
	return fmt.Appendf(nil, "#!/bin/sh\n# %s\necho '{\"identity\":\"%s\",\"version\":\"%s\",\"commit\":\"%s\",\"apiVersion\":%d,\"build\":\"%s\"}'\n",
		stampFor(b), b.Identity, b.Version, b.Commit, api, stampFor(b))
}

type entry struct {
	name string
	data []byte
}

func tarGz(t *testing.T, entries ...entry) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		if err := tw.WriteHeader(&tar.Header{Name: e.name, Mode: 0o755, Size: int64(len(e.data)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(e.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func ar(members ...entry) []byte {
	var buf bytes.Buffer
	buf.WriteString("!<arch>\n")
	for _, m := range members {
		fmt.Fprintf(&buf, "%-16s%-12s%-6s%-6s%-8s%-10d`\n", m.name, "0", "0", "0", "100644", len(m.data))
		buf.Write(m.data)
		if len(m.data)%2 == 1 {
			buf.WriteByte('\n')
		}
	}
	return buf.Bytes()
}

type fixture struct {
	dir, out string
	b        build
	opts     assembleOpts
}

func write(t *testing.T, p string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, data, 0o755); err != nil {
		t.Fatal(err)
	}
}

// newFixture lays out what scripts/release.sh hands assemble: CLIs for
// this machine and linux-amd64, and MyGo's linux-amd64 desktop output.
// mutate may change the desktop output before assembling.
func newFixture(t *testing.T, mutate func(desktop string, f *fixture)) *fixture {
	t.Helper()
	dir := t.TempDir()
	f := &fixture{dir: dir, out: filepath.Join(dir, "out"), b: build{Version: "1.2.3-rc.1", Commit: testCommit, Identity: "stage", Channel: "stable"}}
	host := runtime.GOOS + "-" + runtime.GOARCH
	targets := []string{host}
	if host != "linux-amd64" {
		targets = append(targets, "linux-amd64")
	}
	cli := fakeCLI(f.b, 1)
	for _, tg := range targets {
		write(t, filepath.Join(dir, "cli", tg, "stage"), cli)
	}
	write(t, filepath.Join(dir, "LICENSE"), []byte("MIT"))
	write(t, filepath.Join(dir, "README.md"), []byte("# Stage"))

	desktop := filepath.Join(dir, "build", "linux-amd64")
	slug := f.b.slug()
	exe := []byte("ELF desktop " + stampFor(f.b))
	write(t, filepath.Join(desktop, slug), exe)
	write(t, filepath.Join(desktop, "bin", "stage"), cli)
	tarName := slug + "-1.2.3-rc.1-linux-amd64.tar.gz"
	tgz := tarGz(t, entry{"bin/stage", cli}, entry{slug, exe}, entry{"LICENSE", []byte("MIT")}, entry{slug + ".desktop", []byte("[Desktop Entry]")})
	write(t, filepath.Join(desktop, tarName), tgz)
	control := tarGz(t, entry{"./control", []byte("Package: " + slug + "\nVersion: 1.2.3~rc.1\nArchitecture: amd64\n")})
	data := tarGz(t, entry{"./opt/" + slug + "/bin/stage", cli}, entry{"./opt/" + slug + "/LICENSE", []byte("MIT")}, entry{"./opt/" + slug + "/" + slug, exe})
	write(t, filepath.Join(desktop, slug+"_1.2.3~rc.1_amd64.deb"), ar(entry{"debian-binary", []byte("2.0\n")}, entry{"control.tar.gz", control}, entry{"data.tar.gz", data}))
	write(t, filepath.Join(desktop, slug+"-1.2.3~rc.1-1.x86_64.rpm"), []byte("rpm"))
	icon256, icon512 := pngOf(t, 256), pngOf(t, 512)
	write(t, filepath.Join(desktop, slug+"-256x256.png"), icon256)
	write(t, filepath.Join(desktop, slug+"-512x512.png"), icon512)
	write(t, filepath.Join(desktop, "PKGBUILD"), pkgbuild(tarName, slug, bytesSHA256(tgz), bytesSHA256(icon256), bytesSHA256(icon512)))
	write(t, filepath.Join(desktop, "install.sh"), []byte("#!/bin/sh\n"))
	if mutate != nil {
		mutate(desktop, f)
	}
	f.opts = assembleOpts{
		out: f.out, cliDir: filepath.Join(dir, "cli"), desktopDir: filepath.Join(dir, "build"),
		license: filepath.Join(dir, "LICENSE"), readme: filepath.Join(dir, "README.md"),
		b: f.b, mtime: time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC), cli: targets, desktop: []string{"linux-amd64"},
		omit: []Omission{{Kind: KindDesktop, OS: "darwin", Arch: "universal", Reason: "test"}},
	}
	return f
}

// pngOf is an n x n PNG.
func pngOf(t *testing.T, n int) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewGray(image.Rect(0, 0, n, n))); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// pkgbuild is MyGo's PKGBUILD shape: the tarball, then its two icons.
func pkgbuild(tarName, slug, tarSum, sum256, sum512 string) []byte {
	return []byte("pkgver='1.2.3rc.1'\nsource=('" + tarName + "' '" + slug + "-256x256.png' '" + slug + "-512x512.png')\nsha256sums=('" + tarSum + "' '" + sum256 + "' '" + sum512 + "')\n")
}

func TestAssembleThenVerify(t *testing.T) {
	f := newFixture(t, nil)
	m, err := assemble(f.opts)
	if err != nil {
		t.Fatal(err)
	}
	if m.Compatibility.APIVersion != 1 || m.Version != "1.2.3-rc.1" || len(m.Omitted) != 1 {
		t.Fatalf("manifest %+v", m)
	}
	names := map[string]bool{}
	for _, a := range m.Artifacts {
		names[a.Name] = true
	}
	for _, want := range []string{
		"stage-1.2.3-rc.1-linux-amd64.tar.gz",
		"stage-desktop-1.2.3-rc.1-linux-amd64.tar.gz",
		"stage-desktop_1.2.3~rc.1_amd64.deb",
		"stage-desktop-1.2.3~rc.1-1.x86_64.rpm",
		"stage-desktop-1.2.3-rc.1-linux-amd64.PKGBUILD",
		"stage-desktop-install.sh",
		"stage-desktop-256x256.png",
		"stage-desktop-512x512.png",
	} {
		if !names[want] {
			t.Errorf("missing artifact %s in %v", want, names)
		}
	}
	if _, err := verifyDir(f.out); err != nil {
		t.Fatalf("verify: %v", err)
	}
	// Reproducible: the headless archive is byte-identical when rebuilt.
	first, _, _ := fileSHA256(filepath.Join(f.out, "stage-1.2.3-rc.1-linux-amd64.tar.gz"))
	f.opts.out = filepath.Join(f.dir, "out2")
	if _, err := assemble(f.opts); err != nil {
		t.Fatal(err)
	}
	second, _, _ := fileSHA256(filepath.Join(f.opts.out, "stage-1.2.3-rc.1-linux-amd64.tar.gz"))
	if first != second {
		t.Error("headless archive is not reproducible")
	}
}

func TestVerifyCatchesTampering(t *testing.T) {
	cases := map[string]func(t *testing.T, out string){
		"changed artifact": func(t *testing.T, out string) {
			write(t, filepath.Join(out, "stage-desktop-install.sh"), []byte("#!/bin/sh\nrm -rf ~\n"))
		},
		"extra file": func(t *testing.T, out string) {
			write(t, filepath.Join(out, "notes.txt"), []byte("x"))
		},
		"missing file": func(t *testing.T, out string) {
			if err := os.Remove(filepath.Join(out, "stage-desktop-1.2.3~rc.1-1.x86_64.rpm")); err != nil {
				t.Fatal(err)
			}
		},
		"checksums edited": func(t *testing.T, out string) {
			p := filepath.Join(out, ChecksumsName)
			data, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			write(t, p, bytes.Replace(data, []byte("  stage-desktop-install.sh"), []byte("  stage-desktop-install.sh\n"+strings.Repeat("0", 64)+"  extra"), 1))
		},
		"manifest version changed": func(t *testing.T, out string) {
			p := filepath.Join(out, ManifestName)
			data, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			write(t, p, bytes.ReplaceAll(data, []byte(`"version": "1.2.3-rc.1"`), []byte(`"version": "1.2.4"`)))
		},
	}
	for name, tamper := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t, nil)
			if _, err := assemble(f.opts); err != nil {
				t.Fatal(err)
			}
			tamper(t, f.out)
			if _, err := verifyDir(f.out); err == nil {
				t.Fatal("verify passed a tampered directory")
			}
		})
	}
}

func TestAssembleRefuses(t *testing.T) {
	cases := map[string]func(desktop string, f *fixture){
		"bundled CLI differs": func(desktop string, f *fixture) {
			write(t, filepath.Join(desktop, "bin", "stage"), []byte("other "+stampFor(f.b)))
		},
		"desktop stamped with another version": func(desktop string, f *fixture) {
			other := f.b
			other.Version = "9.9.9"
			write(t, filepath.Join(desktop, f.b.slug()), []byte("ELF "+stampFor(other)))
		},
		"desktop not stamped": func(desktop string, f *fixture) {
			write(t, filepath.Join(desktop, f.b.slug()), []byte("ELF"))
		},
		"secret in the desktop archive": func(desktop string, f *fixture) {
			cli := fakeCLI(f.b, 1)
			write(t, filepath.Join(desktop, f.b.slug()+"-1.2.3-rc.1-linux-amd64.tar.gz"), tarGz(t, entry{"bin/stage", cli}, entry{".env", []byte("TOKEN=x")}))
		},
		"dev database in the deb": func(desktop string, f *fixture) {
			cli := fakeCLI(f.b, 1)
			slug := f.b.slug()
			control := tarGz(t, entry{"./control", []byte("Package: " + slug + "\nVersion: 1.2.3~rc.1\nArchitecture: amd64\n")})
			data := tarGz(t, entry{"./opt/" + slug + "/bin/stage", cli}, entry{"./opt/" + slug + "/LICENSE", []byte("MIT")}, entry{"./opt/" + slug + "/stage.db", []byte("SQLite")})
			write(t, filepath.Join(desktop, slug+"_1.2.3~rc.1_amd64.deb"), ar(entry{"debian-binary", []byte("2.0\n")}, entry{"control.tar.gz", control}, entry{"data.tar.gz", data}))
		},
		"PKGBUILD names another archive": func(desktop string, f *fixture) {
			write(t, filepath.Join(desktop, "PKGBUILD"), []byte("pkgver='1.2.3rc.1'\nsource=('x.tar.gz')\n"))
		},
		"PKGBUILD names an unknown source": func(desktop string, f *fixture) {
			tarName := f.b.slug() + "-1.2.3-rc.1-linux-amd64.tar.gz"
			write(t, filepath.Join(desktop, "evil.sh"), []byte("#!/bin/sh\n"))
			write(t, filepath.Join(desktop, "PKGBUILD"), []byte("pkgver='1.2.3rc.1'\nsource=('"+tarName+"' 'evil.sh')\nsha256sums=('x' 'y')\n"))
		},
		"PKGBUILD icon checksum wrong": func(desktop string, f *fixture) {
			slug := f.b.slug()
			tarName := slug + "-1.2.3-rc.1-linux-amd64.tar.gz"
			tgz, err := os.ReadFile(filepath.Join(desktop, tarName))
			if err != nil {
				t.Fatal(err)
			}
			write(t, filepath.Join(desktop, "PKGBUILD"), pkgbuild(tarName, slug, bytesSHA256(tgz), strings.Repeat("0", 64), bytesSHA256(pngOf(t, 512))))
		},
		"icon not its named size": func(desktop string, f *fixture) {
			slug := f.b.slug()
			tarName := slug + "-1.2.3-rc.1-linux-amd64.tar.gz"
			tgz, err := os.ReadFile(filepath.Join(desktop, tarName))
			if err != nil {
				t.Fatal(err)
			}
			small := pngOf(t, 128)
			write(t, filepath.Join(desktop, slug+"-256x256.png"), small)
			write(t, filepath.Join(desktop, "PKGBUILD"), pkgbuild(tarName, slug, bytesSHA256(tgz), bytesSHA256(small), bytesSHA256(pngOf(t, 512))))
		},
		"deb with another version": func(desktop string, f *fixture) {
			cli := fakeCLI(f.b, 1)
			slug := f.b.slug()
			control := tarGz(t, entry{"./control", []byte("Package: " + slug + "\nVersion: 1.2.2\nArchitecture: amd64\n")})
			data := tarGz(t, entry{"./opt/" + slug + "/bin/stage", cli}, entry{"./opt/" + slug + "/LICENSE", []byte("MIT")})
			write(t, filepath.Join(desktop, slug+"_1.2.3~rc.1_amd64.deb"), ar(entry{"debian-binary", []byte("2.0\n")}, entry{"control.tar.gz", control}, entry{"data.tar.gz", data}))
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t, mutate)
			_, err := assemble(f.opts)
			if err == nil {
				_, err = verifyDir(f.out)
			}
			if err == nil {
				t.Fatal("accepted")
			}
			t.Log(err)
		})
	}
}

func TestAssembleRemovesOutputOnFailure(t *testing.T) {
	f := newFixture(t, func(desktop string, f *fixture) {
		write(t, filepath.Join(desktop, "bin", "stage"), []byte("other"))
	})
	if _, err := assemble(f.opts); err == nil {
		t.Fatal("accepted")
	}
	if _, err := os.Stat(f.out); !os.IsNotExist(err) {
		t.Fatalf("output left behind: %v", err)
	}
}

func TestDeniedPath(t *testing.T) {
	for p, want := range map[string]bool{
		".env": true, "a/.env.local": true, "x/stage.db": true, "stage.db-wal": true, "k/id_ed25519": true,
		"a/.scratch/b": true, ".git/config": true, "cfg/service.env": true, "x/server.key": true,
		"bin/stage": false, "LICENSE": false, "Stage.app/Contents/Info.plist": false, "assets/index.js": false,
	} {
		if got := deniedPath(p); got != want {
			t.Errorf("deniedPath(%q) = %v", p, got)
		}
	}
}

// machO is a minimal 64-bit Mach-O header (no load commands) plus body.
func machO(cpu uint32, body string) []byte {
	var b bytes.Buffer
	for _, v := range []uint32{0xfeedfacf, cpu, 0, 2, 0, 0, 0, 0} {
		_ = binary.Write(&b, binary.LittleEndian, v)
	}
	b.WriteString(body)
	return b.Bytes()
}

func fat(slices ...[]byte) []byte {
	var hdr bytes.Buffer
	_ = binary.Write(&hdr, binary.BigEndian, uint32(0xcafebabe))
	_ = binary.Write(&hdr, binary.BigEndian, uint32(len(slices)))
	off := uint32(4096)
	cpus := []uint32{0x0100000c, 0x01000007}
	var body bytes.Buffer
	for i, s := range slices {
		for _, v := range []uint32{cpus[i], 0, off, uint32(len(s)), 12} {
			_ = binary.Write(&hdr, binary.BigEndian, v)
		}
		off += uint32(len(s))
	}
	out := make([]byte, 4096)
	copy(out, hdr.Bytes())
	for _, s := range slices {
		body.Write(s)
	}
	return append(out, body.Bytes()...)
}

func TestStampsAndUniversalSlices(t *testing.T) {
	b := build{Version: "1.0.0", Commit: testCommit, Identity: "stage"}
	arm := machO(0x0100000c, "arm "+stampFor(b))
	amd := machO(0x01000007, "amd "+stampFor(b))
	u := fat(arm, amd)
	if err := checkStamp(u, b); err != nil {
		t.Fatalf("universal stamp: %v", err)
	}
	if err := checkUniversalCLI(u, map[string][]byte{"darwin-arm64": arm, "darwin-amd64": amd}); err != nil {
		t.Fatalf("slices: %v", err)
	}
	if err := checkUniversalCLI(u, map[string][]byte{"darwin-arm64": arm, "darwin-amd64": machO(0x01000007, "other")}); err == nil {
		t.Fatal("a differing slice was accepted")
	}
	other := b
	other.Commit = strings.Repeat("f", 40)
	if err := checkStamp(fat(arm, machO(0x01000007, stampFor(other))), b); err == nil {
		t.Fatal("a slice with another commit was accepted")
	}
	if err := checkStamp([]byte("no stamp"), b); err == nil {
		t.Fatal("an unstamped binary was accepted")
	}
	if err := checkStamp([]byte(stampFor(b)+stampFor(other)), b); err == nil {
		t.Fatal("two stamps accepted")
	}
}

func TestOmitFlag(t *testing.T) {
	var o omitFlags
	if err := o.Set("desktop/darwin/universal=no Mac"); err != nil || o[0] != (Omission{Kind: "desktop", OS: "darwin", Arch: "universal", Reason: "no Mac"}) {
		t.Fatalf("%v %+v", err, o)
	}
	for _, bad := range []string{"desktop", "desktop/darwin", "desktop=x", "a/b/c/d=x"} {
		if err := o.Set(bad); err == nil {
			t.Errorf("-omit %q accepted", bad)
		}
	}
}
