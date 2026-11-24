package storetest

import (
	"testing"

	"github.com/flexdinesh/stage/server/internal/db"
)

func init() {
	register("TagForKey", caseTagForKey)
}

func caseTagForKey(t *testing.T, open Factory) {
	for key, want := range map[string]string{
		"task:7":         "task-7",
		"thread:1:ext-1": "thread-1:ext-1",
		"thread:1:a:b":   "thread-1:a:b",
		"notification:3": "",
		"task":           "",
		"":               "",
	} {
		if got := db.TagForKey(key); got != want {
			t.Errorf("TagForKey(%q) = %q, want %q", key, got, want)
		}
	}
}
