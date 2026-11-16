package main

import (
	"bytes"
	"debug/macho"
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// darwinBinary links a tiny Go program for darwin/arch as release.sh links
// the CLI (CGO off, -s -w): Go's linker signs darwin/arm64 only.
func darwinBinary(t *testing.T, arch, msg string) []byte {
	t.Helper()
	dir := t.TempDir()
	src := "package main\n\nimport \"fmt\"\n\nfunc main() { fmt.Println(\"" + msg + "\") }\n"
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "bin")
	cmd := exec.Command("go", "build", "-trimpath", "-ldflags", "-s -w", "-o", out, ".")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=darwin", "GOARCH="+arch, "GOWORK=off", "GOFLAGS=")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build darwin/%s: %v\n%s", arch, err, b)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// adHocSign mimics codesign --force --sign - on a thin 64-bit Mach-O: it
// drops any signature, appends a new blob (16-byte aligned) at the end of
// __LINKEDIT, adds or rewrites LC_CODE_SIGNATURE and grows __LINKEDIT.
func adHocSign(t *testing.T, b []byte, blob string) []byte {
	t.Helper()
	f, err := macho.NewFile(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	le := binary.LittleEndian
	out := bytes.Clone(b)
	ncmds, sizeofcmds := le.Uint32(out[16:]), le.Uint32(out[20:])
	end, sigCmd, linkedit := uint64(len(b)), -1, -1
	off := 32
	for range ncmds {
		cmd, size := le.Uint32(out[off:]), le.Uint32(out[off+4:])
		if cmd == lcCodeSignature {
			sigCmd, end = off, uint64(le.Uint32(out[off+8:]))
		}
		if cmd == uint32(macho.LoadCmdSegment64) && string(bytes.TrimRight(out[off+8:off+24], "\x00")) == "__LINKEDIT" {
			linkedit = off
		}
		off += int(size)
	}
	f.Close()
	if linkedit < 0 {
		t.Fatal("no __LINKEDIT")
	}
	if sigCmd < 0 {
		if !bytes.Equal(out[off:off+16], make([]byte, 16)) {
			t.Fatal("no room for LC_CODE_SIGNATURE")
		}
		sigCmd = off
		le.PutUint32(out[off:], lcCodeSignature)
		le.PutUint32(out[off+4:], 16)
		le.PutUint32(out[16:], ncmds+1)
		le.PutUint32(out[20:], sizeofcmds+16)
	}
	dataoff := (end + 15) &^ 15
	out = append(out[:end], make([]byte, dataoff-end)...)
	out = append(out, blob...)
	le.PutUint32(out[sigCmd+8:], uint32(dataoff))
	le.PutUint32(out[sigCmd+12:], uint32(len(blob)))
	segOff := le.Uint64(out[linkedit+40:])
	filesz := uint64(len(out)) - segOff
	le.PutUint64(out[linkedit+48:], filesz)
	le.PutUint64(out[linkedit+32:], (filesz+0x3fff)&^0x3fff)
	return out
}

func TestSameCodeIgnoresAdHocSignature(t *testing.T) {
	arm := darwinBinary(t, "arm64", "stage")
	amd := darwinBinary(t, "amd64", "stage")
	armSigned := adHocSign(t, arm, "re-signed arm64 blob, longer than Go's own")
	amdSigned := adHocSign(t, amd, "new amd64 blob")
	if bytes.Equal(arm, armSigned) || bytes.Equal(amd, amdSigned) {
		t.Fatal("signing changed nothing")
	}
	for name, c := range map[string]struct{ a, b []byte }{"arm64": {arm, armSigned}, "amd64": {amd, amdSigned}} {
		if !sameCode(c.a, c.b) || !sameCode(c.b, c.a) {
			t.Errorf("%s: a re-signed slice is not the same code", name)
		}
	}
	if err := checkUniversalCLI(fat(armSigned, amdSigned), map[string][]byte{"darwin-arm64": arm, "darwin-amd64": amd}); err != nil {
		t.Fatalf("ad-hoc signed universal CLI: %v", err)
	}

	other := darwinBinary(t, "amd64", "other")
	if sameCode(adHocSign(t, other, "new amd64 blob"), amd) {
		t.Error("another program's slice was the same code")
	}
	if sameCode(armSigned, amd) {
		t.Error("an arm64 slice was the same code as an amd64 one")
	}
	f, err := macho.NewFile(bytes.NewReader(amdSigned))
	if err != nil {
		t.Fatal(err)
	}
	text := f.Section("__text")
	f.Close()
	if text == nil {
		t.Fatal("no __text")
	}
	tampered := bytes.Clone(amdSigned)
	tampered[text.Offset+64] ^= 0xff
	if sameCode(tampered, amd) {
		t.Error("a slice with changed code was the same code")
	}
	if err := checkUniversalCLI(fat(armSigned, tampered), map[string][]byte{"darwin-arm64": arm, "darwin-amd64": amd}); err == nil {
		t.Error("a universal CLI with a changed slice was accepted")
	}
}
