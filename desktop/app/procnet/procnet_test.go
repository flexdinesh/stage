package procnet

import (
	"net"
	"os"
	"runtime"
	"testing"
)

// TestSeesOwnListener proves the probe detects a TCP listener, so an
// empty answer elsewhere means none rather than a blind probe.
func TestSeesOwnListener(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("procnet is implemented on Linux and macOS")
	}
	before, err := ListeningTCP(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	after, err := ListeningTCP(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before)+1 {
		t.Fatalf("listeners before %v, after opening one %v", before, after)
	}
}
