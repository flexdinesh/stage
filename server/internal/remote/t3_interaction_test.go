package remote_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/remote/remotetest"
	"github.com/flexdinesh/stage/server/internal/t3"
)

// A worker's T3 adapter answers and sends through the proxy exactly as it
// does locally: the interactive trait is advertised, requests and answers
// cross the channel, and not pending, invalid and busy keep their meaning.
func TestT3InteractionOnTheWorker(t *testing.T) {
	fake := t3.NewFake()
	project := fake.AddProject("p", "/srv/p")
	launched, err := fake.LaunchThread(t.Context(), t3.LaunchInput{ProjectID: project.ID, Title: "t"})
	if err != nil {
		t.Fatal(err)
	}
	local := t3.NewConnectionAdapter(fake, 5)
	rig := remotetest.New(t, 2, map[int64]execution.Adapter{5: local})
	in, ok := execution.As[execution.Interactive](rig.Adapter(5))
	if !ok {
		t.Fatal("worker's T3 interaction hidden")
	}
	ref := execution.SessionRef{ConnectionID: 5, ExternalSessionID: launched.ThreadID}
	value := "v"
	id, _ := fake.AddUserInput(ref.ExternalSessionID, []t3.Question{{ID: "q", Question: "Pick?", Options: []t3.QuestionOption{{Label: "A", Value: &value, Preview: "a"}}}})
	got, err := in.Requests(t.Context(), ref)
	want, localErr := local.Requests(t.Context(), ref)
	if err != nil || localErr != nil || len(got) != 1 || !reflect.DeepEqual(got, want) {
		t.Fatalf("requests %+v, want %+v: %v %v", got, want, err, localErr)
	}
	var invalid *execution.Invalid
	if err := in.Respond(t.Context(), ref, id, execution.Response{Answers: map[string][]string{"q": {"B"}, "x": {"y"}}}); !errors.As(err, &invalid) {
		t.Fatal("invalid answer", err)
	}
	var busy *execution.Busy
	if err := in.Send(t.Context(), ref, execution.Message{Text: "hi"}); !errors.As(err, &busy) || busy.Reason == "" {
		t.Fatal("busy send", err)
	}
	if err := in.Respond(t.Context(), ref, id, execution.Response{Answers: map[string][]string{"q": {"A"}}}); err != nil {
		t.Fatal(err)
	}
	if len(fake.Responses) != 1 || !reflect.DeepEqual(fake.Responses[0].Answers, map[string]t3.Answer{"q": t3.TextAnswer("v")}) {
		t.Fatalf("answers: %+v", fake.Responses)
	}
	if err := in.Respond(t.Context(), ref, id, execution.Response{Answers: map[string][]string{"q": {"A"}}}); !errors.Is(err, execution.ErrNotFound) {
		t.Fatal("answered", err)
	}
	if err := in.Send(t.Context(), ref, execution.Message{Text: "hi"}); err != nil {
		t.Fatal(err)
	}
	if len(fake.Sent) != 1 || fake.Sent[0] != (t3.FakeSend{ThreadID: ref.ExternalSessionID, Message: "hi", Mode: t3.SendAuto}) {
		t.Fatalf("sent: %+v", fake.Sent)
	}
}
