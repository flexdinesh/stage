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
