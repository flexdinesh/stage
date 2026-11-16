// Command release assembles and verifies Stage release assets
// (docs/release.md): one version and commit for the desktop app, its
// bundled CLI/server and the headless CLI/server, a manifest
// (stage-release.json) and checksums.txt. scripts/release.sh drives it.
//
//	release version -mode dev|release [-version X.Y.Z]
//	release assemble -out DIR ...
//	release verify [-smoke] DIR
//
// Standard library only, so it builds with the pinned Go alone.
package main

import (
	"fmt"
	"io"
	"os"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: release version|assemble|verify [flags]")
		return 2
	}
	var err error
	switch args[0] {
	case "version":
		err = cmdVersion(args[1:], stdout)
	case "assemble":
		err = cmdAssemble(args[1:], stdout)
	case "verify":
		err = cmdVerify(args[1:], stdout)
	default:
		fmt.Fprintf(stderr, "release: unknown command %q\n", args[0])
		return 2
	}
	if err != nil {
		fmt.Fprintf(stderr, "release %s: %v\n", args[0], err)
		return 1
	}
	return 0
}
