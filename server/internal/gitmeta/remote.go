package gitmeta

import (
	"net"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// RemoteIdentity normalizes a network origin to host[:port]/path without
// credentials, query parameters or fragments. Local and ambiguous paths
// yield an empty identity.
func RemoteIdentity(raw string) string {
	raw = strings.TrimSpace(raw)
	if !strings.Contains(raw, "://") {
		authority, path, ok := scpRemote(raw)
		if !ok || strings.Contains(path, "%") {
			return ""
		}
		raw = "ssh://" + authority + "/" + path
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	var defaultPort int
	switch u.Scheme {
	case "ssh", "git+ssh", "ssh+git":
		defaultPort = 22
	case "https":
		defaultPort = 443
	case "http":
		defaultPort = 80
	case "git":
		defaultPort = 9418
	default:
		return ""
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if host == "" || strings.HasSuffix(u.Host, ":") {
		return ""
	}
	bracketed := strings.HasPrefix(u.Host, "[")
	if bracketed || strings.Contains(host, ":") {
		addr, err := netip.ParseAddr(host)
		if !bracketed || err != nil || !addr.Is6() || addr.Zone() != "" {
			return ""
		}
	}
	port := u.Port()
	if port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 0 || n > 65535 {
			return ""
		}
		if n == defaultPort {
			port = ""
		}
	}
	if port != "" {
		host = net.JoinHostPort(host, port)
	} else if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	escaped := strings.ToLower(u.EscapedPath())
	for _, ambiguous := range []string{"%2f", "%5c", "%25", "%3f", "%23"} {
		if strings.Contains(escaped, ambiguous) {
			return ""
		}
	}
	for _, segment := range strings.Split(escaped, "/") {
		switch segment {
		case "%2e", "%2e%2e", "%2e.", ".%2e":
			return ""
		}
	}
	if !utf8.ValidString(u.Path) || strings.ContainsRune(u.Path, '\\') || strings.ContainsFunc(u.Path, unicode.IsControl) {
		return ""
	}
	path := strings.TrimSuffix(strings.Trim(u.Path, "/"), ".git")
	if path == "" || strings.HasSuffix(path, "/") {
		return ""
	}
	return host + "/" + path
}

func scpRemote(raw string) (authority, path string, ok bool) {
	authority, path, ok = strings.Cut(raw, ":")
	if strings.Contains(authority, "[") {
		head, tail, found := strings.Cut(raw, "]:")
		authority, path, ok = head+"]", tail, found
	}
	if !ok || strings.ContainsAny(authority, "/\\") {
		return "", "", false
	}
	host := authority[strings.LastIndexByte(authority, '@')+1:]
	if len(host) < 2 {
		return "", "", false
	}
	return authority, path, true
}
