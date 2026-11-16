package main

import (
	"bytes"
	"debug/macho"
	"errors"
	"fmt"
	"strings"
)

// stampPrefix starts the build stamp that scripts/release.sh links into
// every stage CLI (buildinfo.Stamp) and desktop executable (main.appBuild):
// "stage-build/1 identity=<id> version=<v> commit=<sha>;". Go omits
// -ldflags from the embedded build info under -trimpath, so the stamp is
// how verification reads binaries it cannot run.
const stampPrefix = "stage-build/1 "

// stampFor is the stamp of a build.
func stampFor(b build) string {
	return stampPrefix + "identity=" + b.Identity + " version=" + b.Version + " commit=" + b.Commit + ";"
}

// binaryStamps returns the stamp of each architecture slice of a binary
// (one for ELF or thin Mach-O, one per slice of a universal Mach-O). A
// slice without exactly one distinct stamp is an error.
func binaryStamps(b []byte) ([]string, error) {
	parts, err := machoSlices(b)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(parts))
	for i, p := range parts {
		found := map[string]bool{}
		for rest := p; ; {
			j := bytes.Index(rest, []byte(stampPrefix))
			if j < 0 {
				break
			}
			rest = rest[j:]
			end := bytes.IndexByte(rest, ';')
			if end < 0 || end > 512 {
				return nil, fmt.Errorf("slice %d: unterminated build stamp", i)
			}
			found[string(rest[:end+1])] = true
			rest = rest[end+1:]
		}
		if len(found) != 1 {
			return nil, fmt.Errorf("slice %d: %d build stamps, want 1", i, len(found))
		}
		for s := range found {
			out = append(out, s)
		}
	}
	return out, nil
}

// checkStamp requires every slice of a binary to carry b's stamp.
func checkStamp(data []byte, b build) error {
	stamps, err := binaryStamps(data)
	if err != nil {
		return err
	}
	want := stampFor(b)
	for _, s := range stamps {
		if s != want {
			return fmt.Errorf("stamped %q, want %q", strings.TrimSuffix(s, ";"), strings.TrimSuffix(want, ";"))
		}
	}
	return nil
}

// machoSlices splits a universal (fat) Mach-O into its architecture
// slices, in order; any other binary is returned whole.
func machoSlices(b []byte) ([][]byte, error) {
	ff, err := macho.NewFatFile(bytes.NewReader(b))
	if err != nil {
		var fe *macho.FormatError
		if errors.Is(err, macho.ErrNotFat) || errors.As(err, &fe) {
			return [][]byte{b}, nil
		}
		return nil, err
	}
	defer ff.Close()
	var out [][]byte
	for _, a := range ff.Arches {
		end := uint64(a.Offset) + uint64(a.Size)
		if end > uint64(len(b)) {
			return nil, fmt.Errorf("universal binary slice %v overruns the file", a.Cpu)
		}
		out = append(out, b[a.Offset:end])
	}
	return out, nil
}

// sameCode reports whether two Mach-O slices are the same file apart from
// their code signatures. MyGo ad-hoc signs a macOS app's nested code on a
// Mac, which re-signs both slices of the universal CLI (Go's linker signs
// only darwin/arm64), so the bundled slices differ from the standalone CLIs
// in their signature alone: the LC_CODE_SIGNATURE command, its blob at the
// end of __LINKEDIT, and __LINKEDIT's sizes. Every other byte must match.
func sameCode(a, b []byte) bool {
	if bytes.Equal(a, b) {
		return true
	}
	na, okA := withoutSignature(a)
	nb, okB := withoutSignature(b)
	return okA && okB && bytes.Equal(na, nb)
}

const lcCodeSignature = 0x1d

// withoutSignature returns a copy of a thin 64-bit Mach-O with its code
// signature taken out: LC_CODE_SIGNATURE removed from the load commands
// (ncmds and sizeofcmds to match), __LINKEDIT's vmsize and filesize zeroed,
// and the file cut where the signature blob starts, less the zero padding
// before it.
func withoutSignature(b []byte) ([]byte, bool) {
	f, err := macho.NewFile(bytes.NewReader(b))
	if err != nil || f.Magic != macho.Magic64 {
		return nil, false
	}
	defer f.Close()
	bo := f.ByteOrder
	const header = 32 // mach_header_64
	out := bytes.Clone(b)
	ncmds, sizeofcmds := bo.Uint32(out[16:]), bo.Uint32(out[20:])
	cmdsEnd := uint64(header) + uint64(sizeofcmds)
	if cmdsEnd > uint64(len(out)) {
		return nil, false
	}
	cut := uint64(len(out))
	off := uint64(header)
	for range ncmds {
		if off+8 > cmdsEnd {
			return nil, false
		}
		cmd, size := bo.Uint32(out[off:]), uint64(bo.Uint32(out[off+4:]))
		if size < 8 || off+size > cmdsEnd {
			return nil, false
		}
		switch {
		case cmd == lcCodeSignature:
			if size < 16 {
				return nil, false
			}
			cut = min(cut, uint64(bo.Uint32(out[off+8:])))
			// Close the gap; the freed tail becomes header padding.
			copy(out[off:cmdsEnd], out[off+size:cmdsEnd])
			clear(out[cmdsEnd-size : cmdsEnd])
			cmdsEnd -= size
			bo.PutUint32(out[16:], bo.Uint32(out[16:])-1)
			bo.PutUint32(out[20:], uint32(cmdsEnd-header))
			continue
		case cmd == uint32(macho.LoadCmdSegment64) && size >= 72 &&
			string(bytes.TrimRight(out[off+8:off+24], "\x00")) == "__LINKEDIT":
			// segment_command_64: vmsize at 32, filesize at 48.
			clear(out[off+32 : off+40])
			clear(out[off+48 : off+56])
		}
		off += size
	}
	if cut < cmdsEnd {
		return nil, false
	}
	return bytes.TrimRight(out[:cut], "\x00"), true
}
