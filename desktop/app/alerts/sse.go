package alerts

import (
	"bufio"
	"io"
	"strings"
)

// maxSSELine caps one SSE line (and so one event's data line).
const maxSSELine = 1 << 20

// sseEvent is one dispatched server-sent event.
type sseEvent struct {
	Event string
	ID    string
	Data  string
}

// sseReader is a minimal text/event-stream parser: event, id and data
// fields; comments, retry and unknown fields are ignored.
type sseReader struct {
	sc *bufio.Scanner
}

func newSSEReader(r io.Reader) *sseReader {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 4096), maxSSELine)
	return &sseReader{sc: sc}
}

// Next returns the next event with data or a name; io.EOF at the end of
// the stream, bufio.ErrTooLong for a line over 1 MiB.
func (r *sseReader) Next() (sseEvent, error) {
	var ev sseEvent
	var data []string
	for r.sc.Scan() {
		line := r.sc.Text()
		if line == "" {
			if len(data) == 0 && ev.Event == "" {
				ev = sseEvent{}
				continue
			}
			ev.Data = strings.Join(data, "\n")
			return ev, nil
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		field, value, _ := strings.Cut(line, ":")
		value = strings.TrimPrefix(value, " ")
		switch field {
		case "event":
			ev.Event = value
		case "data":
			data = append(data, value)
		case "id":
			if !strings.ContainsRune(value, 0) {
				ev.ID = value
			}
		}
	}
	if err := r.sc.Err(); err != nil {
		return sseEvent{}, err
	}
	return sseEvent{}, io.EOF
}
