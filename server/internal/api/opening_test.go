package api_test

import (
	"fmt"
	"net/url"
	"strings"
	"testing"

	"github.com/flexdinesh/stage/server/internal/opening"
	"github.com/flexdinesh/stage/server/internal/t3"
)

func TestAdoptedOpeningPrompt(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/opening")
	res, err := e.fake.LaunchThread(t.Context(), t3.LaunchInput{ProjectID: p.T3ProjectID, Title: "Opening"})
	if err != nil {
		t.Fatal(err)
	}
	id := res.ThreadID
	d, err := e.fake.ReadThread(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	run := d.ActiveRunID
	var adopted task
	e.must("POST", "/api/connections/1/sessions/"+url.PathEscape(id)+"/adopt", map[string]any{}, 201, &adopted)
	base := fmt.Sprintf("/api/tasks/%d/opening-prompt", adopted.ID)
	var got struct {
		Item *opening.Item `json:"item"`
	}
	e.must("GET", base, nil, 200, &got)
	if got.Item != nil {
		t.Fatalf("no messages yet: item = %+v", got.Item)
	}

	e.fake.AddItem(id, t3.TimelineItem{Type: "user_message", Text: "parent ask", Visibility: "inherited"})
	e.fake.AddItem(id, t3.TimelineItem{Type: "assistant_message", Text: "hi", RunID: run})
	position := e.fake.AddItem(id, t3.TimelineItem{Type: "user_message", Text: "build it", RunID: run, ItemID: "first", CreatedBy: "user"})
	e.fake.AddItem(id, t3.TimelineItem{Type: "user_message", Text: "and test it", RunID: run})
	e.must("GET", base, nil, 200, &got)
	if got.Item == nil || got.Item.ItemID != "first" || got.Item.Text != "build it" || got.Item.Kind != "user" || got.Item.Inherited {
		t.Fatalf("item = %+v", got.Item)
	}

	long := strings.Repeat("opening ", 500)
	if err := e.fake.UpdateItem(id, position, func(item *t3.TimelineItem) { item.Text = long }); err != nil {
		t.Fatal(err)
	}
	e.must("GET", base, nil, 200, &got)
	if got.Item == nil || !got.Item.Truncated {
		t.Fatal("opening preview must be bounded")
	}
	e.must("GET", base+"?full=true", nil, 200, &got)
	if got.Item == nil || got.Item.Text != long || got.Item.Truncated {
		t.Fatal("opening expansion must return only the first own prompt")
	}
	e.must("GET", "/api/tasks/999999/opening-prompt", nil, 404, nil)
	e.fake.RemoveThread(id)
	e.must("GET", base, nil, 404, nil)
}
