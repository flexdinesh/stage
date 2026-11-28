package claudecode

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
)

// trustKey is the one key Stage writes in Claude's state file
// (docs/adr/0012-harness-workspace-add.md decision 8).
const trustKey = "hasTrustDialogAccepted"

// setTrust returns doc with projects[dir].hasTrustDialogAccepted set to
// true. It changes only the bytes it must: the value, or one inserted
// member (the key, a minimal project entry, or the projects object), laid
// out like its neighbors. Every other key keeps its bytes and order. Like
// JSON.parse, the last of duplicate keys counts.
func setTrust(doc []byte, dir string) ([]byte, error) {
	top, err := scanDocument(doc)
	if err != nil {
		return nil, err
	}
	style := layoutOf(doc, top)
	var out []byte
	switch projects := top.last("projects"); {
	case projects == nil:
		out = style.insert(doc, top, "projects", dir, trustKey)
	case projects.object == nil:
		return nil, errors.New("its projects value is not an object")
	default:
		switch entry := projects.object.last(dir); {
		case entry == nil:
			out = style.insert(doc, *projects.object, dir, trustKey)
		case entry.object == nil:
			return nil, fmt.Errorf("its entry for %s is not an object", dir)
		default:
			if flag := entry.object.last(trustKey); flag != nil {
				out = splice(doc, flag.valueStart, flag.valueEnd, "true")
			} else {
				out = style.insert(doc, *entry.object, trustKey)
			}
		}
	}
	if err := sameExceptTrust(doc, out, dir); err != nil {
		return nil, err
	}
	return out, nil
}

// sameExceptTrust proves the patch: out parses to doc with only the
// trust key of dir set.
func sameExceptTrust(doc, out []byte, dir string) error {
	var before, after any
	if err := decodeNumbers(doc, &before); err != nil {
		return err
	}
	if err := decodeNumbers(out, &after); err != nil {
		return fmt.Errorf("the patched state is not JSON: %w", err)
	}
	root, _ := before.(map[string]any)
	projects, ok := root["projects"].(map[string]any)
	if !ok {
		projects = map[string]any{}
		root["projects"] = projects
	}
	entry, ok := projects[dir].(map[string]any)
	if !ok {
		entry = map[string]any{}
		projects[dir] = entry
	}
	entry[trustKey] = true
	if !reflect.DeepEqual(before, after) {
		return errors.New("the patch changed more than the trust key")
	}
	return nil
}

func decodeNumbers(b []byte, v any) error {
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	return d.Decode(v)
}

// jsonObject is a scanned object: its braces and members, by byte offset.
type jsonObject struct {
	open, close int
	members     []jsonMember
}

type jsonMember struct {
	key                  string
	keyStart, keyEnd     int
	valueStart, valueEnd int
	object               *jsonObject // the value, when it is an object
}

func (o jsonObject) last(key string) *jsonMember {
	for i := len(o.members) - 1; i >= 0; i-- {
		if o.members[i].key == key {
			return &o.members[i]
		}
	}
	return nil
}

func scanDocument(doc []byte) (jsonObject, error) {
	s := scanner{b: doc}
	s.space()
	if s.peek() != '{' {
		return jsonObject{}, errors.New("it is not a JSON object")
	}
	top, err := s.object()
	if err != nil {
		return jsonObject{}, err
	}
	if s.space(); s.i != len(doc) {
		return jsonObject{}, errors.New("it has text after its JSON object")
	}
	return top, nil
}

type scanner struct {
	b []byte
	i int
}

var errMalformed = errors.New("it is not valid JSON")

func (s *scanner) peek() byte {
	if s.i < len(s.b) {
		return s.b[s.i]
	}
	return 0
}

func (s *scanner) space() {
	for s.i < len(s.b) && strings.IndexByte(" \t\r\n", s.b[s.i]) >= 0 {
		s.i++
	}
}

func (s *scanner) object() (jsonObject, error) {
	o := jsonObject{open: s.i}
	s.i++
	s.space()
	if s.peek() == '}' {
		o.close = s.i
		s.i++
		return o, nil
	}
	for {
		s.space()
		var m jsonMember
		m.keyStart = s.i
		raw, err := s.str()
		if err != nil {
			return o, err
		}
		if err := json.Unmarshal(raw, &m.key); err != nil {
			return o, errMalformed
		}
		m.keyEnd = s.i
		s.space()
		if s.peek() != ':' {
			return o, errMalformed
		}
		s.i++
		s.space()
		m.valueStart = s.i
		if s.peek() == '{' {
			child, err := s.object()
			if err != nil {
				return o, err
			}
			m.object = &child
		} else if err := s.value(); err != nil {
			return o, err
		}
		m.valueEnd = s.i
		o.members = append(o.members, m)
		s.space()
		switch s.peek() {
		case ',':
			s.i++
		case '}':
			o.close = s.i
			s.i++
			return o, nil
		default:
			return o, errMalformed
		}
	}
}

// str scans a string token and returns its raw bytes.
func (s *scanner) str() ([]byte, error) {
	if s.peek() != '"' {
		return nil, errMalformed
	}
	start := s.i
	for s.i++; s.i < len(s.b); s.i++ {
		switch s.b[s.i] {
		case '\\':
			s.i++
		case '"':
			s.i++
			return s.b[start:s.i], nil
		}
	}
	return nil, errMalformed
}

// value scans any value except an object (arrays may hold objects).
func (s *scanner) value() error {
	start := s.i
	switch c := s.peek(); {
	case c == '"':
		_, err := s.str()
		return err
	case c == '[':
		depth := 0
		for s.i < len(s.b) {
			switch s.b[s.i] {
			case '"':
				if _, err := s.str(); err != nil {
					return err
				}
				continue
			case '[', '{':
				depth++
			case ']', '}':
				depth--
			}
			s.i++
			if depth == 0 {
				break
			}
		}
		if depth != 0 || !json.Valid(s.b[start:s.i]) {
			return errMalformed
		}
		return nil
	default:
		for s.i < len(s.b) && strings.IndexByte(",}] \t\r\n", s.b[s.i]) < 0 {
			s.i++
		}
		if s.i == start || !json.Valid(s.b[start:s.i]) {
			return errMalformed
		}
		return nil
	}
}

// layout is the document's formatting: its indent unit ("" when compact)
// and the bytes between a key and its value.
type layout struct {
	unit  string
	colon string
}

// layoutOf reads the formatting from the top object's first member.
func layoutOf(doc []byte, top jsonObject) layout {
	if len(top.members) == 0 {
		if bytes.IndexByte(doc, '\n') >= 0 {
			return layout{unit: "  ", colon: ": "}
		}
		return layout{colon: ":"}
	}
	first := top.members[0]
	l := layout{colon: string(doc[first.keyEnd:first.valueStart])}
	lead := string(doc[top.open+1 : first.keyStart])
	if i := strings.LastIndexByte(lead, '\n'); i >= 0 {
		l.unit = strings.TrimRight(lead[i+1:], "\r")
	}
	return l
}

// member renders keys[0]: {keys[1]: {...: true}} with this layout.
func (l layout) member(colon string, keys ...string) string {
	if len(keys) == 1 {
		return quote(keys[0]) + colon + "true"
	}
	inner := l.member(colon, keys[1:]...)
	if l.unit == "" {
		return quote(keys[0]) + colon + "{" + inner + "}"
	}
	return quote(keys[0]) + colon + "{\n" + l.unit + indentLines(inner, l.unit) + "\n}"
}

// insert adds the member for keys as o's last member, separated, spaced
// and indented like o's other members (or, in an empty object, one level
// deeper than o's line).
func (l layout) insert(doc []byte, o jsonObject, keys ...string) []byte {
	if len(o.members) == 0 {
		if l.unit == "" {
			return splice(doc, o.open+1, o.close, l.member(l.colon, keys...))
		}
		outer := lineIndent(doc, o.open)
		indent := outer + l.unit
		return splice(doc, o.open+1, o.close, "\n"+indent+indentLines(l.member(l.colon, keys...), indent)+"\n"+outer)
	}
	last := o.members[len(o.members)-1]
	start := o.open + 1
	if len(o.members) > 1 {
		start = bytes.LastIndexByte(doc[:last.keyStart], ',') + 1
	}
	sep := string(doc[start:last.keyStart])
	colon := string(doc[last.keyEnd:last.valueStart])
	nl := strings.LastIndexByte(sep, '\n')
	if nl < 0 {
		// A compact object stays compact.
		return splice(doc, last.valueEnd, last.valueEnd, ","+sep+layout{colon: colon}.member(colon, keys...))
	}
	indent := sep[nl+1:]
	return splice(doc, last.valueEnd, last.valueEnd, ","+sep+indentLines(l.member(colon, keys...), indent))
}

// indentLines indents every line after the first by indent.
func indentLines(s, indent string) string {
	return strings.ReplaceAll(s, "\n", "\n"+indent)
}

// lineIndent is the leading whitespace of the line holding offset i.
func lineIndent(doc []byte, i int) string {
	start := bytes.LastIndexByte(doc[:i], '\n') + 1
	end := start
	for end < len(doc) && (doc[end] == ' ' || doc[end] == '\t') {
		end++
	}
	return string(doc[start:end])
}

func splice(doc []byte, start, end int, text string) []byte {
	out := make([]byte, 0, len(doc)+len(text))
	out = append(out, doc[:start]...)
	out = append(out, text...)
	return append(out, doc[end:]...)
}

// quote encodes s as a JSON string without HTML escaping, as JSON.stringify.
func quote(s string) string {
	var b bytes.Buffer
	e := json.NewEncoder(&b)
	e.SetEscapeHTML(false)
	_ = e.Encode(s)
	return strings.TrimSuffix(b.String(), "\n")
}
