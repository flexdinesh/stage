package procnet

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// listeningTCP matches the socket inodes of /proc/<pid>/fd against the
// LISTEN (state 0A) entries of /proc/<pid>/net/tcp and tcp6.
func listeningTCP(pid int) ([]string, error) {
	dir := filepath.Join("/proc", strconv.Itoa(pid))
	fds, err := os.ReadDir(filepath.Join(dir, "fd"))
	if err != nil {
		return nil, err
	}
	inodes := map[string]bool{}
	for _, fd := range fds {
		link, err := os.Readlink(filepath.Join(dir, "fd", fd.Name()))
		if err != nil {
			continue // closed meanwhile
		}
		if ino, ok := strings.CutPrefix(link, "socket:["); ok {
			inodes[strings.TrimSuffix(ino, "]")] = true
		}
	}
	var out []string
	for _, table := range []string{"tcp", "tcp6"} {
		f, err := os.Open(filepath.Join(dir, "net", table))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		sc := bufio.NewScanner(f)
		sc.Scan() // header
		for sc.Scan() {
			fields := strings.Fields(sc.Text())
			if len(fields) < 10 {
				continue
			}
			if fields[3] == "0A" && inodes[fields[9]] {
				out = append(out, fmt.Sprintf("%s %s", table, fields[1]))
			}
		}
		err = sc.Err()
		_ = f.Close()
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}
