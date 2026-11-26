package harness

import (
	"net/url"
	"slices"

	"github.com/flexdinesh/stage/server/internal/execution"
)

// AllowsDisplayLink applies the web and native scheme policy for this harness.
func (d Descriptor) AllowsDisplayLink(link string) bool {
	if link == "" {
		return true
	}
	u, err := url.Parse(link)
	if err != nil || u.User != nil || !u.IsAbs() {
		return false
	}
	if u.Scheme == "http" || u.Scheme == "https" {
		return u.Hostname() != "" && u.Opaque == ""
	}
	return slices.Contains(d.LinkSchemes, u.Scheme) && (u.Host != "" || u.Opaque != "" || u.Path != "")
}

// DisplayFacts removes invalid metadata and unsafe links from a relay. A
// malformed display value must not turn a successful mutation into a failure.
func (d Descriptor) DisplayFacts(facts []execution.Fact) []execution.Fact {
	var display []execution.Fact
	seen := make(map[string]bool)
	for _, f := range facts {
		if seen[f.ID] || execution.ValidateFacts([]execution.Fact{f}) != nil {
			continue
		}
		seen[f.ID] = true
		if !d.AllowsDisplayLink(f.Link) {
			f.Link = ""
		}
		display = append(display, f)
	}
	return display
}
