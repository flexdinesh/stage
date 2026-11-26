package store

import (
	"regexp"
	"strings"
)

// previewMaxRunes caps the list-row preview (rows clamp it to one line).
const previewMaxRunes = 240

var (
	mdFence     = regexp.MustCompile("^(```|~~~)")
	mdRule      = regexp.MustCompile(`^[\s|:*_=-]+$`)
	mdLineLead  = regexp.MustCompile(`^(#{1,6}\s+|>\s?|[-*+]\s+(\[[ xX]\]\s+)?|\d+[.)]\s+)+`)
	mdImage     = regexp.MustCompile(`!\[([^\]]*)\]\([^)]*\)`)
	mdLink      = regexp.MustCompile(`\[([^\]]+)\]\([^)]*\)`)
	mdEmphasis  = regexp.MustCompile("(\\*\\*|__|~~|`)")
	mdTablePipe = regexp.MustCompile(`\s*\|\s*`)
	spaces      = regexp.MustCompile(`\s+`)
)

// PreviewText turns a markdown reply into one plain line: block markers,
// fences, rules, link/image syntax and emphasis marks dropped, whitespace
// collapsed, capped at previewMaxRunes. Single * and _ stay (identifiers).
func PreviewText(md string) string {
	var parts []string
	for _, line := range strings.Split(md, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || mdFence.MatchString(line) || mdRule.MatchString(line) {
			continue
		}
		line = mdLineLead.ReplaceAllString(line, "")
		line = mdImage.ReplaceAllString(line, "$1")
		line = mdLink.ReplaceAllString(line, "$1")
		line = mdEmphasis.ReplaceAllString(line, "")
		if strings.HasPrefix(line, "|") || strings.HasSuffix(line, "|") {
			line = strings.Trim(mdTablePipe.ReplaceAllString(line, " "), " ")
		}
		if line = strings.TrimSpace(line); line != "" {
			parts = append(parts, line)
		}
	}
	out := spaces.ReplaceAllString(strings.Join(parts, " "), " ")
	if r := []rune(out); len(r) > previewMaxRunes {
		out = strings.TrimSpace(string(r[:previewMaxRunes]))
	}
	return out
}

// Preview is the plain one-line preview of a reply excerpt (the task reads
// pass its first 1000 characters; nil when there is none or nothing is
// left).
func Preview(reply *string) *string {
	if reply == nil {
		return nil
	}
	p := PreviewText(*reply)
	if p == "" {
		return nil
	}
	return &p
}
