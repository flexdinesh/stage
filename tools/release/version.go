package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"regexp"
	"strings"
)

var nonAlnum = regexp.MustCompile(`[^0-9a-z]+`)

// devVersion is a developer build's version: 0.0.0-dev.<branch>, the
// branch lowercased with every run of other characters as one dot, or
// 0.0.0-dev.g<commit12> when detached (branch "HEAD" or empty) or nothing
// usable remains.
func devVersion(branch, commit string) (string, error) {
	c := strings.TrimSuffix(commit, "-dirty")
	if !commitRe.MatchString(c) {
		return "", fmt.Errorf("commit %q is not a full revision", commit)
	}
	var ids []string
	if branch != "HEAD" {
		for _, id := range strings.Split(strings.Trim(nonAlnum.ReplaceAllString(strings.ToLower(branch), "."), "."), ".") {
			if id == "" {
				continue
			}
			// Semver forbids leading zeros in numeric identifiers.
			if strings.Trim(id, "0123456789") == "" && len(id) > 1 && id[0] == '0' {
				id = "b" + id
			}
			ids = append(ids, id)
		}
	}
	tail := strings.Join(ids, ".")
	if len(tail) > 40 {
		tail = strings.Trim(tail[:40], ".")
	}
	if tail == "" {
		tail = "g" + c[:12]
	}
	v := "0.0.0-dev." + tail
	if !validVersion(v) {
		return "", fmt.Errorf("derived version %q is invalid", v)
	}
	return v, nil
}

func cmdVersion(args []string, w io.Writer) error {
	fs := flag.NewFlagSet("version", flag.ContinueOnError)
	mode := fs.String("mode", "", "dev or release")
	version := fs.String("version", "", "release version X.Y.Z[-pre]")
	branch := fs.String("branch", "", "dev: current branch (HEAD when detached)")
	commit := fs.String("commit", "", "dev: full revision, -dirty suffix allowed")
	if err := fs.Parse(args); err != nil {
		return err
	}
	switch *mode {
	case "release":
		if !validVersion(*version) {
			return fmt.Errorf("VERSION %q is not X.Y.Z or X.Y.Z-pre (lowercase letters, digits, dots)", *version)
		}
		fmt.Fprintln(w, *version)
		return nil
	case "dev":
		v, err := devVersion(*branch, *commit)
		if err != nil {
			return err
		}
		fmt.Fprintln(w, v)
		return nil
	}
	return errors.New("-mode must be dev or release")
}
