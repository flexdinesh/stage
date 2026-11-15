package procnet

import (
	"errors"
	"os/exec"
	"strconv"
	"strings"
)

// listeningTCP asks lsof, which exits 1 when it finds nothing.
func listeningTCP(pid int) ([]string, error) {
	out, err := exec.Command("lsof", "-nP", "-a", "-p", strconv.Itoa(pid), "-iTCP", "-sTCP:LISTEN", "-Fn").Output()
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var addrs []string
	for _, line := range strings.Split(string(out), "\n") {
		if addr, ok := strings.CutPrefix(line, "n"); ok {
			addrs = append(addrs, addr)
		}
	}
	return addrs, nil
}
