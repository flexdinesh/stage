package api

import (
	"testing"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/t3"
)

func TestMatchSource(t *testing.T) {
	str := func(s string) *string { return &s }
	row := &db.Thread{Title: "Fix auth refresh", Branch: str("feat/token-renew"), ProjectTitle: str("Stage web")}
	for _, c := range []struct {
		query  string
		source t3.SearchSource
		row    *db.Thread
		want   string
	}{
		{"auth", t3.SearchTitle, row, "title"},
		{"refresh auth", t3.SearchTitle, row, "title"}, // every word, any order
		{"TOKEN", t3.SearchTitle, row, "branch"},
		{"stage", t3.SearchTitle, row, "project"},
		{"#41", t3.SearchTitle, row, "pr"}, // T3 matched something stage does not mirror
		{"#41", t3.SearchTitle, &db.Thread{Title: "x"}, "pr"},
		{"anything", t3.SearchTitle, nil, "title"}, // not synced: trust T3
		{"anything", t3.SearchUser, row, "user"},
		{"anything", t3.SearchAssistant, nil, "assistant"},
	} {
		if got := matchSource(c.query, c.source, c.row); got != c.want {
			t.Errorf("matchSource(%q, %s) = %q, want %q", c.query, c.source, got, c.want)
		}
	}
}
