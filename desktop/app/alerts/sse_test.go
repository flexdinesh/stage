package alerts

import (
	"bufio"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestSSEReader(t *testing.T) {
	in := "retry: 3000\n\n" +
		": ping\n\n" +
		"event: hello\nid: 5\ndata: {\"latestId\":5}\n\n" +
		"event: gap\r\ndata: {}\r\n\r\n" +
		"data: a\ndata:b\nunknown: x\n\n"
	r := newSSEReader(strings.NewReader(in))
	want := []sseEvent{
		{Event: "hello", ID: "5", Data: `{"latestId":5}`},
		{Event: "gap", Data: "{}"},
		{Data: "a\nb"},
	}
	for _, w := range want {
		got, err := r.Next()
		if err != nil {
			t.Fatal(err)
		}
		if got != w {
			t.Errorf("event = %+v, want %+v", got, w)
		}
	}
	if _, err := r.Next(); err != io.EOF {
		t.Errorf("end = %v, want EOF", err)
	}
}

func TestSSEReaderLineCap(t *testing.T) {
	in := "data: " + strings.Repeat("x", maxSSELine) + "\n\n"
	if _, err := newSSEReader(strings.NewReader(in)).Next(); !errors.Is(err, bufio.ErrTooLong) {
		t.Fatalf("err = %v, want ErrTooLong", err)
	}
}
